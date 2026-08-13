// Package classify provides pluggable classification backends:
// local Gemma 3 via Ollama, remote gRPC, and pattern matching for fast-path classification.

package classify

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	classifierpb "gitlab.readydedis.com/rabbit-hole/rabbit-hole/api/proto/classifier/v1"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// RemoteBackend connects to a centralized classifier over gRPC.
type RemoteBackend struct {
	endpoint string
	conn     *grpc.ClientConn
	client   classifierpb.ClassifierClient
	token    string
	mu       sync.RWMutex
	lastPing time.Duration
}

// NewRemoteBackend dials the gRPC endpoint and returns a ready RemoteBackend.
// When token is non-empty, every RPC carries an "authorization: Bearer <token>"
// metadata header, matching the token slot of the serve --remote flag and the
// reference classify-server's token auth.
func NewRemoteBackend(endpoint, token string) (*RemoteBackend, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	}
	if token != "" {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
			return invoker(ctx, method, req, reply, cc, opts...)
		}))
	}

	conn, err := grpc.DialContext(ctx, endpoint, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("remote backend dial %s: %w", endpoint, err)
	}

	return &RemoteBackend{
		endpoint: endpoint,
		conn:     conn,
		client:   classifierpb.NewClassifierClient(conn),
		token:    token,
	}, nil
}

// Classify sends trace groups to the remote classifier and returns the flows.
func (b *RemoteBackend) Classify(ctx context.Context, groups [][]types.Trace) ([]types.Flow, error) {
	req, err := b.marshalClassifyRequest(groups)
	if err != nil {
		return nil, fmt.Errorf("remote classify: %w", err)
	}

	resp, err := b.client.Classify(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("remote classify: %w", err)
	}

	return b.unmarshalClassifyResponse(resp), nil
}

// Health pings the remote classifier and tracks the last observed latency.
func (b *RemoteBackend) Health(ctx context.Context) error {
	start := time.Now()
	if _, err := b.client.Ping(ctx, &classifierpb.PingRequest{}); err != nil {
		return err
	}
	b.mu.Lock()
	b.lastPing = time.Since(start)
	b.mu.Unlock()
	return nil
}

// Info returns metadata about the remote backend.
func (b *RemoteBackend) Info(ctx context.Context) (ModelInfo, error) {
	resp, err := b.client.Info(ctx, &classifierpb.InfoRequest{})
	if err != nil {
		return ModelInfo{}, err
	}

	b.mu.RLock()
	latency := b.lastPing
	b.mu.RUnlock()

	return ModelInfo{
		Name:       resp.Name,
		Version:    resp.Version,
		LoadedAt:   time.Now(),
		MemoryMB:   resp.MemoryMb,
		DeviceType: resp.DeviceType,
		Kind:       "remote",
		Ready:      true,
		Endpoint:   b.endpoint,
		Latency:    latency,
	}, nil
}

// Status reports the effective backend mode: "remote gRPC <endpoint>",
// degraded with an unreachable detail when the Ping probe fails. The
// status VALUE is what /health surfaces for the classifier component.
func (b *RemoteBackend) Status(ctx context.Context) (string, string) {
	if err := b.Health(ctx); err != nil {
		return fmt.Sprintf("degraded — remote gRPC %s", b.endpoint),
			fmt.Sprintf("unreachable: %v", err)
	}
	return fmt.Sprintf("ok — remote gRPC %s", b.endpoint), ""
}

// Close releases the underlying gRPC connection.
func (b *RemoteBackend) Close() error {
	if b.conn == nil {
		return nil
	}
	return b.conn.Close()
}

func (b *RemoteBackend) marshalClassifyRequest(groups [][]types.Trace) (*classifierpb.ClassifyRequest, error) {
	req := &classifierpb.ClassifyRequest{
		Groups: make([]*classifierpb.TraceGroup, 0, len(groups)),
	}

	for _, group := range groups {
		pbGroup := &classifierpb.TraceGroup{
			Traces: make([]*classifierpb.Trace, 0, len(group)),
		}
		for _, t := range group {
			pbGroup.Traces = append(pbGroup.Traces, &classifierpb.Trace{
				Pid:         t.PID,
				Syscall:     t.Syscall,
				Args:        strings.Join(t.Args, " "),
				ReturnValue: t.ReturnValue,
				TimestampNs: t.Timestamp.UnixNano(),
			})
		}
		req.Groups = append(req.Groups, pbGroup)
	}

	return req, nil
}

func (b *RemoteBackend) unmarshalClassifyResponse(resp *classifierpb.ClassifyResponse) []types.Flow {
	if resp == nil || len(resp.Flows) == 0 {
		return nil
	}

	flows := make([]types.Flow, 0, len(resp.Flows))
	for _, f := range resp.Flows {
		flows = append(flows, types.Flow{
			ID:          f.Id,
			TraceIDs:    f.TraceIds,
			Intent:      f.Intent,
			Phase:       types.FlowPhase(f.Phase),
			Description: f.Description,
			Outcome:     types.FlowOutcome(f.Outcome),
			Confidence:  f.Confidence,
			StartTime:   time.Unix(0, f.StartTime),
			EndTime:     time.Unix(0, f.EndTime),
			Duration:    time.Duration(f.DurationNs),
		})
	}
	return flows
}

// compile-time interface check.
var _ ClassificationBackend = (*RemoteBackend)(nil)
