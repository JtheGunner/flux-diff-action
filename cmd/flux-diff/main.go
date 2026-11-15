// Command flux-diff posts a readable, secret-safe diff of Flux manifests.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func newRootCommand() *cobra.Command {
	var opts runOptions

	cmd := &cobra.Command{
		Use:           "flux-diff",
		Short:         "Post a readable, secret-safe Flux manifest diff",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDiff(cmd.Context(), opts, cmd.OutOrStdout())
		},
	}
	addRunFlags(cmd, &opts)
	cmd.AddCommand(newEventCommand(), newCommentCommand(), newFetchFlateCommand())
	cmd.SetVersionTemplate("flux-diff {{.Version}}\n")

	return cmd
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
