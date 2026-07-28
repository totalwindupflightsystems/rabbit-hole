package express

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// stubChatServer is a tiny OpenAI-compatible chat completions endpoint
// used by the RealChatModel tests. It echoes back a response built by
// the provided handler so each test can simulate a particular LLM.
func stubChatServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// defaultTranslateHandler returns a JSON body matching translateSystemPrompt.
func defaultTranslateHandler(t *testing.T, query string, limit int, phases, outcomes []string) http.HandlerFunc {
	t.Helper()
	body := map[string]any{
		"query":    query,
		"limit":    limit,
		"phases":   phases,
		"outcomes": outcomes,
	}
	b, _ := json.Marshal(body)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing or bad Authorization header: %q", got)
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": string(b)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func TestChatConfigFromEnv(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://example.invalid/v1")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "test-model")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "sk-test")

	cfg, err := ChatConfigFromEnv()
	if err != nil {
		t.Fatalf("ChatConfigFromEnv: %v", err)
	}
	if cfg.Endpoint != "http://example.invalid/v1" {
		t.Errorf("endpoint=%q", cfg.Endpoint)
	}
	if cfg.Model != "test-model" {
		t.Errorf("model=%q", cfg.Model)
	}
	if cfg.APIKey != "sk-test" {
		t.Errorf("apikey=%q", cfg.APIKey)
	}
}

func TestChatConfigFromEnv_MissingEndpoint(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")
	if _, err := ChatConfigFromEnv(); err == nil {
		t.Error("expected error for missing endpoint")
	}
}

func TestChatConfigFromEnv_MissingModel(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://x")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")
	if _, err := ChatConfigFromEnv(); err == nil {
		t.Error("expected error for missing model")
	}
}

func TestChatConfigFromEnv_MissingAPIKey(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://x")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "")
	if _, err := ChatConfigFromEnv(); err == nil {
		t.Error("expected error for missing api key")
	}
}

func TestRealChatModel_TranslateQuery_Success(t *testing.T) {
	ts := stubChatServer(t, defaultTranslateHandler(t, "auth.go failures", 25,
		[]string{"action"}, []string{"failure"}))

	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "m", APIKey: "sk-test", Timeout: 5 * time.Second,
	}, ts.Client())

	got, err := m.TranslateQuery(context.Background(), "what failed in auth.go?")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if got.Query != "auth.go failures" {
		t.Errorf("Query=%q", got.Query)
	}
	if got.Limit != 25 {
		t.Errorf("Limit=%d", got.Limit)
	}
	if len(got.Categories) != 1 || got.Categories[0] != types.FlowPhaseAction {
		t.Errorf("Categories=%v", got.Categories)
	}
	if len(got.Outcomes) != 1 || got.Outcomes[0] != types.FlowOutcomeFailure {
		t.Errorf("Outcomes=%v", got.Outcomes)
	}
}

func TestRealChatModel_TranslateQuery_DefaultsApplied(t *testing.T) {
	// Model omits limit and arrays — we should default sensibly.
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"query":"read"}`}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "m", APIKey: "sk",
	}, ts.Client())

	got, err := m.TranslateQuery(context.Background(), "find read operations")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if got.Query != "read" {
		t.Errorf("Query=%q", got.Query)
	}
	if got.Limit != 50 {
		t.Errorf("Limit default=%d, want 50", got.Limit)
	}
}

func TestRealChatModel_TranslateQuery_StripsMarkdownFences(t *testing.T) {
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": "```json\n{\"query\":\"write\",\"limit\":10}\n```",
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "m", APIKey: "sk",
	}, ts.Client())

	got, err := m.TranslateQuery(context.Background(), "show write ops")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if got.Query != "write" || got.Limit != 10 {
		t.Errorf("got %+v", got)
	}
}

func TestRealChatModel_TranslateQuery_FallbackOnGarbage(t *testing.T) {
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "Sorry, I cannot help."}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "m", APIKey: "sk",
	}, ts.Client())

	got, err := m.TranslateQuery(context.Background(), "raw user message")
	if err != nil {
		t.Fatalf("TranslateQuery should not error on parse failure: %v", err)
	}
	if got.Query != "raw user message" {
		t.Errorf("fallback Query=%q", got.Query)
	}
	if got.Limit != 50 {
		t.Errorf("fallback Limit=%d", got.Limit)
	}
}

func TestRealChatModel_TranslateQuery_InvalidPhaseFiltered(t *testing.T) {
	ts := stubChatServer(t, defaultTranslateHandler(t, "x", 5,
		[]string{"action", "bogus_phase"}, []string{"failure", "wrong"}))

	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "m", APIKey: "sk",
	}, ts.Client())

	got, err := m.TranslateQuery(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Categories) != 1 || got.Categories[0] != types.FlowPhaseAction {
		t.Errorf("Categories=%v (should drop invalid)", got.Categories)
	}
	if len(got.Outcomes) != 1 || got.Outcomes[0] != types.FlowOutcomeFailure {
		t.Errorf("Outcomes=%v (should drop invalid)", got.Outcomes)
	}
}

