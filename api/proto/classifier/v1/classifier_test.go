package classifierpb

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// roundTrip marshals m and unmarshals it into a new message of the same type,
// failing the test on any error.
func roundTrip[M proto.Message](t *testing.T, m M) M {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}
	out := m.ProtoReflect().New().Interface().(M)
	if err := proto.Unmarshal(data, out); err != nil {
		t.Fatalf("proto.Unmarshal failed: %v", err)
	}
	if !proto.Equal(m, out) {
		t.Fatalf("round-trip mismatch:\n got: %v\nwant: %v", out, m)
	}
	return out
}

func TestTrace_FieldsAndGetters(t *testing.T) {
	tr := &Trace{
		Pid:         4242,
		Syscall:     "openat",
		Args:        "flags=O_RDONLY",
		ReturnValue: 3,
		TimestampNs: 1721400000000000000,
	}
	if got := tr.GetPid(); got != 4242 {
		t.Errorf("GetPid() = %d, want 4242", got)
	}
	if got := tr.GetSyscall(); got != "openat" {
		t.Errorf("GetSyscall() = %q, want %q", got, "openat")
	}
	if got := tr.GetArgs(); got != "flags=O_RDONLY" {
		t.Errorf("GetArgs() = %q, want %q", got, "flags=O_RDONLY")
	}
	if got := tr.GetReturnValue(); got != 3 {
		t.Errorf("GetReturnValue() = %d, want 3", got)
	}
	if got := tr.GetTimestampNs(); got != 1721400000000000000 {
		t.Errorf("GetTimestampNs() = %d, want 1721400000000000000", got)
	}

	roundTrip(t, tr)
}

func TestTrace_NilGetters(t *testing.T) {
	var tr *Trace
	if got := tr.GetPid(); got != 0 {
		t.Errorf("nil GetPid() = %d, want 0", got)
	}
	if got := tr.GetSyscall(); got != "" {
		t.Errorf("nil GetSyscall() = %q, want empty", got)
	}
	if got := tr.GetArgs(); got != "" {
		t.Errorf("nil GetArgs() = %q, want empty", got)
	}
	if got := tr.GetReturnValue(); got != 0 {
		t.Errorf("nil GetReturnValue() = %d, want 0", got)
	}
	if got := tr.GetTimestampNs(); got != 0 {
		t.Errorf("nil GetTimestampNs() = %d, want 0", got)
	}
}

func TestTraceGroup_RepeatedTraces(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		tg := &TraceGroup{}
		if got := tg.GetTraces(); len(got) != 0 {
			t.Errorf("GetTraces() len = %d, want 0", len(got))
		}
		roundTrip(t, tg)
	})

	t.Run("with traces", func(t *testing.T) {
		tg := &TraceGroup{
			Traces: []*Trace{
				{Pid: 1, Syscall: "read"},
				{Pid: 1, Syscall: "write", ReturnValue: 42},
				{Pid: 2, Syscall: "execve", TimestampNs: 999},
			},
		}
		if got := tg.GetTraces(); len(got) != 3 {
			t.Fatalf("GetTraces() len = %d, want 3", len(got))
		}
		out := roundTrip(t, tg)
		if out.GetTraces()[1].GetReturnValue() != 42 {
			t.Errorf("round-trip traces[1].ReturnValue = %d, want 42", out.GetTraces()[1].GetReturnValue())
		}
	})

	t.Run("nil getter", func(t *testing.T) {
		var tg *TraceGroup
		if got := tg.GetTraces(); got != nil {
			t.Errorf("nil GetTraces() = %v, want nil", got)
		}
	})
}

