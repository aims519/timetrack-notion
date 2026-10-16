package cli

import (
	"context"
	"io"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/spf13/cobra"
)

// newRootCmd returns the root cobra command for the application
func newRootCmd(f *output.Format) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "tt",
		Short:         "Local-first time tracker that syncs time entries to Notion",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.PersistentFlags().Var(f, "output", "output format: table or json")

	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &clierr.Error{Code: clierr.Usage, Message: err.Error(), Cause: err}
	})

	return rootCmd
}

// Run builds the root command with args and executes it, writing output to stdout or stderr
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	format := output.Table // default

	root := newRootCmd(&format)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err != nil {
		printError(stderr, format, err)
		return exitCode(err)
	}
	return 0 // success
}
