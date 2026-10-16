package cli

import (
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/spf13/cobra"
)

// NewRootCmd returns the root cobra command for the application
func NewRootCmd() *cobra.Command {
	format := output.Table // default

	rootCmd := &cobra.Command{
		Use:   "tt",
		Short: "Local-first time tracker that syncs time entries to Notion",
	}

	rootCmd.PersistentFlags().Var(&format, "output", "output format: table or json")

	return rootCmd
}
