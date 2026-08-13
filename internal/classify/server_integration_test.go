package classify

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	classifierpb "gitlab.readydedis.com/rabbit-hole/rabbit-hole/api/proto/classifier/v1"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// startPatternServer brings up the real shipped classify-server
// implementation (PatternServer + NewGRPCServer — the same non-test code the
// rabbit-hole classify-server subcommand runs) on an ephemeral localhost
// port. Returns the address and a cleanup function.
func startPatternServer(t *testing.T, token string) (string, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}

	ps := NewPatternServer()
	grpcServer := NewGRPCServer(ps, token)

	done := make(chan struct{})
	go func() {
		_ = grpcServer.Serve(lis)
		close(done)
	}()

	addr := lis.Addr().String()
	cleanup := func() {
		grpcServer.Stop()
		<-done
	}
	return addr, cleanup
}

// TestRemoteBackend_ReferenceServer exercises the remote gRPC path end-to-end
// against the shipped reference server. Unlike TestRemoteBackend_Integration
// (which uses canned test-only scaffolding and skips under -short), this runs
// by default: the server under test is the production PatternServer, so the
// default test run proves the remote backend works against a real deployment
// target (DF-023).
func TestRemoteBackend_ReferenceServer(t *testing.T) {
	addr, cleanup := startPatternServer(t, "")
	defer cleanup()

	backend, err := NewRemoteBackend(addr, "")
	if err != nil {
		t.Fatalf("NewRemoteBackend failed: %v", err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("backend Close failed: %v", err)
		}
	}()

	// Health — real Ping round-trip against the reference server.
	if err := backend.Health(context.Background()); err != nil {
		t.Fatalf("Health failed: %v", err)
	}

	// Info — metadata round-trip.
	info, err := backend.Info(context.Background())
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if info.Name != "rabbit-hole-pattern-classifier" {
		t.Errorf("expected name rabbit-hole-pattern-classifier, got %s", info.Name)
	}
	// The client stamps Kind itself (this IS the remote backend from serve's
	// perspective); the server's own kind ("pattern") is asserted directly
	// in TestPatternServer_Info.
	if info.Kind != "remote" {
		t.Errorf("expected kind remote (client-stamped), got %s", info.Kind)
	}
	if !info.Ready {
		t.Error("expected Ready=true")
	}
	if info.Endpoint != addr {
		t.Errorf("expected endpoint %s, got %s", addr, info.Endpoint)
	}

	// Classify — a file_read group must classify via the same pattern
	// catalog the local backend uses.
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	groups := [][]types.Trace{
		{
			{PID: 1, Syscall: "openat", Args: []string{"config.yaml"}, ReturnValue: 3, Timestamp: now},
			{PID: 1, Syscall: "read", Args: []string{"config.yaml"}, ReturnValue: 1024, Timestamp: now.Add(2 * time.Millisecond)},
			{PID: 1, Syscall: "close", Args: []string{"3"}, ReturnValue: 0, Timestamp: now.Add(4 * time.Millisecond)},
		},
	}

	flows, err := backend.Classify(context.Background(), groups)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	flow := flows[0]
	if flow.Intent != "read_file" {
		t.Errorf("expected intent read_file, got %s", flow.Intent)
	}
	if flow.Phase != types.FlowPhaseObservation {
		t.Errorf("expected phase observation, got %s", flow.Phase)
	}
	if flow.Outcome != types.FlowOutcomeSuccess {
		t.Errorf("expected outcome success, got %s", flow.Outcome)
	}
	if flow.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", flow.Confidence)
	}
	if !flow.StartTime.Equal(now) {
		t.Errorf("expected start %v, got %v", now, flow.StartTime)
	}
	if !flow.EndTime.Equal(now.Add(4 * time.Millisecond)) {
		t.Errorf("expected end %v, got %v", now.Add(4*time.Millisecond), flow.EndTime)
	}
	if flow.Duration != 4*time.Millisecond {
		t.Errorf("expected duration 4ms, got %v", flow.Duration)
	}
	if flow.ID == "" {
		t.Error("expected a non-empty flow id")
	}

	// Unmatched group → the reference server falls back to an unknown flow
	// (mirroring the local pipeline), so the client always receives one
	// flow per group.
	unmatched := [][]types.Trace{
		{{PID: 9, Syscall: "mprotect", Args: []string{"0x7f0000000000"}, ReturnValue: 0, Timestamp: now}},
	}
	flows, err = backend.Classify(context.Background(), unmatched)
	if err != nil {
		t.Fatalf("Classify (unmatched) failed: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 unknown flow, got %d", len(flows))
	}
	if flows[0].Intent != "unknown" {
		t.Errorf("expected intent unknown, got %s", flows[0].Intent)
	}
	if flows[0].Outcome != types.FlowOutcomeUnknown {
		t.Errorf("expected outcome unknown, got %s", flows[0].Outcome)
	}
	if flows[0].Confidence != 0 {
		t.Errorf("expected confidence 0, got %f", flows[0].Confidence)
	}

	// Empty batch → empty response, no error.
	flows, err = backend.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("Classify (empty) failed: %v", err)
	}
	if len(flows) != 0 {
		t.Errorf("expected 0 flows for empty batch, got %d", len(flows))
	}
}

