package express

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestParseTranslateResponse_TimeRange verifies the model's "time_range"
// {start,end} RFC3339 window is parsed into searchReq.TimeRange. DF-002.
func TestParseTranslateResponse_TimeRange(t *testing.T) {
	start := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	end := time.Now().UTC().Format(time.RFC3339)
	raw := fmt.Sprintf(`{"query":"","limit":10,"phases":[],"outcomes":[],"time_range":{"start":%q,"end":%q}}`, start, end)

	req, err := parseTranslateResponse(raw)
	if err != nil {
		t.Fatalf("parseTranslateResponse: %v", err)
	}
	if req.TimeRange.Start.IsZero() || req.TimeRange.End.IsZero() {
		t.Fatalf("TimeRange not parsed: %+v", req.TimeRange)
	}
	if got := req.TimeRange.Start.Format(time.RFC3339); got != start {
		t.Errorf("Start=%s, want %s", got, start)
	}
	if got := req.TimeRange.End.Format(time.RFC3339); got != end {
		t.Errorf("End=%s, want %s", got, end)
	}
}

// TestParseTranslateResponse_TimeRangeOnly proves a pure time-window
// question (query="" + time_range) is accepted — the store filters by
// the window alone when query is empty. DF-002.
func TestParseTranslateResponse_TimeRangeOnly(t *testing.T) {
	start := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	raw := fmt.Sprintf(`{"query":"","limit":50,"time_range":{"start":%q,"end":null}}`, start)

	req, err := parseTranslateResponse(raw)
	if err != nil {
		t.Fatalf("parseTranslateResponse: %v", err)
	}
	if req.Query != "" {
		t.Errorf("Query=%q, want empty", req.Query)
	}
	if req.TimeRange.Start.IsZero() {
		t.Fatal("TimeRange.Start not parsed")
	}
}

// TestParseTranslateResponse_EmptyNoTimeRange keeps the original guard:
// query="" with no time range is still an error.
func TestParseTranslateResponse_EmptyNoTimeRange(t *testing.T) {
	if _, err := parseTranslateResponse(`{"query":"","limit":50}`); err == nil {
		t.Error("expected error for empty query with no time range")
	}
}

// TestParseTranslateResponse_BadTimeRange verifies unparseable window
// timestamps are dropped silently (zero TimeRange) instead of failing
// the whole translation.
func TestParseTranslateResponse_BadTimeRange(t *testing.T) {
	req, err := parseTranslateResponse(`{"query":"x","limit":50,"time_range":{"start":"not-a-time","end":"also-bad"}}`)
	if err != nil {
		t.Fatalf("parseTranslateResponse: %v", err)
	}
	if !req.TimeRange.Start.IsZero() || !req.TimeRange.End.IsZero() {
		t.Errorf("expected zero TimeRange for unparseable timestamps, got %+v", req.TimeRange)
	}
	if req.Query != "x" {
		t.Errorf("Query=%q, want x", req.Query)
	}
}

// TestRealChatModel_TranslateQuery_TimeRange round-trips a time_range
// emission through the real model path: the stub endpoint returns
// query="" + time_range and TranslateQuery must surface both.
func TestRealChatModel_TranslateQuery_TimeRange(t *testing.T) {
	start := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"query":    "",
			"limit":    50,
			"phases":   []string{},
			"outcomes": []string{},
			"time_range": map[string]any{
				"start": start,
				"end":   nil,
			},
		}
		b, _ := json.Marshal(body)
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": string(b)}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	m := NewRealChatModel(ChatModelConfig{Endpoint: ts.URL, Model: "m", APIKey: "sk"}, ts.Client())
	got, err := m.TranslateQuery(context.Background(), "What happened in the last hour?")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if got.Query != "" {
		t.Errorf("Query=%q, want empty (time-window question)", got.Query)
	}
	if got.TimeRange.Start.IsZero() {
		t.Fatal("TimeRange.Start not surfaced by TranslateQuery")
	}
	if got.TimeRange.Start.Format(time.RFC3339) != start {
		t.Errorf("Start=%s, want %s", got.TimeRange.Start.Format(time.RFC3339), start)
	}
	if !got.TimeRange.End.IsZero() {
		t.Errorf("End=%v, want zero (null)", got.TimeRange.End)
	}
}

