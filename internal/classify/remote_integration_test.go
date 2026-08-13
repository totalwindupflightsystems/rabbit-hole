package classify

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	classifierpb "gitlab.readydedis.com/rabbit-hole/rabbit-hole/api/proto/classifier/v1"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// testClassifierServer implements classifierpb.ClassifierServer for the
// integration test. It returns canned flows and records the requests it
// receives so the test can assert on the wire-level behaviour.
type testClassifierServer struct {
	classifierpb.UnimplementedClassifierServer

	classifyCalls int
	pingCalls     int
	infoCalls     int

	// lastClassifyReq records the most recent Classify request for assertions.
	lastClassifyReq *classifierpb.ClassifyRequest
}

func (s *testClassifierServer) Classify(_ context.Context, req *classifierpb.ClassifyRequest) (*classifierpb.ClassifyResponse, error) {
	s.classifyCalls++
	s.lastClassifyReq = req

	if len(req.Groups) == 0 {
		return nil, errors.New("no groups provided")
	}

	flows := make([]*classifierpb.Flow, 0, len(req.Groups))
	for i, group := range req.Groups {
		start := int64(0)
		end := int64(0)
		if len(group.Traces) > 0 {
			start = group.Traces[0].TimestampNs
			end = group.Traces[len(group.Traces)-1].TimestampNs
		}
		flows = append(flows, &classifierpb.Flow{
			Id:          "flow-" + itoa(i),
			Intent:      "read_file",
			Phase:       "action",
			Description: "Read file via openat+read",
			Outcome:     "success",
			Confidence:  0.95,
			TraceIds:    []string{"trace-a", "trace-b"},
			StartTime:   start,
			EndTime:     end,
			DurationNs:  end - start,
		})
	}

	return &classifierpb.ClassifyResponse{Flows: flows}, nil
}

func (s *testClassifierServer) Ping(_ context.Context, _ *classifierpb.PingRequest) (*classifierpb.PingResponse, error) {
	s.pingCalls++
	return &classifierpb.PingResponse{}, nil
}

func (s *testClassifierServer) Info(_ context.Context, _ *classifierpb.InfoRequest) (*classifierpb.InfoResponse, error) {
	s.infoCalls++
	return &classifierpb.InfoResponse{
		Name:       "test-classifier",
		Version:    "1.0.0",
		Kind:       "remote",
		Ready:      true,
		MemoryMb:   512,
		DeviceType: "cpu",
	}, nil
}

// itoa is a minimal int-to-string helper to keep the test free of fmt import
// noise; the indices are small non-negative integers.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	buf := []byte{}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// startTestServer brings up a real gRPC ClassifierServer on an ephemeral
// localhost port and returns the server handle, its address, and a cleanup
// function. The caller must invoke cleanup when done.
func startTestServer(t *testing.T) (*grpc.Server, string, *testClassifierServer, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}

	srv := &testClassifierServer{}
	grpcServer := grpc.NewServer()
	classifierpb.RegisterClassifierServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	addr := lis.Addr().String()
	cleanup := func() {
		grpcServer.Stop()
		_ = lis.Close()
	}

	return grpcServer, addr, srv, cleanup
}

