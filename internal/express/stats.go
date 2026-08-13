// Package express provides the HTTP/WebSocket expression server for Rabbit-Hole.
// This file implements GET /api/v1/stats — the daemon-side aggregate view
// the CLI `status` command renders. The daemon owns the database (DF-001),
// so the endpoint reports the daemon's store and runtime, never the CLI's
// local configuration (DF-014).
package express

import (
	"net/http"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// RuntimeInfo carries daemon-side runtime facts that the stats endpoint
// surfaces but that live outside the storage layer (log level, eBPF state).
// It is wired by the serve command via SetRuntimeInfo; the zero value is
// safe and the endpoint still works (eBPF simply reports disabled).
type RuntimeInfo struct {
	LogLevel    string // daemon log level: debug|info|warn|error
	EBPFEnabled bool   // kernel probes active
	EBPFDetail  string // why eBPF is disabled, when it is
}

// SetRuntimeInfo records daemon runtime facts for GET /api/v1/stats.
// Safe to call after NewServer; last writer wins.
func (s *Server) SetRuntimeInfo(info RuntimeInfo) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.runtimeInfo = info
}

// snapshotRuntimeInfo returns the recorded runtime facts under the read lock.
func (s *Server) snapshotRuntimeInfo() RuntimeInfo {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.runtimeInfo
}

// statsSessionLimit bounds the session scan used to compute the active
// session count. The collector caps concurrent sessions at 50 (MaxSessions),
// so 10000 is effectively unbounded for counting.
const statsSessionLimit = 10000

// handleStats aggregates storage and runtime facts for `rabbit-hole status`.
// The listen address reports the daemon's ACTUAL bound address: the
// "listen_addr" DB metadata row written by serve at startup wins (DF-007),
// with the server's own bound address as the fallback (covers test servers
// and old DBs that never wrote the row).
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	storageStats, err := s.store.Stats(ctx)
	if err != nil {
		s.logger.Error("stats failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read storage stats")
		return
	}

	sessions, err := s.store.ListSessions(ctx, 0, statsSessionLimit)
	if err != nil {
		s.logger.Error("stats sessions failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	var active int64
	for _, sess := range sessions {
		if sess.Status == types.SessionStatusRunning {
			active++
		}
	}

	listenAddr := s.srv.Addr
	if stored, ok, err := s.store.GetMetadata(ctx, "listen_addr"); err == nil && ok && stored != "" {
		listenAddr = stored
	}

	rt := s.snapshotRuntimeInfo()

	var oldest *time.Time
	if !storageStats.OldestTrace.IsZero() {
		t := storageStats.OldestTrace
		oldest = &t
	}

	writeJSON(w, http.StatusOK, types.DaemonStats{
		DBPath:         s.store.Path(),
		ListenAddr:     listenAddr,
		TotalSessions:  storageStats.TotalSessions,
		ActiveSessions: active,
		TotalTraces:    storageStats.TotalTraces,
		TotalFlows:     storageStats.TotalFlows,
		DBSizeBytes:    storageStats.DBSizeBytes,
		OldestTrace:    oldest,
		LogLevel:       rt.LogLevel,
		EBPFEnabled:    rt.EBPFEnabled,
		EBPFDetail:     rt.EBPFDetail,
	})
}
