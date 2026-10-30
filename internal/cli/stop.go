package cli

import (
	"context"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

type stopArgs struct {
	at  string
	ago string
}

func newStopCmd(a *app) *cobra.Command {
	var sa stopArgs
	stopCmd := &cobra.Command{
		Use:     "stop",
		Example: "",
		Short:   "stop the currently running timer",
		Long:    "",
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}

			stopUTC, err := validateStopFlags(sa, time.Now(), loc)
			if err != nil {
				return err
			}

			entryDoc, err := executeStopUpdates(cmd.Context(), a, stopUTC)
			if err != nil {
				return err
			}

			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, entryDoc, loc)
		},
	}

	stopCmd.Flags().StringVar(&sa.at, "at", "", "stop time tracking at: can be today in HH:MM format or RFC3339 (YYYY-MM-DDTHH:mm:ss)")
	stopCmd.Flags().StringVar(&sa.ago, "ago", "", "stop time tracking at a time in the past: expects a duration (e.g. \"1h10m\", \"2h\", \"30m\")")

	return stopCmd
}

// validateStopFlags checks for invalid combinations of flags, required flags that are missing,
// or flags with malformed values. Returns the requested start time of the entry.
func validateStopFlags(s stopArgs, now time.Time, loc *time.Location) (time.Time, error) {
	if s.ago != "" && s.at != "" {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "only one of --ago or --at values are accepted"}
	}

	if s.ago != "" {
		return timeparse.ParseAgo(s.ago, now)
	}

	if s.at != "" {
		return timeparse.Parse(s.at, now, loc)
	}

	return now, nil
}

func executeStopUpdates(ctx context.Context, a *app, stopUTC time.Time) (output.StopDoc, error) {
	dbEntry, err := a.db.StopTimer(ctx, stopUTC)
	if err != nil {
		return output.StopDoc{}, err
	}

	return output.NewStopDoc(mapDBEntry(dbEntry), nil), nil
}