// TestPatternServer_TokenAuth verifies the reference server's bearer-token
// auth end-to-end: RPCs without the token are rejected with
// Unauthenticated, and the RemoteBackend's token slot (serve --remote
// <addr>@<token>) satisfies it.
func TestPatternServer_TokenAuth(t *testing.T) {
	const secret = "s3cret-token"

	addr, cleanup := startPatternServer(t, secret)
	defer cleanup()

	// Without a token every RPC must be rejected.
	anon, err := NewRemoteBackend(addr, "")
	if err != nil {
		t.Fatalf("NewRemoteBackend (no token) failed: %v", err)
	}
	defer anon.Close()

	if err := anon.Health(context.Background()); err == nil {
		t.Fatal("expected Health to fail without token")
	} else if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated without token, got %v", err)
	}

	if _, err := anon.Classify(context.Background(), [][]types.Trace{
		{{PID: 1, Syscall: "openat", Args: []string{"x"}, ReturnValue: 3, Timestamp: time.Now()}},
	}); err == nil {
		t.Fatal("expected Classify to fail without token")
	} else if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated without token, got %v", err)
	}

	// With the matching token every RPC succeeds.
	authed, err := NewRemoteBackend(addr, secret)
	if err != nil {
		t.Fatalf("NewRemoteBackend (with token) failed: %v", err)
	}
	defer authed.Close()

	if err := authed.Health(context.Background()); err != nil {
		t.Fatalf("Health with token failed: %v", err)
	}
	if _, err := authed.Info(context.Background()); err != nil {
		t.Fatalf("Info with token failed: %v", err)
	}

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	flows, err := authed.Classify(context.Background(), [][]types.Trace{
		{
			{PID: 1, Syscall: "openat", Args: []string{"config.yaml"}, ReturnValue: 3, Timestamp: now},
			{PID: 1, Syscall: "read", Args: []string{"config.yaml"}, ReturnValue: 1024, Timestamp: now.Add(time.Millisecond)},
			{PID: 1, Syscall: "close", Args: []string{"3"}, ReturnValue: 0, Timestamp: now.Add(2 * time.Millisecond)},
		},
	})
	if err != nil {
		t.Fatalf("Classify with token failed: %v", err)
	}
	if len(flows) != 1 || flows[0].Intent != "read_file" {
		t.Fatalf("expected 1 read_file flow, got %d flows: %+v", len(flows), flows)
	}
}

// TestPatternServer_Info verifies the server's own Info response (the
// client-side RemoteBackend re-stamps Kind as "remote", so the server's
// "pattern" kind is only observable here or on the wire).
func TestPatternServer_Info(t *testing.T) {
	ps := NewPatternServer()
	resp, err := ps.Info(context.Background(), &classifierpb.InfoRequest{})
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if resp.Name != "rabbit-hole-pattern-classifier" {
		t.Errorf("expected name rabbit-hole-pattern-classifier, got %s", resp.Name)
	}
	if resp.Kind != "pattern" {
		t.Errorf("expected kind pattern, got %s", resp.Kind)
	}
	if !resp.Ready {
		t.Error("expected Ready=true")
	}
	if resp.DeviceType != "cpu" {
		t.Errorf("expected device_type cpu, got %s", resp.DeviceType)
	}
}

// TestPatternServer_Classify covers the server's classification logic
// directly (no network): empty requests, per-group handling, and the
// unknown-flow fallback contract.
func TestPatternServer_Classify(t *testing.T) {
	ps := NewPatternServer()

	// Empty request → empty response.
	resp, err := ps.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("Classify(nil) failed: %v", err)
	}
	if len(resp.Flows) != 0 {
		t.Fatalf("expected 0 flows for nil request, got %d", len(resp.Flows))
	}

	// Mixed batch: matched group → read_file, unmatched group → unknown.
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	resp, err = ps.Classify(context.Background(), &classifierpb.ClassifyRequest{
		Groups: []*classifierpb.TraceGroup{
			{
				Traces: []*classifierpb.Trace{
					{Pid: 1, Syscall: "openat", Args: "config.yaml", ReturnValue: 3, TimestampNs: now.UnixNano()},
					{Pid: 1, Syscall: "read", Args: "config.yaml", ReturnValue: 1024, TimestampNs: now.Add(2 * time.Millisecond).UnixNano()},
					{Pid: 1, Syscall: "close", Args: "3", ReturnValue: 0, TimestampNs: now.Add(4 * time.Millisecond).UnixNano()},
				},
			},
			{
				Traces: []*classifierpb.Trace{
					{Pid: 9, Syscall: "mprotect", Args: "0x7f0000000000", ReturnValue: 0, TimestampNs: now.UnixNano()},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if len(resp.Flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(resp.Flows))
	}
	if resp.Flows[0].Intent != "read_file" || resp.Flows[0].Phase != "observation" {
		t.Errorf("flow[0]: expected read_file/observation, got %s/%s", resp.Flows[0].Intent, resp.Flows[0].Phase)
	}
	if resp.Flows[1].Intent != "unknown" || resp.Flows[1].Outcome != "unknown" {
		t.Errorf("flow[1]: expected unknown/unknown, got %s/%s", resp.Flows[1].Intent, resp.Flows[1].Outcome)
	}
	if resp.Flows[1].Confidence != 0 {
		t.Errorf("flow[1]: expected confidence 0, got %f", resp.Flows[1].Confidence)
	}

	// Group with no traces → skipped, no flow.
	resp, err = ps.Classify(context.Background(), &classifierpb.ClassifyRequest{
		Groups: []*classifierpb.TraceGroup{{}, {}},
	})
	if err != nil {
		t.Fatalf("Classify (empty groups) failed: %v", err)
	}
	if len(resp.Flows) != 0 {
		t.Fatalf("expected 0 flows for empty groups, got %d", len(resp.Flows))
	}
}
