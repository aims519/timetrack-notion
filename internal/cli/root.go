package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd returns the root cobra command for the application
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tt",
		Short: "Local-first time tracker that syncs time entries to Notion",
	}

	return rootCmd
}