func TestClassifyRequest_Groups(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		req := &ClassifyRequest{}
		if got := req.GetGroups(); len(got) != 0 {
			t.Errorf("GetGroups() len = %d, want 0", len(got))
		}
		roundTrip(t, req)
	})

	t.Run("multiple groups", func(t *testing.T) {
		req := &ClassifyRequest{
			Groups: []*TraceGroup{
				{Traces: []*Trace{{Pid: 100, Syscall: "openat"}}},
				{Traces: []*Trace{{Pid: 200, Syscall: "clone"}, {Pid: 200, Syscall: "exit_group"}}},
			},
		}
		if got := req.GetGroups(); len(got) != 2 {
			t.Fatalf("GetGroups() len = %d, want 2", len(got))
		}
		out := roundTrip(t, req)
		if out.GetGroups()[1].GetTraces()[1].GetSyscall() != "exit_group" {
			t.Errorf("round-trip groups[1].traces[1].Syscall = %q, want %q",
				out.GetGroups()[1].GetTraces()[1].GetSyscall(), "exit_group")
		}
	})

	t.Run("nil getter", func(t *testing.T) {
		var req *ClassifyRequest
		if got := req.GetGroups(); got != nil {
			t.Errorf("nil GetGroups() = %v, want nil", got)
		}
	})
}

func TestFlow_FieldsAndGetters(t *testing.T) {
	f := &Flow{
		Id:          "flow-abc123",
		Intent:      "read-config",
		Phase:       "setup",
		Description: "Agent read its configuration file",
		Outcome:     "success",
		Confidence:  0.95,
		TraceIds:    []string{"t-1", "t-2", "t-3"},
		StartTime:   1721400000,
		EndTime:     1721400005,
		DurationNs:  5000000000,
	}
	if got := f.GetId(); got != "flow-abc123" {
		t.Errorf("GetId() = %q, want %q", got, "flow-abc123")
	}
	if got := f.GetIntent(); got != "read-config" {
		t.Errorf("GetIntent() = %q, want %q", got, "read-config")
	}
	if got := f.GetPhase(); got != "setup" {
		t.Errorf("GetPhase() = %q, want %q", got, "setup")
	}
	if got := f.GetDescription(); got != "Agent read its configuration file" {
		t.Errorf("GetDescription() = %q", got)
	}
	if got := f.GetOutcome(); got != "success" {
		t.Errorf("GetOutcome() = %q, want %q", got, "success")
	}
	if got := f.GetConfidence(); got != 0.95 {
		t.Errorf("GetConfidence() = %v, want 0.95", got)
	}
	if got := f.GetTraceIds(); len(got) != 3 {
		t.Errorf("GetTraceIds() len = %d, want 3", len(got))
	}
	if got := f.GetStartTime(); got != 1721400000 {
		t.Errorf("GetStartTime() = %d, want 1721400000", got)
	}
	if got := f.GetEndTime(); got != 1721400005 {
		t.Errorf("GetEndTime() = %d, want 1721400005", got)
	}
	if got := f.GetDurationNs(); got != 5000000000 {
		t.Errorf("GetDurationNs() = %d, want 5000000000", got)
	}

	roundTrip(t, f)
}

func TestFlow_NilGetters(t *testing.T) {
	var f *Flow
	if got := f.GetId(); got != "" {
		t.Errorf("nil GetId() = %q, want empty", got)
	}
	if got := f.GetIntent(); got != "" {
		t.Errorf("nil GetIntent() = %q, want empty", got)
	}
	if got := f.GetPhase(); got != "" {
		t.Errorf("nil GetPhase() = %q, want empty", got)
	}
	if got := f.GetDescription(); got != "" {
		t.Errorf("nil GetDescription() = %q, want empty", got)
	}
	if got := f.GetOutcome(); got != "" {
		t.Errorf("nil GetOutcome() = %q, want empty", got)
	}
	if got := f.GetConfidence(); got != 0 {
		t.Errorf("nil GetConfidence() = %v, want 0", got)
	}
	if got := f.GetTraceIds(); got != nil {
		t.Errorf("nil GetTraceIds() = %v, want nil", got)
	}
	if got := f.GetStartTime(); got != 0 {
		t.Errorf("nil GetStartTime() = %d, want 0", got)
	}
	if got := f.GetEndTime(); got != 0 {
		t.Errorf("nil GetEndTime() = %d, want 0", got)
	}
	if got := f.GetDurationNs(); got != 0 {
		t.Errorf("nil GetDurationNs() = %d, want 0", got)
	}
}

