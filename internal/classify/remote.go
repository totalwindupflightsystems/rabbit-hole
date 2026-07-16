package classify

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/classify/pb"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// RemoteBackend connects to a centralized classifier over gRPC.
type RemoteBackend struct {
	endpoint string
	conn     *grpc.ClientConn
	client   pb.ClassifierClient
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
		client:   pb.NewClassifierClient(conn),
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
	if _, err := b.client.Ping(ctx, &pb.PingRequest{}); err != nil {
		return err
	}
	b.mu.Lock()
	b.lastPing = time.Since(start)
	b.mu.Unlock()
	return nil
}

// Info returns metadata about the remote backend.
func (b *RemoteBackend) Info(ctx context.Context) (ModelInfo, error) {
	resp, err := b.client.Info(ctx, &pb.InfoRequest{})
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
		MemoryMB:   resp.MemoryMB,
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

func (b *RemoteBackend) marshalClassifyRequest(groups [][]types.Trace) (*pb.ClassifyRequest, error) {
	req := &pb.ClassifyRequest{
		Groups: make([]*pb.TraceGroup, 0, len(groups)),
	}

	for _, group := range groups {
		pbGroup := &pb.TraceGroup{
			Traces: make([]*pb.Trace, 0, len(group)),
		}
		for _, t := range group {
			pbGroup.Traces = append(pbGroup.Traces, &pb.Trace{
				PID:          t.PID,
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

func (b *RemoteBackend) unmarshalClassifyResponse(resp *pb.ClassifyResponse) []types.Flow {
	if resp == nil || len(resp.Flows) == 0 {
		return nil
	}

	flows := make([]types.Flow, 0, len(resp.Flows))
	for _, f := range resp.Flows {
		flows = append(flows, types.Flow{
			ID:          f.ID,
			TraceIDs:    f.TraceIDs,
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
