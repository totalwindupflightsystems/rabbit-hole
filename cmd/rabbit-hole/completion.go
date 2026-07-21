// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"os"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Output shell completion scripts for rabbit-hole.

To load completions:

  Bash:
    source <(rabbit-hole completion bash)

  Zsh:
    source <(rabbit-hole completion zsh)

  Fish:
    rabbit-hole completion fish | source

  PowerShell:
    rabbit-hole completion powershell | Out-String | Invoke-Expression

To install permanently:

  Bash: rabbit-hole completion bash | sudo tee /usr/share/bash-completion/completions/rabbit-hole
  Zsh:  rabbit-hole completion zsh  | sudo tee /usr/share/zsh/site-functions/_rabbit-hole
  Fish: rabbit-hole completion fish | sudo tee /usr/share/fish/vendor_completions.d/rabbit-hole.fish`,
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCompletion(cmd, args[0])
		},
	}
}

func runCompletion(cmd *cobra.Command, shell string) error {
	root := cmd.Root()
	switch shell {
	case "bash":
		return root.GenBashCompletion(os.Stdout)
	case "zsh":
		return root.GenZshCompletion(os.Stdout)
	case "fish":
		return root.GenFishCompletion(os.Stdout, true)
	case "powershell":
		return root.GenPowerShellCompletionWithDesc(os.Stdout)
	default:
		return nil
	}
}
