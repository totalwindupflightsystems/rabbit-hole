// Package demo generates realistic agent activity for dogfooding.
//
// Rabbit-Hole's own dogfood story: you can't fully appreciate the product
// until you see real traces flowing. The demo package produces a believable
// Hermes-like agent session — file reads, web searches, code patches, test
// runs, LLM calls — with the same shape the classifier would emit, so the
// dashboard, chat, and search are all populated with meaningful data.
package demo

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// Scenario is a seeded agent-work session: what a coding agent does in
// ~30 minutes of work.
type Scenario struct {
	Session types.Session
	Flows   []types.Flow
}

// Generate builds a dogfood session with nFlows flows starting at start.
// Flows are generated with realistic intents, phases, durations, outcomes
// and step metadata so the dashboard waterfall renders meaningful bars.
//
// By default flows are packed at a fixed 15s cadence from start. Pass an
// optional window (spread) to distribute them evenly across
// [start, start+window) instead, so the seeded session spans the whole
// window rather than clustering at its beginning.
func Generate(nFlows int, start time.Time, seed int64, spread ...time.Duration) *Scenario {
	rng := rand.New(rand.NewSource(seed))

	// Session IDs are UUIDv7 to satisfy the OpenAPI contract
	// (specs/openapi.yaml declares Session.id as UUIDv7).
	sessionID := uuid.Must(uuid.NewV7()).String()
	session := types.Session{
		ID:        sessionID,
		AgentPID:  4242,
		AgentName: "hermes",
		StartTime: start,
		Status:    types.SessionStatusCompleted,
		Metadata: types.SessionMetadata{
			CommandLine: "hermes chat -q 'fix rate limiter race'",
			WorkDir:     "/home/kara/rabbit-hole",
			BinaryPath:  "/home/kara/.local/bin/hermes",
			Version:     "2026.08",
			Environment: map[string]string{
				"os":   "linux",
				"arch": "amd64",
				"go":   "1.26.5",
			},
		},
	}

	intents := []struct {
		intent  string
		phase   types.FlowPhase
		desc    func(n int) string
		durMin  time.Duration
		durMax  time.Duration
		success float64
	}{
		{"read_file", types.FlowPhaseObservation,
			func(n int) string {
				return fmt.Sprintf("Read %s.go (%d lines, %dms)", pick(rng, []string{"server", "auth", "storage", "handlers", "config", "types"}), 80+n*20, 1+n%5)
			},
			2 * time.Millisecond, 40 * time.Millisecond, 0.98},
		{"search_web", types.FlowPhaseObservation,
			func(n int) string {
				return fmt.Sprintf("Searched the web for %q (5 results)", pick(rng, []string{"go eBPF attach API", "sqlite FTS5 ranking", "gorilla websocket ping", "prometheus histogram buckets", "cobra completion"}))
			},
			120 * time.Millisecond, 900 * time.Millisecond, 0.92},
		{"llm_api_call", types.FlowPhaseDeliberation,
			func(n int) string {
				return fmt.Sprintf("LLM call %s — %d prompt tokens, %d completion tokens (%.1fs)", pick(rng, []string{"classify_traces", "generate_patch", "summarize_flow", "translate_query"}), 800+n*120, 120+n*40, 0.8+float64(n%8)*0.3)
			},
			800 * time.Millisecond, 4 * time.Second, 0.95},
		{"patch_code", types.FlowPhaseAction,
			func(n int) string {
				return fmt.Sprintf("Patched %s — %d insertions, %d deletions", pick(rng, []string{"middleware.go", "rate.go", "auth.go", "metrics.go", "websocket.go"}), 4+n%18, 1+n%6)
			},
			15 * time.Millisecond, 200 * time.Millisecond, 0.85},
		{"run_tests", types.FlowPhaseVerification,
			func(n int) string {
				return fmt.Sprintf("Ran go test ./... — %d passed, %d failed (%.1fs)", 8+n%5, n%3, 2.0+float64(n%6)*1.3)
			},
			1 * time.Second, 9 * time.Second, 0.75},
		{"git_commit", types.FlowPhaseAction,
			func(n int) string {
				return fmt.Sprintf("Committed %q — %d files changed", pick(rng, []string{"fix: rate limiter race", "feat: add metrics", "docs: update spec", "chore: bump deps", "test: add stress"}), 1+n%6)
			},
			50 * time.Millisecond, 400 * time.Millisecond, 0.97},
		{"write_file", types.FlowPhaseAction,
			func(n int) string {
				return fmt.Sprintf("Wrote %s (%d bytes)", pick(rng, []string{"internal/express/rate_test.go", "internal/storage/dashboard.go", "cmd/rabbit-hole/demo.go", "web/dashboard/index.html"}), 400+n*180)
			},
			8 * time.Millisecond, 90 * time.Millisecond, 0.95},
		{"network_call", types.FlowPhaseObservation,
			func(n int) string {
				return fmt.Sprintf("GET %s — %d bytes, %dms", pick(rng, []string{"https://api.deepseek.com/v1/models", "https://gitlab.readydedis.com/api/v4/projects", "https://api.openrouter.ai/api/v1/models", "https://registry.hub.docker.com/v2/"}), 200+n*150, 40+n*25)
			},
			30 * time.Millisecond, 700 * time.Millisecond, 0.88},
		{"classify_traces", types.FlowPhaseDeliberation,
			func(n int) string {
				return fmt.Sprintf("Classified %d traces into %d flows (%.2fs)", 20+n*8, 1+n%4, 0.3+float64(n%5)*0.25)
			},
			250 * time.Millisecond, 1 * time.Second, 0.98},
		{"memory_write", types.FlowPhaseAction,
			func(n int) string { return fmt.Sprintf("Stored %d memory entries under /projects/rabbit-hole/", 1+n%4) },
			4 * time.Millisecond, 30 * time.Millisecond, 0.99},
	}

	// Default cadence packs flows 15s apart from start. With a spread
	// window, evenly distribute them across [start, start+window).
	gap := 15 * time.Second
	if len(spread) > 0 && spread[0] > 0 && nFlows > 0 {
		gap = spread[0] / time.Duration(nFlows)
	}

	now := start
	var flows []types.Flow
	for i := 0; i < nFlows; i++ {
		t := intents[rng.Intn(len(intents))]
		dur := t.durMin + time.Duration(rng.Int63n(int64(t.durMax-t.durMin)))

		outcome := types.FlowOutcomeSuccess
		if rng.Float64() > t.success {
			if rng.Float64() < 0.5 {
				outcome = types.FlowOutcomeFailure
			} else {
				outcome = types.FlowOutcomeTimeout
			}
		}

		stepCount := 2 + rng.Intn(4)
		steps := make([]map[string]any, 0, stepCount)
		acc := time.Duration(0)
		for s := 0; s < stepCount; s++ {
			sd := time.Duration(rng.Int63n(int64(dur))) / time.Duration(stepCount)
			steps = append(steps, map[string]any{
				"label": pick(rng, []string{"open", "read", "parse", "match", "decide", "write", "verify", "flush"}),
				"dur":   sd.Nanoseconds(),
				"start": acc.Nanoseconds(),
			})
			acc += sd
		}
		meta, _ := json.Marshal(map[string]any{
			"steps":   steps,
			"source":  "demo",
			"pattern": rng.Float64() < 0.7,
		})

		traceIDs := make([]string, 0, 3)
		for t := 0; t < 3; t++ {
			traceIDs = append(traceIDs, fmt.Sprintf("tr-%d-%d", i, t))
		}

		conf := 0.55 + rng.Float64()*0.45
		flows = append(flows, types.Flow{
			ID:          fmt.Sprintf("fl-%s-%06d", sessionID, i+1),
			SessionID:   sessionID,
			TraceIDs:    traceIDs,
			Intent:      t.intent,
			Phase:       t.phase,
			Description: t.desc(i),
			Outcome:     outcome,
			Confidence:  conf,
			StartTime:   now,
			EndTime:     now.Add(dur),
			Duration:    dur,
			Metadata:    meta,
		})
		now = now.Add(gap)
	}

	return &Scenario{Session: session, Flows: flows}
}

func pick(rng *rand.Rand, items []string) string {
	return items[rng.Intn(len(items))]
}