func TestRealChatModel_TranslateQuery_HTTPError(t *testing.T) {
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	})

	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "m", APIKey: "sk",
	}, ts.Client())

	if _, err := m.TranslateQuery(context.Background(), "x"); err == nil {
		t.Error("expected error on HTTP 401")
	}
}

func TestRealChatModel_GenerateAnswer(t *testing.T) {
	var gotBody chatRequest
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "Helios edited auth.go and saw 1 failure."}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "test-model", APIKey: "sk",
	}, ts.Client())

	flows := []types.Flow{
		{ID: "f1", SessionID: "s1", Intent: "edit_file", Outcome: types.FlowOutcomeSuccess, Description: "edit auth.go"},
		{ID: "f2", SessionID: "s1", Intent: "run_test", Outcome: types.FlowOutcomeFailure, Description: "auth_test failed"},
	}

	ans, err := m.GenerateAnswer(context.Background(), "what did helios do?", flows)
	if err != nil {
		t.Fatalf("GenerateAnswer: %v", err)
	}
	if !strings.Contains(ans, "auth.go") {
		t.Errorf("answer=%q", ans)
	}

	// Verify the request shape: model name, system prompt present, flows JSON present.
	if gotBody.Model != "test-model" {
		t.Errorf("model=%q", gotBody.Model)
	}
	if len(gotBody.Messages) < 2 {
		t.Fatalf("messages=%d", len(gotBody.Messages))
	}
	if gotBody.Messages[0].Role != "system" {
		t.Errorf("first role=%q", gotBody.Messages[0].Role)
	}
	combined := gotBody.Messages[1].Content
	if !strings.Contains(combined, "what did helios do?") {
		t.Error("user message missing from prompt")
	}
	if !strings.Contains(combined, `"f1"`) || !strings.Contains(combined, `"f2"`) {
		t.Errorf("flows JSON missing in prompt: %s", combined)
	}
}

func TestRealChatModel_GenerateAnswer_NoFlows(t *testing.T) {
	// No HTTP call expected — short-circuit.
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP should not be called when flows is empty")
	})
	m := NewRealChatModel(ChatModelConfig{
		Endpoint: ts.URL, Model: "m", APIKey: "sk",
	}, ts.Client())

	ans, err := m.GenerateAnswer(context.Background(), "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ans == "" {
		t.Error("expected fallback answer")
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fenced_no_lang", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"with_prose", `Here you go: {"a":1} cheers`, `{"a":1}`},
		{"nested", `pre {"a":{"b":1}} post`, `{"a":{"b":1}}`},
		{"empty", "", ""},
		{"no_braces", "no json here", ""},
		{"unbalanced", `{"a":1`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractJSON(c.in); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestSelectChatModel_StubWhenDisabled(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "false")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://x")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	got := selectChatModel(nil)
	if _, ok := got.(*stubChatModel); !ok {
		t.Errorf("expected stubChatModel, got %T", got)
	}
}

func TestSelectChatModel_StubWhenMissing(t *testing.T) {
	// Disable entirely — no env vars — should still return stub without panic.
	os.Unsetenv("RABBITHOLE_CHAT_ENABLED")
	os.Unsetenv("RABBITHOLE_CHAT_MODEL_ENDPOINT")
	os.Unsetenv("RABBITHOLE_CHAT_MODEL_NAME")
	os.Unsetenv("RABBITHOLE_CHAT_MODEL_API_KEY")

	got := selectChatModel(nil)
	if _, ok := got.(*stubChatModel); !ok {
		t.Errorf("expected stubChatModel fallback, got %T", got)
	}
}

func TestSelectChatModel_RealWhenConfigured(t *testing.T) {
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"query":"q","limit":5}`}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	t.Setenv("RABBITHOLE_CHAT_ENABLED", "true")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", ts.URL)
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	got := selectChatModel(nil)
	if _, ok := got.(*RealChatModel); !ok {
		t.Fatalf("expected *RealChatModel, got %T", got)
	}
	// Round-trip to verify wiring works end-to-end.
	req, err := got.TranslateQuery(context.Background(), "hello")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if req.Query != "q" {
		t.Errorf("Query=%q", req.Query)
	}
}

func TestRealChatModel_TranslateQuery_LimitClamped(t *testing.T) {
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"query":"x","limit":99999}`}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	m := NewRealChatModel(ChatModelConfig{Endpoint: ts.URL, Model: "m", APIKey: "sk"}, ts.Client())
	got, err := m.TranslateQuery(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Limit != 200 {
		t.Errorf("Limit=%d, want clamp to 200", got.Limit)
	}
}
