package demo

import (
	"regexp"
	"testing"
	"time"
)

// uuidV7RE matches the canonical UUIDv7 shape: version nibble 7 and a
// RFC 4122 variant nibble (8/9/a/b) in the third group.
var uuidV7RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestGenerate_Shape(t *testing.T) {
	sc := Generate(60, time.Now().Add(-5*time.Hour), 1234)

	if len(sc.Flows) != 60 {
		t.Fatalf("flows = %d, want 60", len(sc.Flows))
	}
	if sc.Session.AgentName != "hermes" {
		t.Errorf("AgentName = %q, want hermes", sc.Session.AgentName)
	}
	if sc.Session.AgentPID <= 0 {
		t.Errorf("AgentPID = %d, want > 0", sc.Session.AgentPID)
	}

	for _, f := range sc.Flows {
		if f.ID == "" {
			t.Error("flow with empty ID")
		}
		if f.SessionID != sc.Session.ID {
			t.Errorf("flow %s session %q != %q", f.ID, f.SessionID, sc.Session.ID)
		}
		if f.Intent == "" {
			t.Errorf("flow %s empty intent", f.ID)
		}
		if f.Duration <= 0 {
			t.Errorf("flow %s duration = %v", f.ID, f.Duration)
		}
		if f.Confidence < 0 || f.Confidence > 1 {
			t.Errorf("flow %s confidence = %f out of range", f.ID, f.Confidence)
		}
		if len(f.TraceIDs) == 0 {
			t.Errorf("flow %s no trace IDs", f.ID)
		}
		if len(f.Metadata) == 0 {
			t.Errorf("flow %s no metadata", f.ID)
		}
	}
}

func TestGenerate_SessionIDIsUUIDv7(t *testing.T) {
	sc := Generate(10, time.Now().Add(-1*time.Hour), 42)

	if !uuidV7RE.MatchString(sc.Session.ID) {
		t.Fatalf("session ID %q is not UUIDv7", sc.Session.ID)
	}
	// Every flow must reference the UUIDv7 session id.
	for _, f := range sc.Flows {
		if f.SessionID != sc.Session.ID {
			t.Errorf("flow %s session %q != %q", f.ID, f.SessionID, sc.Session.ID)
		}
	}
}

func TestGenerate_UniqueIDsAcrossRuns(t *testing.T) {
	a := Generate(10, time.Now(), 111)
	b := Generate(10, time.Now(), 222)

	if a.Session.ID == b.Session.ID {
		t.Errorf("session IDs collide: %s", a.Session.ID)
	}
	if a.Flows[0].ID == b.Flows[0].ID {
		t.Errorf("flow IDs collide: %s", a.Flows[0].ID)
	}
}

func TestGenerate_OutcomesAreValid(t *testing.T) {
	sc := Generate(200, time.Now().Add(-2*time.Hour), 99)
	seen := map[string]int{}
	for _, f := range sc.Flows {
		seen[string(f.Outcome)]++
	}
	valid := map[string]bool{"success": true, "failure": true, "timeout": true, "unknown": true}
	for o := range seen {
		if !valid[o] {
			t.Errorf("invalid outcome %q", o)
		}
	}
	// A 200-flow run should exercise more than one outcome.
	if len(seen) < 2 {
		t.Errorf("only %d outcomes seen in 200 flows, want >= 2", len(seen))
	}
}

func TestGenerate_PhasesCoverAll(t *testing.T) {
	sc := Generate(400, time.Now().Add(-3*time.Hour), 5)
	phases := map[string]bool{}
	for _, f := range sc.Flows {
		phases[string(f.Phase)] = true
	}
	for _, want := range []string{"observation", "deliberation", "action", "verification"} {
		if !phases[want] {
			t.Errorf("phase %q never generated", want)
		}
	}
}

func TestGenerate_SpreadDistributesOverWindow(t *testing.T) {
	const (
		window = 3 * time.Hour
		n      = 48
	)
	start := time.Now().Add(-window)
	sc := Generate(n, start, 7, window)

	if len(sc.Flows) != n {
		t.Fatalf("flows = %d, want %d", len(sc.Flows), n)
	}
	if !sc.Flows[0].StartTime.Equal(start) {
		t.Errorf("first flow starts %v, want %v", sc.Flows[0].StartTime, start)
	}
	// With spread the cadence is window/nFlows, so the flows span the
	// whole window instead of clustering at its start.
	gap := window / time.Duration(n)
	for i := 1; i < len(sc.Flows); i++ {
		if got := sc.Flows[i].StartTime.Sub(sc.Flows[i-1].StartTime); got != gap {
			t.Fatalf("flow %d gap = %v, want %v", i, got, gap)
		}
	}
	if got := sc.Flows[n-1].StartTime.Sub(start); got != window-gap {
		t.Errorf("last flow starts %v after start, want %v (near end of window)", got, window-gap)
	}
}

func TestGenerate_DefaultCadenceWithoutSpread(t *testing.T) {
	// No spread window → the fixed 15s cadence must be preserved so
	// existing callers (serve.go, dashboard tests) keep their shape.
	start := time.Now().Add(-time.Hour)
	sc := Generate(10, start, 3)
	for i := 1; i < len(sc.Flows); i++ {
		if got := sc.Flows[i].StartTime.Sub(sc.Flows[i-1].StartTime); got != 15*time.Second {
			t.Fatalf("flow %d gap = %v, want 15s", i, got)
		}
	}
}
