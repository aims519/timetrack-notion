package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/spf13/cobra"
)

// newRootCmd returns the root cobra command for the application
func newRootCmd(a *app) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "tt",
		Short:         "Local-first time tracker that syncs time entries to Notion",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := dbFilePath()
			if err != nil {
				return err
			}

			db, err := store.Open(cmd.Context(), dbPath)
			if err != nil {
				return err
			}

			a.db = db

			return nil
		},
	}

	rootCmd.PersistentFlags().Var(a.format, "output", "output format: table or json")

	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &clierr.Error{Code: clierr.Usage, Message: err.Error(), Cause: err}
	})

	rootCmd.AddCommand(newStartCmd(a))

	return rootCmd
}

// app holds a pointer to the db connection pool so that Run can safely close it
type app struct {
	db     *store.Store
	format *output.Format
}

// Run builds the root command with args and executes it, writing output to stdout or stderr
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	format := output.Table // default
	a := app{format: &format}

	root := newRootCmd(&a)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)

	var closeErr error
	// Close store if open
	if a.db != nil {
		closeErr = a.db.Close()
	}

	if err != nil {
		printError(stderr, format, err)
		return exitCode(err)
	}

	if closeErr != nil {
		printError(stderr, format, closeErr)
		return exitCode(closeErr)
	}

	return 0 // success
}

func dbFilePathPerOS(goos string) (string, error) {
	// Check for TT_DB override
	val, exists := os.LookupEnv("TT_DB")
	if exists {
		return val, nil
	}

	// target data directory
	var ttDir string

	// Check for XDG_DATA_HOME
	val = os.Getenv("XDG_DATA_HOME")
	if filepath.IsAbs(val) {
		ttDir = filepath.Join(val, "tt")

		err := os.MkdirAll(ttDir, 0o700) // user read+write+execute
		if err != nil {
			return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to create default data directory", Cause: err, Details: map[string]any{"path": ttDir}}
		}

		return filepath.Join(ttDir, "tt.db"), nil
	}

	// Fallback per OS
	if goos == "windows" {
		val = os.Getenv("LOCALAPPDATA")
		ttDir = filepath.Join(val, "tt")
		if !filepath.IsAbs(val) {
			return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "LOCALAPPDATA is not set to an absolute path"}
		}
	} else {
		homeDir, err := os.UserHomeDir()
		ttDir = filepath.Join(homeDir, ".local", "share", "tt")
		if err != nil || !filepath.IsAbs(homeDir) {
			return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to get user's home directory as an absolute path", Cause: err, Details: map[string]any{"path": ttDir}}
		}
	}

	err := os.MkdirAll(ttDir, 0o700) // user read+write+execute
	if err != nil {
		return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to create default data directory", Cause: err, Details: map[string]any{"path": ttDir}}
	}

	return filepath.Join(ttDir, "tt.db"), nil
}

// dbFilePath checks for an environmental override, otherwise returns the default database name in the
// application's data directory
func dbFilePath() (string, error) {
	return dbFilePathPerOS(runtime.GOOS)
}
