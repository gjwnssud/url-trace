// Package cmd wires the url-trace command-line interface.
package cmd

import (
	"context"

	"github.com/spf13/cobra"
)

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "url-trace",
		Version: version,
		Short:   "Collect URLs from traffic and crawls to build whitelist policies",
		Long: "url-trace extracts the URLs an application actually uses and " +
			"aggregates them into audit-friendly records for whitelist policy review.",
		// Let main.go be the single place that prints the error, so it is not
		// reported twice.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newExtractCmd(), newExportCmd(), newDiffCmd())
	return root
}

// Execute runs the root command with the given context and returns its error.
func Execute(ctx context.Context, version string) error {
	return newRootCmd(version).ExecuteContext(ctx)
}
