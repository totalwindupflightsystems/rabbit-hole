package classify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	classifierpb "gitlab.readydedis.com/rabbit-hole/rabbit-hole/api/proto/classifier/v1"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// mockClassifierClient implements classifierpb.ClassifierClient for testing.
type mockClassifierClient struct {
	pingFn     func(context.Context, *classifierpb.PingRequest, ...grpc.CallOption) (*classifierpb.PingResponse, error)
	classifyFn func(context.Context, *classifierpb.ClassifyRequest, ...grpc.CallOption) (*classifierpb.ClassifyResponse, error)
	infoFn     func(context.Context, *classifierpb.InfoRequest, ...grpc.CallOption) (*classifierpb.InfoResponse, error)
}

func (m *mockClassifierClient) Ping(ctx context.Context, in *classifierpb.PingRequest, opts ...grpc.CallOption) (*classifierpb.PingResponse, error) {
	return m.pingFn(ctx, in, opts...)
}

func (m *mockClassifierClient) Classify(ctx context.Context, in *classifierpb.ClassifyRequest, opts ...grpc.CallOption) (*classifierpb.ClassifyResponse, error) {
	return m.classifyFn(ctx, in, opts...)
}

func (m *mockClassifierClient) Info(ctx context.Context, in *classifierpb.InfoRequest, opts ...grpc.CallOption) (*classifierpb.InfoResponse, error) {
	return m.infoFn(ctx, in, opts...)
}

func TestNewRemoteBackend(t *testing.T) {
	client := &mockClassifierClient{
		pingFn: func(_ context.Context, _ *classifierpb.PingRequest, _ ...grpc.CallOption) (*classifierpb.PingResponse, error) {
			return &classifierpb.PingResponse{}, nil
		},
	}
	b := &RemoteBackend{
		endpoint: "test.example.com:50051",
		client:   client,
	}
	if b.client == nil {
		t.Fatal("expected client to be set")
	}
}

func TestRemoteBackend_Health(t *testing.T) {
	var pingDelay time.Duration
	client := &mockClassifierClient{
		pingFn: func(_ context.Context, _ *classifierpb.PingRequest, _ ...grpc.CallOption) (*classifierpb.PingResponse, error) {
			time.Sleep(pingDelay)
			return &classifierpb.PingResponse{}, nil
		},
	}

	b := &RemoteBackend{
		endpoint: "test.example.com:50051",
		client:   client,
	}

	pingDelay = 5 * time.Millisecond
	if err := b.Health(context.Background()); err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if b.lastPing == 0 {
		t.Fatal("expected lastPing to be set after Health")
	}
	if b.lastPing < 5*time.Millisecond {
		t.Fatalf("expected lastPing >= 5ms, got %v", b.lastPing)
	}
}

func TestRemoteBackend_Health_Error(t *testing.T) {
	client := &mockClassifierClient{
		pingFn: func(_ context.Context, _ *classifierpb.PingRequest, _ ...grpc.CallOption) (*classifierpb.PingResponse, error) {
			return nil, errors.New("connection refused")
		},
	}

	b := &RemoteBackend{
		endpoint: "bad.example.com:50051",
		client:   client,
	}

	if err := b.Health(context.Background()); err == nil {
		t.Fatal("expected Health to fail with connection error")
	}
}

func TestRemoteBackend_Info(t *testing.T) {
	client := &mockClassifierClient{
		infoFn: func(_ context.Context, _ *classifierpb.InfoRequest, _ ...grpc.CallOption) (*classifierpb.InfoResponse, error) {
			return &classifierpb.InfoResponse{
				Name:       "central-gemma",
				Version:    "1.0.0",
				Kind:       "remote",
				Ready:      true,
				MemoryMb:   2048,
				DeviceType: "cuda",
			}, nil
		},
	}

	b := &RemoteBackend{
		endpoint: "test.example.com:50051",
		client:   client,
	}

	modelInfo, err := b.Info(context.Background())
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if modelInfo.Name != "central-gemma" {
		t.Errorf("expected name central-gemma, got %s", modelInfo.Name)
	}
	if modelInfo.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", modelInfo.Version)
	}
	if modelInfo.MemoryMB != 2048 {
		t.Errorf("expected memory 2048, got %d", modelInfo.MemoryMB)
	}
	if modelInfo.DeviceType != "cuda" {
		t.Errorf("expected device cuda, got %s", modelInfo.DeviceType)
	}
	if modelInfo.Endpoint != "test.example.com:50051" {
		t.Errorf("expected endpoint test.example.com:50051, got %s", modelInfo.Endpoint)
	}
}