func TestFlow_ZeroConfidenceRoundTrip(t *testing.T) {
	// proto3 omits zero scalar values on the wire; make sure a zero-confidence
	// flow still round-trips cleanly (proto.Equal on empty vs zero).
	f := &Flow{Id: "f-zero", Confidence: 0.0}
	out := roundTrip(t, f)
	if out.GetConfidence() != 0.0 {
		t.Errorf("round-trip Confidence = %v, want 0.0", out.GetConfidence())
	}
}

func TestClassifyResponse_Flows(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		resp := &ClassifyResponse{}
		if got := resp.GetFlows(); len(got) != 0 {
			t.Errorf("GetFlows() len = %d, want 0", len(got))
		}
		roundTrip(t, resp)
	})

	t.Run("with flows", func(t *testing.T) {
		resp := &ClassifyResponse{
			Flows: []*Flow{
				{Id: "f-1", Intent: "read-file", Confidence: 0.9},
				{Id: "f-2", Intent: "spawn-child", Phase: "execute", Outcome: "failure"},
			},
		}
		if got := resp.GetFlows(); len(got) != 2 {
			t.Fatalf("GetFlows() len = %d, want 2", len(got))
		}
		out := roundTrip(t, resp)
		if out.GetFlows()[1].GetOutcome() != "failure" {
			t.Errorf("round-trip flows[1].Outcome = %q, want %q",
				out.GetFlows()[1].GetOutcome(), "failure")
		}
	})

	t.Run("nil getter", func(t *testing.T) {
		var resp *ClassifyResponse
		if got := resp.GetFlows(); got != nil {
			t.Errorf("nil GetFlows() = %v, want nil", got)
		}
	})
}

func TestPingMessages(t *testing.T) {
	req := &PingRequest{}
	resp := &PingResponse{}
	roundTrip(t, req)
	roundTrip(t, resp)
	// Empty messages have an empty String(); ProtoMessage marker must not panic.
	req.ProtoMessage()
	resp.ProtoMessage()
}

func TestInfoMessages(t *testing.T) {
	req := &InfoRequest{}
	roundTrip(t, req)

	resp := &InfoResponse{
		Name:       "gemma-3n",
		Version:    "v0.3.1",
		Kind:       "local",
		Ready:      true,
		MemoryMb:   4096,
		DeviceType: "cuda",
	}
	if got := resp.GetName(); got != "gemma-3n" {
		t.Errorf("GetName() = %q, want %q", got, "gemma-3n")
	}
	if got := resp.GetVersion(); got != "v0.3.1" {
		t.Errorf("GetVersion() = %q, want %q", got, "v0.3.1")
	}
	if got := resp.GetKind(); got != "local" {
		t.Errorf("GetKind() = %q, want %q", got, "local")
	}
	if got := resp.GetReady(); !got {
		t.Error("GetReady() = false, want true")
	}
	if got := resp.GetMemoryMb(); got != 4096 {
		t.Errorf("GetMemoryMb() = %d, want 4096", got)
	}
	if got := resp.GetDeviceType(); got != "cuda" {
		t.Errorf("GetDeviceType() = %q, want %q", got, "cuda")
	}

	roundTrip(t, resp)

	t.Run("nil getters", func(t *testing.T) {
		var r *InfoResponse
		if got := r.GetName(); got != "" {
			t.Errorf("nil GetName() = %q, want empty", got)
		}
		if got := r.GetVersion(); got != "" {
			t.Errorf("nil GetVersion() = %q, want empty", got)
		}
		if got := r.GetKind(); got != "" {
			t.Errorf("nil GetKind() = %q, want empty", got)
		}
		if got := r.GetReady(); got {
			t.Error("nil GetReady() = true, want false")
		}
		if got := r.GetMemoryMb(); got != 0 {
			t.Errorf("nil GetMemoryMb() = %d, want 0", got)
		}
		if got := r.GetDeviceType(); got != "" {
			t.Errorf("nil GetDeviceType() = %q, want empty", got)
		}
	})
}

func TestMessageReset(t *testing.T) {
	f := &Flow{Id: "to-reset", Confidence: 0.5, TraceIds: []string{"a"}}
	f.Reset()
	if f.GetId() != "" || f.GetConfidence() != 0 || len(f.GetTraceIds()) != 0 {
		t.Errorf("Flow not cleared after Reset(): %+v", f)
	}

	req := &ClassifyRequest{Groups: []*TraceGroup{{}}}
	req.Reset()
	if len(req.GetGroups()) != 0 {
		t.Errorf("ClassifyRequest not cleared after Reset(): %+v", req)
	}
}

