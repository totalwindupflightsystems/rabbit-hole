// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/attach"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/classify"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/demo"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/express"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func newServeCmd() *cobra.Command {
	var (
		addr         string
		noClassifier bool
		remote       string
		demoStream   bool
		demoEveryS   int
		noEBPF       bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Rabbit-Hole daemon (collect + classify + serve)",
		Long: `Start the full Rabbit-Hole daemon: open the database, load the classification
model, initialise the eBPF collector, and start the HTTP/WebSocket server.

The daemon blocks until it receives SIGINT or SIGTERM.

Dogfood mode (--demo-stream): with no root/eBPF available, generate one
realistic flow every N seconds into a demo session and publish it to the
dashboard's Live tab over WebSocket. This is how Rabbit-Hole eats its own
dog food — open /dashboard, hit Live Stream, and watch the agent work.`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			if addr != "" {
				cfg.ListenAddr = addr
			}

			logger := newLogger(cfg.LogLevel)

			// 1. Storage
			store, err := storage.NewSQLiteStore(cfg.DBPath, logger)
			if err != nil {
				return fmt.Errorf("storage: %w", err)
			}
			defer store.Close()

			// Crash recovery: reconcile sessions that were running at last shutdown.
			count, err := store.ReconcileCrashedSessions(cobraCmd.Context())
			if err != nil {
				return fmt.Errorf("crash recovery: %w", err)
			}
			if count > 0 {
				logger.Info("crash recovery: reconciled sessions", "count", count)
			}

			// 2. Collector
			coll, err := collector.NewEBPFCollector(cfg.BufferSize, cfg.MaxSessions, logger)
			if err != nil {
				return fmt.Errorf("collector: %w", err)
			}

			// Preflight: serve promises kernel telemetry, so degraded
			// eBPF is a hard error unless the user explicitly opted into
			// degraded mode (--no-ebpf or the --demo-stream dogfood mode,
			// which is documented as "no root/eBPF available").
			if !coll.EBPFEnabled() {
				if !noEBPF && !demoStream {
					return coll.PreflightEBPF()
				}
				logger.Warn("eBPF unavailable — telemetry DISABLED, running in degraded mode (--no-ebpf or --demo-stream)")
			} else if noEBPF {
				logger.Warn("eBPF disabled via --no-ebpf — telemetry DISABLED, running in degraded mode")
			}

			// 3. Classifier
			var cls classify.Classifier
			if !noClassifier {
				var backend classify.ClassificationBackend
				if remote != "" {
					endpoint, token, err := parseRemoteFlag(remote)
					if err != nil {
						return fmt.Errorf("remote: %w", err)
					}
					backend, err = classify.NewRemoteBackend(endpoint, token)
					if err != nil {
						return fmt.Errorf("remote backend: %w", err)
					}
				} else {
					model := classify.NewGemmaModel(cfg.ModelPath, cfg.ModelName, "")
					if err := model.Load(cobraCmd.Context()); err != nil {
						logger.Warn("failed to load classification model — pattern matching only", "err", err)
					} else {
						defer model.Unload()
					}
					backend = classify.NewLocalBackend(model)
				}
				cls = classify.NewClassifier(classify.NewClassificationEngine(backend, store, logger))
			}

			// 4. Pipeline: collector → classifier → store
			pipeline := attach.NewPipeline(coll, cls, store, logger)
			go pipeline.Run(cobraCmd.Context())

			// 5. Expression server
			rl := express.NewRateLimiter(
				cfg.RateLimitSearchRPS,
				cfg.RateLimitChatRPS,
				cfg.RateLimitWSRPS,
				cfg.RateLimitEnabled,
			)
			server := express.NewServer(store, logger, cfg.ListenAddr, rl)

			// Session lifecycle: the attach/detach CLI commands hand sessions
			// to the daemon over HTTP. The daemon owns the collector and the
			// database, so sessions persist across CLI invocations (DF-001).
			// The pipeline above picks up sessions started this way and
			// classifies their traces like any other.
			server.RegisterSessionManager(attach.NewSessionManager(coll, store, logger))

			// Daemon runtime facts for GET /api/v1/stats (`rabbit-hole
			// status` reads them via the daemon, not the CLI's own config —
			// DF-014). eBPF counts as disabled when it is unavailable or
			// explicitly turned off with --no-ebpf.
			ebpfEnabled := coll.EBPFEnabled()
			ebpfDetail := "kernel probes attached"
			if !ebpfEnabled {
				ebpfDetail = "no kernel probes — telemetry not collected; see README for required privileges"
			} else if noEBPF {
				ebpfEnabled = false
				ebpfDetail = "disabled via --no-ebpf"
			}
			server.SetRuntimeInfo(express.RuntimeInfo{
				LogLevel:    cfg.LogLevel,
				EBPFEnabled: ebpfEnabled,
				EBPFDetail:  ebpfDetail,
			})

			// Wire component-level health checks. Storage and metrics are
			// reported automatically by the server (they live on the Server
			// struct). Classifier and collector are wired here because they
			// are constructed in the serve command.
			//
			// The classifier uses a Status func so /health reports the
			// EFFECTIVE backend mode truthfully (DF-022): "pattern-only
			// (model not loaded)" on zero-config installs and load failures,
			// "gemma via ollama <url>" when the local model is loaded, or
			// "remote gRPC <endpoint>" — never a static "local model
			// backend" claim while no model is actually loaded.
			if cls != nil {
				clsRef := cls
				server.RegisterHealthCheck(express.HealthCheck{
					Name:   "classifier",
					Status: clsRef.Status,
				})
			}
			collRef := coll
			collectorDetail := "eBPF probes attached"
			if !coll.EBPFEnabled() {
				collectorDetail = "eBPF degraded — telemetry DISABLED"
			}
			server.RegisterHealthCheck(express.HealthCheck{
				Name:   "collector",
				Detail: collectorDetail,
				Check:  collRef.Health,
			})

			if err := server.Start(cobraCmd.Context()); err != nil {
				return fmt.Errorf("server: %w", err)
			}

			// Record the ACTUAL bound address (server.Addr() reflects
			// --addr and :0 ephemeral ports) in DB metadata so `status`
			// reports what is really listening, not the configured
			// default (DF-007).
			if err := store.SetMetadata(cobraCmd.Context(), "listen_addr", server.Addr()); err != nil {
				return fmt.Errorf("store listen addr: %w", err)
			}

			// Dogfood live stream: generate + store + publish one flow every N
			// seconds into a dedicated demo session. The dashboard's Live tab
			// subscribes via WebSocket and shows flows arriving in real time.
			if demoStream {
				sc := demo.Generate(1, time.Now(), time.Now().UnixNano())
				sessionID := sc.Session.ID
				_ = store.StoreSession(cobraCmd.Context(), &sc.Session)
				streamCtx, streamCancel := context.WithCancel(cobraCmd.Context())
				defer streamCancel()
				go func() {
					ticker := time.NewTicker(time.Duration(demoEveryS) * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-streamCtx.Done():
							return
						case <-ticker.C:
							sc := demo.Generate(1, time.Now(), time.Now().UnixNano())
							f := sc.Flows[0]
							f.SessionID = sessionID
							f.ID = fmt.Sprintf("fl-live-%d", time.Now().UnixNano())
							if err := store.StoreFlows(streamCtx, []types.Flow{f}); err != nil {
								logger.Error("demo stream: store", "err", err)
								continue
							}
							server.PublishFlow(sessionID, f)
							logger.Info("demo stream: flow published", "intent", f.Intent)
						}
					}
				}()
				logger.Info("dogfood live stream enabled", "session", sessionID, "every_s", demoEveryS)
			}

			logger.Info("rabbit-hole is running", "addr", server.Addr(), "pid", os.Getpid())

			// Wait for signal
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh

			logger.Info("shutting down...")
			server.Shutdown(context.Background())
			return nil
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (default: 127.0.0.1:9734)")
	cmd.Flags().BoolVar(&noClassifier, "no-classifier", false, "Disable classification (pattern matching only)")
	cmd.Flags().StringVar(&remote, "remote", "", "Remote classifier endpoint[::token] (e.g. localhost:50051 or host:443@token)")
	cmd.Flags().BoolVar(&demoStream, "demo-stream", false, "Dogfood mode: publish a demo flow every N seconds (no root needed)")
	cmd.Flags().IntVar(&demoEveryS, "demo-every", 3, "Seconds between demo-stream flows")
	cmd.Flags().BoolVar(&noEBPF, "no-ebpf", false, "Run in degraded mode without eBPF (telemetry DISABLED)")

	return cmd
}

// parseRemoteFlag parses the --remote value of the form "endpoint" or
// "endpoint@token". The token is optional.
func parseRemoteFlag(s string) (endpoint, token string, err error) {
	if s == "" {
		return "", "", fmt.Errorf("remote endpoint cannot be empty")
	}
	parts := strings.SplitN(s, "@", 2)
	endpoint = parts[0]
	if len(parts) == 2 {
		token = parts[1]
	}
	if endpoint == "" {
		return "", "", fmt.Errorf("remote endpoint cannot be empty")
	}
	return endpoint, token, nil
}
