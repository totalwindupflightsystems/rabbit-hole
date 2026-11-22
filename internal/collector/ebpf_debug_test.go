package collector

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/cilium/ebpf"
)

// TestWithBPFDebug_Option pins the plumbing between the option and the
// collector field that gates verifier-log output (RABBITHOLE_BPF_DEBUG).
func TestWithBPFDebug_Option(t *testing.T) {
	var o collectorOptions
	WithBPFDebug()(&o)
	if !o.bpfDebug {
		t.Fatal("WithBPFDebug did not set bpfDebug")
	}

	c, err := NewEBPFCollector(1000, 10, nil, WithBPFDebug())
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	defer func() { _ = c.Close() }()
	if !c.bpfDebug {
		t.Error("collector did not retain the bpfDebug option")
	}
}

// TestLogVerifierError prints the kernel verifier log carried by a failed load.
func TestLogVerifierError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	logVerifierError(logger, &ebpf.VerifierError{Log: []string{"processed 3 insns", "invalid mem access 'inv'"}})

	out := buf.String()
	if !strings.Contains(out, "verifier log") {
		t.Fatalf("expected a verifier-log line, got %q", out)
	}
	if !strings.Contains(out, "invalid mem access") {
		t.Fatalf("expected the kernel log body, got %q", out)
	}
}

// TestLogVerifierError_NonVerifierError stays silent for ordinary errors so a
// non-verifier load failure is not buried under an empty log line.
func TestLogVerifierError_NonVerifierError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	logVerifierError(logger, errPlainForTest)

	if buf.Len() != 0 {
		t.Fatalf("expected no output for a non-verifier error, got %q", buf.String())
	}
}

type plainErr struct{}

func (plainErr) Error() string { return "plain failure" }

var errPlainForTest = plainErr{}