func TestProtoReflectDescriptor(t *testing.T) {
	// Descriptors must be registered and resolvable for all 9 messages.
	messages := []proto.Message{
		&ClassifyRequest{}, &TraceGroup{}, &Trace{},
		&ClassifyResponse{}, &Flow{},
		&PingRequest{}, &PingResponse{},
		&InfoRequest{}, &InfoResponse{},
	}
	wantNames := []string{
		"classifier.v1.ClassifyRequest", "classifier.v1.TraceGroup", "classifier.v1.Trace",
		"classifier.v1.ClassifyResponse", "classifier.v1.Flow",
		"classifier.v1.PingRequest", "classifier.v1.PingResponse",
		"classifier.v1.InfoRequest", "classifier.v1.InfoResponse",
	}
	for i, m := range messages {
		name := string(m.ProtoReflect().Descriptor().FullName())
		if name != wantNames[i] {
			t.Errorf("message %d descriptor = %q, want %q", i, name, wantNames[i])
		}
	}
}

func TestUnimplementedClassifierServer(t *testing.T) {
	srv := UnimplementedClassifierServer{}
	ctx := context.Background()

	if _, err := srv.Classify(ctx, &ClassifyRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Classify() code = %v, want Unimplemented (err=%v)", status.Code(err), err)
	}
	if _, err := srv.Ping(ctx, &PingRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Ping() code = %v, want Unimplemented (err=%v)", status.Code(err), err)
	}
	if _, err := srv.Info(ctx, &InfoRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Info() code = %v, want Unimplemented (err=%v)", status.Code(err), err)
	}
}

func TestServiceDescAndMethodNames(t *testing.T) {
	if Classifier_ServiceDesc.ServiceName != "classifier.v1.Classifier" {
		t.Errorf("ServiceName = %q, want %q", Classifier_ServiceDesc.ServiceName, "classifier.v1.Classifier")
	}
	if len(Classifier_ServiceDesc.Methods) != 3 {
		t.Fatalf("ServiceDesc has %d methods, want 3", len(Classifier_ServiceDesc.Methods))
	}
	wantMethods := []string{"Classify", "Ping", "Info"}
	for i, m := range Classifier_ServiceDesc.Methods {
		if m.MethodName != wantMethods[i] {
			t.Errorf("Methods[%d] = %q, want %q", i, m.MethodName, wantMethods[i])
		}
	}
	if Classifier_Classify_FullMethodName != "/classifier.v1.Classifier/Classify" {
		t.Errorf("Classifier_Classify_FullMethodName = %q", Classifier_Classify_FullMethodName)
	}
	if Classifier_Ping_FullMethodName != "/classifier.v1.Classifier/Ping" {
		t.Errorf("Classifier_Ping_FullMethodName = %q", Classifier_Ping_FullMethodName)
	}
	if Classifier_Info_FullMethodName != "/classifier.v1.Classifier/Info" {
		t.Errorf("Classifier_Info_FullMethodName = %q", Classifier_Info_FullMethodName)
	}
}

// fakeServer implements ClassifierServer to exercise registration and handlers.
type fakeServer struct {
	UnimplementedClassifierServer
}

func (fakeServer) Ping(_ context.Context, _ *PingRequest) (*PingResponse, error) {
	return &PingResponse{}, nil
}

func TestRegisterClassifierServer(t *testing.T) {
	s := grpc.NewServer()
	RegisterClassifierServer(s, fakeServer{})
	// Registration succeeded if no panic; verify the service shows up.
	info := s.GetServiceInfo()
	svc, ok := info["classifier.v1.Classifier"]
	if !ok {
		t.Fatal("classifier.v1.Classifier not registered on grpc.Server")
	}
	if len(svc.Methods) != 3 {
		t.Errorf("registered service has %d methods, want 3", len(svc.Methods))
	}
}

func TestClientConstructor(t *testing.T) {
	// NewClassifierClient should return a non-nil client bound to the conn.
	c := NewClassifierClient(nil)
	if c == nil {
		t.Fatal("NewClassifierClient(nil) returned nil")
	}
}