// TestRemoteBackend_Integration exercises RemoteBackend end-to-end against
// a real gRPC server. Skipped under -short.
func TestRemoteBackend_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	_, addr, srv, cleanup := startTestServer(t)
	defer cleanup()

	// Dial the real server.
	backend, err := NewRemoteBackend(addr, "")
	if err != nil {
		t.Fatalf("NewRemoteBackend failed: %v", err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("backend Close failed: %v", err)
		}
	}()

	// Health — real Ping round-trip.
	if err := backend.Health(context.Background()); err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if srv.pingCalls != 1 {
		t.Errorf("expected 1 ping call on server, got %d", srv.pingCalls)
	}

	// Info — real metadata round-trip.
	info, err := backend.Info(context.Background())
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if info.Name != "test-classifier" {
		t.Errorf("expected name test-classifier, got %s", info.Name)
	}
	if info.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", info.Version)
	}
	if info.MemoryMB != 512 {
		t.Errorf("expected memory 512, got %d", info.MemoryMB)
	}
	if info.DeviceType != "cpu" {
		t.Errorf("expected device cpu, got %s", info.DeviceType)
	}
	if info.Kind != "remote" {
		t.Errorf("expected kind remote, got %s", info.Kind)
	}
	if !info.Ready {
		t.Error("expected Ready=true")
	}
	if info.Endpoint != addr {
		t.Errorf("expected endpoint %s, got %s", addr, info.Endpoint)
	}
	if srv.infoCalls != 1 {
		t.Errorf("expected 1 info call on server, got %d", srv.infoCalls)
	}

	// Classify — real trace groups → flows round-trip.
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 7, 20, 10, 0, 0, int(500*time.Millisecond), time.UTC)
	t2 := time.Date(2026, 7, 20, 10, 0, 1, 0, time.UTC)

	groups := [][]types.Trace{
		{
			{PID: 1, Syscall: "openat", Args: []string{"config.yaml"}, ReturnValue: 3, Timestamp: t0},
			{PID: 1, Syscall: "read", Args: []string{"config.yaml"}, ReturnValue: 1024, Timestamp: t1},
			{PID: 1, Syscall: "close", Args: []string{"3"}, ReturnValue: 0, Timestamp: t2},
		},
		{
			{PID: 2, Syscall: "openat", Args: []string{"README.md"}, ReturnValue: 4, Timestamp: t0},
			{PID: 2, Syscall: "read", Args: []string{"README.md"}, ReturnValue: 512, Timestamp: t2},
		},
	}

	flows, err := backend.Classify(context.Background(), groups)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if len(flows) != 2 {
		t.Fatalf("expected 2 flows (one per group), got %d", len(flows))
	}

	if srv.classifyCalls != 1 {
		t.Errorf("expected 1 classify call on server, got %d", srv.classifyCalls)
	}
	if srv.lastClassifyReq == nil {
		t.Fatal("expected lastClassifyReq to be recorded on server")
	}
	if len(srv.lastClassifyReq.Groups) != 2 {
		t.Errorf("expected 2 groups on server, got %d", len(srv.lastClassifyReq.Groups))
	}

	// Verify each flow was correctly unmarshalled from the wire response.
	flow := flows[0]
	if flow.ID != "flow-0" {
		t.Errorf("flow[0]: expected id flow-0, got %s", flow.ID)
	}
	if flow.Intent != "read_file" {
		t.Errorf("flow[0]: expected intent read_file, got %s", flow.Intent)
	}
	if flow.Phase != types.FlowPhaseAction {
		t.Errorf("flow[0]: expected phase action, got %s", flow.Phase)
	}
	if flow.Outcome != types.FlowOutcomeSuccess {
		t.Errorf("flow[0]: expected outcome success, got %s", flow.Outcome)
	}
	if flow.Confidence != 0.95 {
		t.Errorf("flow[0]: expected confidence 0.95, got %f", flow.Confidence)
	}
	if len(flow.TraceIDs) != 2 || flow.TraceIDs[0] != "trace-a" {
		t.Errorf("flow[0]: unexpected trace ids: %v", flow.TraceIDs)
	}
	if !flow.StartTime.Equal(t0) {
		t.Errorf("flow[0]: expected start %v, got %v", t0, flow.StartTime)
	}
	if !flow.EndTime.Equal(t2) {
		t.Errorf("flow[0]: expected end %v, got %v", t2, flow.EndTime)
	}
	if flow.Duration != t2.Sub(t0) {
		t.Errorf("flow[0]: expected duration %v, got %v", t2.Sub(t0), flow.Duration)
	}

	// Second flow corresponds to the second trace group.
	flow2 := flows[1]
	if flow2.ID != "flow-1" {
		t.Errorf("flow[1]: expected id flow-1, got %s", flow2.ID)
	}

	// Health latency should have been recorded from the Ping above.
	if info.Latency <= 0 {
		t.Error("expected positive latency recorded from health ping")
	}
}
