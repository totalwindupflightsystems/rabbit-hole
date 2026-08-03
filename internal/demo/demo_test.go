package demo

import (
	"testing"
	"time"
)

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
