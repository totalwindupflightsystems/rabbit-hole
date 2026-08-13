// Package classify provides pluggable classification backends:
// local Gemma 3 via Ollama, remote gRPC, and pattern matching for fast-path classification.

package classify

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	classifierpb "gitlab.readydedis.com/rabbit-hole/rabbit-hole/api/proto/classifier/v1"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// PatternServer is a reference implementation of the classifier.v1.Classifier
// gRPC service (DF-023). It wraps the same pattern-matching catalog used by
// the local backend, so a fleet operator can stand up a centralized
// classifier with zero model files and no custom code.
type PatternServer struct {
	classifierpb.UnimplementedClassifierServer

	patterns *PatternCatalog
	name     string
	version  string
}

// ServerOption configures a PatternServer.
type ServerOption func(*PatternServer)

// WithServerName overrides the backend name reported by Info.
func WithServerName(name string) ServerOption {
	return func(s *PatternServer) { s.name = name }
}

// WithServerVersion overrides the backend version reported by Info.
func WithServerVersion(version string) ServerOption {
	return func(s *PatternServer) { s.version = version }
}

// NewPatternServer creates a reference classifier server backed by the
// built-in pattern catalog.
func NewPatternServer(opts ...ServerOption) *PatternServer {
	s := &PatternServer{
		patterns: NewPatternCatalog(),
		name:     "rabbit-hole-pattern-classifier",
		version:  "1.0.0",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Classify classifies each trace group with the pattern catalog. Groups that
// match a pattern produce a high-confidence flow; groups that do not match
// produce a low-confidence "unknown" flow, mirroring the local pipeline's
// fallback behaviour (makeUnknownFlow), so a remote client always receives
// one flow per input group. An empty batch returns an empty response.
func (s *PatternServer) Classify(_ context.Context, req *classifierpb.ClassifyRequest) (*classifierpb.ClassifyResponse, error) {
	if req == nil || len(req.Groups) == 0 {
		return &classifierpb.ClassifyResponse{}, nil
	}

	flows := make([]*classifierpb.Flow, 0, len(req.Groups))
	for _, group := range req.Groups {
		traces := tracesFromPB(group)
		if len(traces) == 0 {
			continue
		}
		flow, matched := s.patterns.Match(traces)
		if !matched {
			flow = unknownFlowFromTraces(traces)
		}
		flows = append(flows, flowToPB(flow))
	}

	return &classifierpb.ClassifyResponse{Flows: flows}, nil
}

// Ping is a lightweight health check. It returns immediately; response time
// is used by the serve command's health-check loop as a latency proxy.
func (s *PatternServer) Ping(context.Context, *classifierpb.PingRequest) (*classifierpb.PingResponse, error) {
	return &classifierpb.PingResponse{}, nil
}

// Info returns runtime metadata about this reference backend. It is cached
// by the calling serve instance and refreshed periodically.
func (s *PatternServer) Info(context.Context, *classifierpb.InfoRequest) (*classifierpb.InfoResponse, error) {
	return &classifierpb.InfoResponse{
		Name:       s.name,
		Version:    s.version,
		Kind:       "pattern",
		Ready:      true,
		MemoryMb:   0,
		DeviceType: "cpu",
	}, nil
}

// NewGRPCServer registers the PatternServer on a fresh grpc.Server. When
// token is non-empty, every RPC must carry an "authorization: Bearer <token>"
// metadata header, matching the token slot of the serve --remote
// endpoint[::token] client flag.
func NewGRPCServer(ps *PatternServer, token string) *grpc.Server {
	var opts []grpc.ServerOption
	if token != "" {
		opts = append(opts, grpc.UnaryInterceptor(tokenAuthInterceptor(token)))
	}
	srv := grpc.NewServer(opts...)
	classifierpb.RegisterClassifierServer(srv, ps)
	return srv
}

// tokenAuthInterceptor rejects RPCs that do not carry the expected
// "authorization: Bearer <token>" metadata header.
func tokenAuthInterceptor(expected string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing bearer token")
		}
		vals := md.Get("authorization")
		if len(vals) != 1 || !strings.EqualFold(vals[0], "Bearer "+expected) {
			return nil, status.Error(codes.Unauthenticated, "invalid bearer token")
		}
		return handler(ctx, req)
	}
}

// tracesFromPB converts a wire TraceGroup back into the internal Trace
// shape. The wire format serializes args as a single space-joined string
// (see RemoteBackend.marshalClassifyRequest), so the conversion splits on
// whitespace — filenames containing spaces are not representable on the
// wire and lose fidelity. The wire Trace has no ID field, so the returned
// traces carry empty IDs.
func tracesFromPB(group *classifierpb.TraceGroup) []types.Trace {
	if group == nil || len(group.Traces) == 0 {
		return nil
	}

	traces := make([]types.Trace, 0, len(group.Traces))
	for _, t := range group.Traces {
		if t == nil {
			continue
		}
		traces = append(traces, types.Trace{
			PID:         t.Pid,
			Timestamp:   time.Unix(0, t.TimestampNs),
			Syscall:     t.Syscall,
			Args:        splitTraceArgs(t.Args),
			ReturnValue: t.ReturnValue,
		})
	}
	return traces
}

// splitTraceArgs splits a space-joined wire arg string into the internal
// []string shape. Empty input yields nil.
func splitTraceArgs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

// flowToPB converts an internal Flow into its wire representation.
func flowToPB(f types.Flow) *classifierpb.Flow {
	return &classifierpb.Flow{
		Id:          f.ID,
		Intent:      f.Intent,
		Phase:       string(f.Phase),
		Description: f.Description,
		Outcome:     string(f.Outcome),
		Confidence:  f.Confidence,
		TraceIds:    f.TraceIDs,
		StartTime:   f.StartTime.UnixNano(),
		EndTime:     f.EndTime.UnixNano(),
		DurationNs:  int64(f.Duration),
	}
}

// unknownFlowFromTraces creates a low-confidence flow for a trace group the
// pattern catalog could not classify, mirroring makeUnknownFlow (without a
// session ID, which is not part of the wire protocol).
func unknownFlowFromTraces(traces []types.Trace) types.Flow {
	return types.Flow{
		ID:          uuid.Must(uuid.NewV7()).String(),
		TraceIDs:    extractTraceIDs(traces),
		Intent:      "unknown",
		Phase:       types.FlowPhaseAction,
		Outcome:     types.FlowOutcomeUnknown,
		Confidence:  0,
		StartTime:   traces[0].Timestamp,
		EndTime:     traces[len(traces)-1].Timestamp,
		Duration:    traces[len(traces)-1].Timestamp.Sub(traces[0].Timestamp),
		Description: buildGroupDescription(traces),
	}
}

// compile-time interface check.
var _ classifierpb.ClassifierServer = (*PatternServer)(nil)
