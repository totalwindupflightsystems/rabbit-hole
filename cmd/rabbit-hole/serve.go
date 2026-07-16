package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/attach"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/classify"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/express"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
)

func newServeCmd() *cobra.Command {
	var (
		addr         string
		noClassifier bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Rabbit-Hole daemon (collect + classify + serve)",
		Long: `Start the full Rabbit-Hole daemon: open the database, load the classification
model, initialise the eBPF collector, and start the HTTP/WebSocket server.

The daemon blocks until it receives SIGINT or SIGTERM.`,
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

			// 2. Collector
			coll, err := collector.NewEBPFCollector(cfg.BufferSize, cfg.MaxSessions, logger)
			if err != nil {
				return fmt.Errorf("collector: %w", err)
			}

			// 3. Classifier
			var cls classify.Classifier
			if !noClassifier {
				model := classify.NewGemmaModel(cfg.ModelPath, cfg.ModelName)
				if err := model.Load(cobraCmd.Context()); err != nil {
					logger.Warn("failed to load classification model — pattern matching only", "err", err)
				} else {
					defer model.Unload()
				}
				cls = classify.NewClassifier(classify.NewClassificationEngine(classify.NewLocalBackend(model), store, logger))
			}

			// 4. Pipeline: collector → classifier → store
			pipeline := attach.NewPipeline(coll, cls, store, logger)
			go pipeline.Run(cobraCmd.Context())

			// 5. Expression server
			server := express.NewServer(store, logger, cfg.ListenAddr)
			if err := server.Start(cobraCmd.Context()); err != nil {
				return fmt.Errorf("server: %w", err)
			}

			logger.Info("rabbit-hole is running", "addr", cfg.ListenAddr, "pid", os.Getpid())

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

	return cmd
}
