package main

import "github.com/spf13/cobra"

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the Rabbit-Hole daemon (collect + classify + serve)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}