// TestStubChatModel_TranslateQuery_TimeWindow verifies the stub detects
// time phrases and returns an empty keyword query + a concrete UTC window,
// mirroring the real model's contract for time-based questions. DF-008.
func TestStubChatModel_TranslateQuery_TimeWindow(t *testing.T) {
	m := &stubChatModel{}
	cases := []struct {
		msg     string
		wantDur time.Duration // expected window duration (0 = today, ends at now)
	}{
		{"What did the agent do in the last hour?", time.Hour},
		{"what happened in the past 2 hours?", 2 * time.Hour},
		{"show me the activity today", 0},
	}
	for _, c := range cases {
		t.Run(c.msg, func(t *testing.T) {
			before := time.Now().UTC()
			req, err := m.TranslateQuery(context.Background(), c.msg)
			after := time.Now().UTC()
			if err != nil {
				t.Fatalf("TranslateQuery: %v", err)
			}
			if req.Query != "" {
				t.Errorf("Query=%q, want empty (time-only question)", req.Query)
			}
			if req.Limit != 50 {
				t.Errorf("Limit=%d, want 50", req.Limit)
			}
			if req.TimeRange.Start.IsZero() || req.TimeRange.End.IsZero() {
				t.Fatalf("TimeRange not set: %+v", req.TimeRange)
			}
			if req.TimeRange.End.Before(req.TimeRange.Start) {
				t.Errorf("window inverted: start=%v end=%v", req.TimeRange.Start, req.TimeRange.End)
			}
			if c.wantDur > 0 {
				if d := req.TimeRange.End.Sub(req.TimeRange.Start); d < c.wantDur-5*time.Minute || d > c.wantDur+5*time.Minute {
					t.Errorf("window duration=%v, want ~%v", d, c.wantDur)
				}
				if req.TimeRange.Start.Before(before.Add(-c.wantDur-10*time.Minute)) || req.TimeRange.Start.After(after) {
					t.Errorf("Start=%v, want ≈ now-%v", req.TimeRange.Start, c.wantDur)
				}
			} else {
				// "today": window starts at UTC midnight and ends at now.
				if h, mnt, s := req.TimeRange.Start.UTC().Clock(); h != 0 || mnt != 0 || s != 0 {
					t.Errorf("Start=%v, want UTC midnight today", req.TimeRange.Start)
				}
			}
			if req.TimeRange.End.Before(before.Add(-1*time.Minute)) || req.TimeRange.End.After(after.Add(1*time.Minute)) {
				t.Errorf("End=%v, want ≈ now", req.TimeRange.End)
			}
		})
	}
}

// TestStubChatModel_TranslateQuery_KeywordPlusWindow verifies a question
// with BOTH a time phrase and a concrete keyword keeps the keyword as the
// query and adds the window. DF-008.
func TestStubChatModel_TranslateQuery_KeywordPlusWindow(t *testing.T) {
	m := &stubChatModel{}
	req, err := m.TranslateQuery(context.Background(), "sql errors in the last hour")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if req.Query != "sql errors" {
		t.Errorf("Query=%q, want %q", req.Query, "sql errors")
	}
	if req.TimeRange.Start.IsZero() || req.TimeRange.End.IsZero() {
		t.Fatalf("TimeRange not set: %+v", req.TimeRange)
	}
	if d := req.TimeRange.End.Sub(req.TimeRange.Start); d < 55*time.Minute || d > 65*time.Minute {
		t.Errorf("window duration=%v, want ~1h", d)
	}

	req, err = m.TranslateQuery(context.Background(), "show me sql errors from yesterday")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if req.Query != "sql errors" {
		t.Errorf("Query=%q, want %q", req.Query, "sql errors")
	}
	if req.TimeRange.Start.IsZero() {
		t.Fatal("TimeRange.Start not set for yesterday question")
	}
}

// TestStubChatModel_TranslateQuery_PlainKeyword is the no-regression case:
// a plain keyword query without time words behaves exactly as before —
// the whole message becomes the query and no window is set. DF-008.
func TestStubChatModel_TranslateQuery_PlainKeyword(t *testing.T) {
	m := &stubChatModel{}
	for _, msg := range []string{"sql error", "what failed in auth.go?"} {
		req, err := m.TranslateQuery(context.Background(), msg)
		if err != nil {
			t.Fatalf("TranslateQuery(%q): %v", msg, err)
		}
		if req.Query != msg {
			t.Errorf("Query=%q, want %q (unchanged)", req.Query, msg)
		}
		if !req.TimeRange.Start.IsZero() || !req.TimeRange.End.IsZero() {
			t.Errorf("TimeRange=%+v, want zero for non-time query", req.TimeRange)
		}
		if req.Limit != 50 {
			t.Errorf("Limit=%d, want 50", req.Limit)
		}
	}
}
