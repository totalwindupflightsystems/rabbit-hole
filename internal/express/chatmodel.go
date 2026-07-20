// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// ChatModelConfig configures a RealChatModel.
//
// Endpoint should be the base URL of an OpenAI-compatible API
// (e.g. "http://127.0.0.1:11434/v1" for Ollama, "https://api.openai.com/v1"
// for OpenAI). The "/chat/completions" suffix is appended automatically.
type ChatModelConfig struct {
	Endpoint string        // RABBITHOLE_CHAT_MODEL_ENDPOINT
	Model    string        // RABBITHOLE_CHAT_MODEL_NAME
	APIKey   string        // RABBITHOLE_CHAT_MODEL_API_KEY
	Timeout  time.Duration // per-request timeout (default 30s)
	Logger   *slog.Logger
}

// ChatConfigFromEnv builds a ChatModelConfig from environment variables.
// Returns an error when required values are missing (caller falls back
// to stubChatModel). The env vars read are:
//
//   - RABBITHOLE_CHAT_MODEL_ENDPOINT (required)
//   - RABBITHOLE_CHAT_MODEL_NAME     (required)
//   - RABBITHOLE_CHAT_MODEL_API_KEY  (required)
func ChatConfigFromEnv() (ChatModelConfig, error) {
	endpoint := strings.TrimRight(os.Getenv("RABBITHOLE_CHAT_MODEL_ENDPOINT"), "/")
	model := os.Getenv("RABBITHOLE_CHAT_MODEL_NAME")
	apiKey := os.Getenv("RABBITHOLE_CHAT_MODEL_API_KEY")

	if endpoint == "" {
		return ChatModelConfig{}, errors.New("RABBITHOLE_CHAT_MODEL_ENDPOINT is not set")
	}
	if model == "" {
		return ChatModelConfig{}, errors.New("RABBITHOLE_CHAT_MODEL_NAME is not set")
	}
	if apiKey == "" {
		return ChatModelConfig{}, errors.New("RABBITHOLE_CHAT_MODEL_API_KEY is not set")
	}

	return ChatModelConfig{
		Endpoint: endpoint,
		Model:    model,
		APIKey:   apiKey,
		Timeout:  30 * time.Second,
	}, nil
}

// RealChatModel is an OpenAI-compatible ChatModel implementation.
//
// It calls a chat completions endpoint for both TranslateQuery and
// GenerateAnswer. TranslateQuery asks the model to emit a JSON object
// describing the search; GenerateAnswer asks the model to summarize the
// flows in natural language.
type RealChatModel struct {
	cfg    ChatModelConfig
	client *http.Client
	logger *slog.Logger
}

// NewRealChatModel constructs a RealChatModel from config. The HTTP
// client is injectable for tests (pass nil to use a default client with
// the configured timeout).
func NewRealChatModel(cfg ChatModelConfig, client *http.Client) *RealChatModel {
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &RealChatModel{
		cfg:    cfg,
		client: client,
		logger: logger,
	}
}

// --- TranslateQuery ---

// translateSystemPrompt instructs the model to emit a JSON SearchRequest
// or a null body. The model is told the valid flow phases and outcomes
// so it can constrain filters to known values.
const translateSystemPrompt = `You translate natural language queries about agent activity into a structured JSON search request.

Return ONLY a JSON object with these fields:
{
  "query":   "<search string, keywords or phrase>",
  "limit":   <integer between 1 and 200, default 50>,
  "phases":  [<zero or more of: "observation", "deliberation", "action", "verification">],
  "outcomes":[<zero or more of: "success", "failure", "timeout", "unknown">]
}

Rules:
- "query" should be a compact keywords string, not the full question.
- If the user asks about failures, include "failure" in outcomes.
- If unsure about phases or outcomes, return empty arrays.
- Do NOT add commentary, explanation, or markdown — JSON only.`