func TestRemoteBackend_Classify(t *testing.T) {
	client := &mockClassifierClient{
		classifyFn: func(_ context.Context, req *classifierpb.ClassifyRequest, _ ...grpc.CallOption) (*classifierpb.ClassifyResponse, error) {
			if len(req.Groups) != 1 {
				return nil, errors.New("expected 1 group")
			}
			group := req.Groups[0]
			if len(group.Traces) != 2 {
				return nil, errors.New("expected 2 traces")
			}
			if group.Traces[0].Args != "auth.go" {
				return nil, errors.New("expected args auth.go")
			}
			if !strings.Contains(group.Traces[1].Syscall, "read") {
				return nil, errors.New("expected read syscall")
			}
			return &classifierpb.ClassifyResponse{
				Flows: []*classifierpb.Flow{
					{
						Id:          "flow-001",
						Intent:      "read_file",
						Phase:       "action",
						Description: "Read auth.go",
						Outcome:     "success",
						Confidence:  0.95,
						TraceIds:    []string{"trace-1", "trace-2"},
						StartTime:   time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC).UnixNano(),
						EndTime:     time.Date(2026, 7, 16, 0, 0, 1, 0, time.UTC).UnixNano(),
						DurationNs:  int64(time.Second),
					},
				},
			}, nil
		},
	}

	b := &RemoteBackend{
		endpoint: "test.example.com:50051",
		client:   client,
	}

	groups := [][]types.Trace{
		{
			{PID: 42, Syscall: "openat", Args: []string{"auth.go"}, ReturnValue: 3, Timestamp: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)},
			{PID: 42, Syscall: "read", Args: []string{"auth.go"}, ReturnValue: 247, Timestamp: time.Date(2026, 7, 16, 0, 0, 1, 0, time.UTC)},
		},
	}

	flows, err := b.Classify(context.Background(), groups)
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	flow := flows[0]
	if flow.ID != "flow-001" {
		t.Errorf("expected id flow-001, got %s", flow.ID)
	}
	if flow.Intent != "read_file" {
		t.Errorf("expected intent read_file, got %s", flow.Intent)
	}
	if flow.Phase != types.FlowPhaseAction {
		t.Errorf("expected phase action, got %s", flow.Phase)
	}
	if flow.Outcome != types.FlowOutcomeSuccess {
		t.Errorf("expected outcome success, got %s", flow.Outcome)
	}
	if flow.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", flow.Confidence)
	}
	if flow.Duration != time.Second {
		t.Errorf("expected duration 1s, got %v", flow.Duration)
	}
	if len(flow.TraceIDs) != 2 || flow.TraceIDs[0] != "trace-1" {
		t.Errorf("unexpected trace ids: %v", flow.TraceIDs)
	}
}

func TestRemoteBackend_ClassifyError(t *testing.T) {
	client := &mockClassifierClient{
		classifyFn: func(_ context.Context, _ *classifierpb.ClassifyRequest, _ ...grpc.CallOption) (*classifierpb.ClassifyResponse, error) {
			return nil, errors.New("classifier overloaded")
		},
	}

	b := &RemoteBackend{
		endpoint: "test.example.com:50051",
		client:   client,
	}

	groups := [][]types.Trace{
		{{PID: 1, Syscall: "openat", Timestamp: time.Now()}},
	}

	_, err := b.Classify(context.Background(), groups)
	if err == nil {
		t.Fatal("expected Classify to fail with classifier overloaded")
	}
}

func TestRemoteBackend_Close(t *testing.T) {
	b := &RemoteBackend{
		endpoint: "test.example.com:50051",
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close with nil conn should not fail: %v", err)
	}
}

func TestRemoteBackend_marshalClassifyRequest(t *testing.T) {
	b := &RemoteBackend{}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	groups := [][]types.Trace{
		{
			{PID: 1, Syscall: "openat", Args: []string{"/tmp/test"}, ReturnValue: 3, Timestamp: now},
		},
	}

	req, err := b.marshalClassifyRequest(groups)
	if err != nil {
		t.Fatalf("marshalClassifyRequest failed: %v", err)
	}
	if len(req.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(req.Groups))
	}
	if len(req.Groups[0].Traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(req.Groups[0].Traces))
	}
	tr := req.Groups[0].Traces[0]
	if tr.Pid != 1 {
		t.Errorf("expected PID 1, got %d", tr.Pid)
	}
	if tr.Args != "/tmp/test" {
		t.Errorf("expected args '/tmp/test', got '%s'", tr.Args)
	}
	if tr.TimestampNs != now.UnixNano() {
		t.Errorf("expected timestamp %d, got %d", now.UnixNano(), tr.TimestampNs)
	}
}

func TestRemoteBackend_unmarshalClassifyResponse(t *testing.T) {
	b := &RemoteBackend{}

	// Nil response.
	flows := b.unmarshalClassifyResponse(nil)
	if len(flows) != 0 {
		t.Errorf("expected 0 flows for nil response, got %d", len(flows))
	}

	// Empty flows.
	flows = b.unmarshalClassifyResponse(&classifierpb.ClassifyResponse{})
	if len(flows) != 0 {
		t.Errorf("expected 0 flows for empty response, got %d", len(flows))
	}

	// Valid flow.
	resp := &classifierpb.ClassifyResponse{
		Flows: []*classifierpb.Flow{
			{
				Id:          "f1",
				Intent:      "read_file",
				Phase:       "action",
				Description: "Read file",
				Outcome:     "success",
				Confidence:  0.95,
				TraceIds:    []string{"t1"},
				StartTime:   time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC).UnixNano(),
				EndTime:     time.Date(2026, 7, 16, 12, 0, 1, 0, time.UTC).UnixNano(),
				DurationNs:  int64(time.Second),
			},
		},
	}

	flows = b.unmarshalClassifyResponse(resp)
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	f := flows[0]
	if f.ID != "f1" {
		t.Errorf("expected id f1, got %s", f.ID)
	}
	if f.Intent != "read_file" {
		t.Errorf("expected intent read_file, got %s", f.Intent)
	}
	if f.Duration != time.Second {
		t.Errorf("expected duration 1s, got %v", f.Duration)
	}
}

// Ensure RemoteBackend implements ClassificationBackend.
var _ ClassificationBackend = (*RemoteBackend)(nil)
