package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("rabbit-hole %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
			fmt.Printf("build: %s\n", BuildTime)
			fmt.Printf("commit: %s\n", Commit)
			return nil
		},
	}
}
