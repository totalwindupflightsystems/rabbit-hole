// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/classify"
)

func newClassifyServerCmd() *cobra.Command {
	var (
		addr  string
		token string
	)

	cmd := &cobra.Command{
		Use:   "classify-server",
		Short: "Run the reference gRPC classifier server (classifier.v1)",
		Long: `Run the reference gRPC classifier server for the classifier.v1 service.

This is the server side of the remote classification backend: a fleet or
centralized deployment runs this command and serve instances point at it
via --remote <addr>[::token]. It wraps the same pattern-matching catalog
as the local backend, so it requires no model files and no custom code.

The server blocks until it receives SIGINT or SIGTERM.`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			logger := newLogger("info")

			ps := classify.NewPatternServer(classify.WithServerVersion(Version))
			grpcServer := classify.NewGRPCServer(ps, token)

			lis, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("classify-server listen %s: %w", addr, err)
			}

			logger.Info("classify-server listening",
				"addr", lis.Addr().String(),
				"service", "classifier.v1.Classifier",
				"version", Version,
				"auth", token != "",
			)

			errCh := make(chan error, 1)
			go func() {
				if err := grpcServer.Serve(lis); err != nil {
					errCh <- err
				}
			}()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			select {
			case err := <-errCh:
				return fmt.Errorf("classify-server: %w", err)
			case <-sigCh:
				logger.Info("shutting down classify-server...")
			}

			grpcServer.GracefulStop()
			return nil
		},
	}

	cmd.Flags().StringVar(&addr, "addr", ":50051", "gRPC listen address (e.g. 127.0.0.1:50051 or :50051)")
	cmd.Flags().StringVar(&token, "token", "", "Require this bearer token on every RPC; serve --remote must then be <addr>@<token>")

	return cmd
}