// fakeConn implements grpc.ClientConnInterface for client-side tests.
type fakeConn struct {
	invokeFn func(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error
}

func (f fakeConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	return f.invokeFn(ctx, method, args, reply, opts...)
}

func (f fakeConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, status.Error(codes.Unimplemented, "streams not supported by fakeConn")
}

func TestClassifierClient_Invoke(t *testing.T) {
	var gotMethod string
	conn := fakeConn{
		invokeFn: func(_ context.Context, method string, _, reply any, _ ...grpc.CallOption) error {
			gotMethod = method
			switch r := reply.(type) {
			case *ClassifyResponse:
				r.Flows = []*Flow{{Id: "f-1"}}
			case *InfoResponse:
				r.Name = "test-model"
				r.Ready = true
			}
			return nil
		},
	}
	c := NewClassifierClient(conn)
	ctx := context.Background()

	if _, err := c.Classify(ctx, &ClassifyRequest{}); err != nil {
		t.Fatalf("Classify() error: %v", err)
	}
	if gotMethod != Classifier_Classify_FullMethodName {
		t.Errorf("Classify invoked %q, want %q", gotMethod, Classifier_Classify_FullMethodName)
	}

	if _, err := c.Ping(ctx, &PingRequest{}); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
	if gotMethod != Classifier_Ping_FullMethodName {
		t.Errorf("Ping invoked %q, want %q", gotMethod, Classifier_Ping_FullMethodName)
	}

	info, err := c.Info(ctx, &InfoRequest{})
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}
	if gotMethod != Classifier_Info_FullMethodName {
		t.Errorf("Info invoked %q, want %q", gotMethod, Classifier_Info_FullMethodName)
	}
	if info.GetName() != "test-model" || !info.GetReady() {
		t.Errorf("Info reply = %+v, want Name=test-model Ready=true", info)
	}
}

func TestClassifierClient_InvokeError(t *testing.T) {
	conn := fakeConn{
		invokeFn: func(context.Context, string, any, any, ...grpc.CallOption) error {
			return status.Error(codes.Unavailable, "backend down")
		},
	}
	c := NewClassifierClient(conn)
	ctx := context.Background()

	if _, err := c.Classify(ctx, &ClassifyRequest{}); status.Code(err) != codes.Unavailable {
		t.Errorf("Classify() code = %v, want Unavailable", status.Code(err))
	}
	if _, err := c.Ping(ctx, &PingRequest{}); status.Code(err) != codes.Unavailable {
		t.Errorf("Ping() code = %v, want Unavailable", status.Code(err))
	}
	if _, err := c.Info(ctx, &InfoRequest{}); status.Code(err) != codes.Unavailable {
		t.Errorf("Info() code = %v, want Unavailable", status.Code(err))
	}
}

func TestStringAndDescriptor(t *testing.T) {
	// String() on populated messages must include field values.
	f := &Flow{Id: "stringy", Confidence: 0.5}
	if s := f.String(); s == "" {
		t.Error("Flow.String() should not be empty for populated message")
	}
	tr := &Trace{Pid: 7, Syscall: "openat"}
	if s := tr.String(); s == "" {
		t.Error("Trace.String() should not be empty for populated message")
	}

	// Deprecated Descriptor() methods must return non-empty gzipped descriptors.
	messages := []proto.Message{
		&ClassifyRequest{}, &TraceGroup{}, &Trace{},
		&ClassifyResponse{}, &Flow{},
		&PingRequest{}, &PingResponse{},
		&InfoRequest{}, &InfoResponse{},
	}
	for i, m := range messages {
		type descriptor interface {
			Descriptor() ([]byte, []int)
		}
		d, ok := m.(descriptor)
		if !ok {
			t.Fatalf("message %d does not implement deprecated Descriptor()", i)
		}
		raw, path := d.Descriptor()
		if len(raw) == 0 {
			t.Errorf("message %d Descriptor() returned empty bytes", i)
		}
		if len(path) != 1 || path[0] != i {
			t.Errorf("message %d Descriptor() path = %v, want [%d]", i, path, i)
		}
	}
}
