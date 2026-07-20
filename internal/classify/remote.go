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

	classifierpb "github.com/totalwindupflightsystems/rabbit-hole/api/proto/classifier/v1"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
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
func NewRemoteBackend(endpoint, token string) (*RemoteBackend, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
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
				Pid:          t.PID,
				Syscall:      t.Syscall,
				Args:         strings.Join(t.Args, " "),
				ReturnValue:  t.ReturnValue,
				TimestampNs:  t.Timestamp.UnixNano(),
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