// TranslateQuery implements ChatModel.
func (m *RealChatModel) TranslateQuery(ctx context.Context, message string) (*types.SearchRequest, error) {
	body := chatRequest{
		Model: m.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: translateSystemPrompt},
			{Role: "user", Content: message},
		},
		Temperature: 0,
	}

	raw, err := m.complete(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("translate: %w", err)
	}

	req, err := parseTranslateResponse(raw)
	if err != nil {
		// Fall back to using the raw message as the query.
		m.logger.Warn("translate response unparseable, using raw message",
			"err", err, "raw", truncate(raw, 200))
		return &types.SearchRequest{Query: message, Limit: 50}, nil
	}
	return req, nil
}

// parseTranslateResponse extracts a SearchRequest from a model response.
// The model is expected to emit a JSON object; we strip common wrappers
// (markdown fences, surrounding prose) and fall back to defaults if any
// field is missing or malformed.
func parseTranslateResponse(raw string) (*types.SearchRequest, error) {
	cleaned := extractJSON(raw)
	if cleaned == "" {
		return nil, errors.New("no JSON object found in model response")
	}

	var parsed struct {
		Query    string   `json:"query"`
		Limit    int      `json:"limit"`
		Phases   []string `json:"phases"`
		Outcomes []string `json:"outcomes"`
	}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	if parsed.Query == "" {
		return nil, errors.New("query field is empty")
	}
	if parsed.Limit <= 0 {
		parsed.Limit = 50
	}
	if parsed.Limit > 200 {
		parsed.Limit = 200
	}

	req := &types.SearchRequest{
		Query: parsed.Query,
		Limit: parsed.Limit,
	}

	for _, p := range parsed.Phases {
		fp := types.FlowPhase(p)
		if isValidPhase(fp) {
			req.Categories = append(req.Categories, fp)
		}
	}
	for _, o := range parsed.Outcomes {
		fo := types.FlowOutcome(o)
		if isValidOutcome(fo) {
			req.Outcomes = append(req.Outcomes, fo)
		}
	}

	return req, nil
}

func isValidPhase(p types.FlowPhase) bool {
	switch p {
	case types.FlowPhaseObservation,
		types.FlowPhaseDeliberation,
		types.FlowPhaseAction,
		types.FlowPhaseVerification:
		return true
	}
	return false
}

func isValidOutcome(o types.FlowOutcome) bool {
	switch o {
	case types.FlowOutcomeSuccess,
		types.FlowOutcomeFailure,
		types.FlowOutcomeTimeout,
		types.FlowOutcomeUnknown:
		return true
	}
	return false
}

// --- GenerateAnswer ---

// answerSystemPrompt tells the model to summarize a set of agent flows.
const answerSystemPrompt = `You are a helpful assistant summarizing recorded agent activity.
Answer the user's question using ONLY the provided flow data.
Be concise — 1-3 sentences unless the question demands more.
Reference specific flows when relevant (use the flow ID).
If the data does not answer the question, say so plainly.`

// GenerateAnswer implements ChatModel.
func (m *RealChatModel) GenerateAnswer(ctx context.Context, message string, flows []types.Flow) (string, error) {
	if len(flows) == 0 {
		return "I couldn't find any matching activity for your query.", nil
	}

	contextJSON, err := json.Marshal(flows)
	if err != nil {
		return "", fmt.Errorf("marshal flows: %w", err)
	}

	userPrompt := fmt.Sprintf(
		"User question: %s\n\nRecorded flows (JSON):\n%s",
		message, string(contextJSON),
	)

	body := chatRequest{
		Model: m.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: answerSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.2,
	}

	return m.complete(ctx, body)
}

// --- HTTP plumbing ---

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// complete posts the chat request and returns the assistant text.
func (m *RealChatModel) complete(ctx context.Context, req chatRequest) (string, error) {
	endpoint := m.cfg.Endpoint + "/chat/completions"

	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("chat API status %d: %s", resp.StatusCode, truncate(string(bodyBytes), 500))
	}

	var parsed chatResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("chat API error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("chat API returned no choices")
	}

	text := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if text == "" {
		return "", errors.New("chat API returned empty content")
	}
	return text, nil
}

// --- helpers ---

// extractJSON pulls the first {...} block out of a string. Useful when
// models occasionally wrap JSON in markdown fences or preamble prose.
func extractJSON(s string) string {
	// Strip markdown fences if present.
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}

	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}