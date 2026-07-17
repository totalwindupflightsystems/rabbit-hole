// Package storage provides the SQLite-backed persistence layer for Rabbit-Hole.
package storage

import (
	"context"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// Storage defines the complete persistence interface for Rabbit-Hole.
// It is satisfied by *SQLiteStore. Define narrower interfaces at the
// consumer (e.g. classify.Storage) where only a subset of operations
// is needed.
type Storage interface {
	// Trace operations
	StoreTraces(ctx context.Context, traces []types.Trace) error
	QueryTraces(ctx context.Context, req types.TraceQuery) ([]types.Trace, error)

	// Flow operations
	StoreFlows(ctx context.Context, flows []types.Flow) error
	GetFlow(ctx context.Context, flowID string) (*types.Flow, error)
	QueryFlows(ctx context.Context, req types.FlowQuery) ([]types.Flow, string, error) // returns cursor

	// Session operations
	StoreSession(ctx context.Context, session *types.Session) error
	GetSession(ctx context.Context, sessionID string) (*types.Session, error)
	ListSessions(ctx context.Context, offset, limit int) ([]types.Session, error)
	UpdateSession(ctx context.Context, session *types.Session) error

	// Context window operations
	StoreContextWindow(ctx context.Context, cw *types.ContextWindow) error
	GetContextWindow(ctx context.Context, flowID string) (*types.ContextWindow, error)

	// Search
	SearchFlows(ctx context.Context, query string, limit int) ([]types.Flow, error)

	// Maintenance
	Compact(ctx context.Context, before time.Time) error
	Stats(ctx context.Context) (types.StorageStats, error)
	Health(ctx context.Context) error
}

// Compile-time check: SQLiteStore satisfies Storage.
var _ Storage = (*SQLiteStore)(nil)
