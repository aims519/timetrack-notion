package cli

import (
	"context"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

type startArgs struct {
	task        string
	description string
	pickTask    bool
	switchTask  bool
	at          string
	ago         string
}

func newStartCmd(a *app) *cobra.Command {
	var sa startArgs

	startCmd := &cobra.Command{
		Use:     "start [description]",
		Example: "",
		Short:   "start tracking time",
		Long:    "",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				sa.description = args[0]
			}

			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}

			startUTC, err := validateFlags(sa, time.Now(), loc)
			if err != nil {
				return err
			}

			err = rejectUnimplemented(sa)
			if err != nil {
				return err
			}

			startDoc, err := executeUpdates(cmd.Context(), a, sa, startUTC)
			if err != nil {
				return err
			}

			return output.Write(cmd.OutOrStdout(), *a.format, startDoc, loc)
		},
	}

	startCmd.Flags().BoolVar(&sa.switchTask, "switch", false, "stop a previously running time tracking entry and start a new one")
	startCmd.Flags().StringVarP(&sa.task, "task", "t", "", "task selector: can be a full page ID, short ID, or task order number")
	startCmd.Flags().BoolVar(&sa.pickTask, "pick", false, "open a picker to choose a task")
	startCmd.Flags().StringVar(&sa.at, "at", "", "start time tracking at: can be today in HH:MM format or RFC3339 (YYYY-MM-DDTHH:mm:ss)")
	startCmd.Flags().StringVar(&sa.ago, "ago", "", "start time tracking from a time in the past: expects a duration (e.g. \"1h10m\", \"2h\", \"30m\")")

	return startCmd
}

// validateFlags checks for invalid combinations of flags, required flags that are missing,
// or flags with malformed values. Returns the requested start time of the entry.
func validateFlags(s startArgs, now time.Time, loc *time.Location) (time.Time, error) {
	if s.ago != "" && s.at != "" {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "only one of --ago or --at values are accepted"}
	}

	if (s.task == "" && !s.pickTask) && s.description == "" {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "at least one of [description] or --task/--pick must be provided"}
	}

	if s.task != "" && s.pickTask {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "either --task or --pick can be set, not both"}
	}

	if s.ago != "" {
		return timeparse.ParseAgo(s.ago, now)
	}

	if s.at != "" {
		return timeparse.Parse(s.at, now, loc)
	}

	return now, nil
}

// rejectUnimplemented is a temporary measure until all start flags are implemented
func rejectUnimplemented(sa startArgs) error {
	if sa.pickTask {
		return &clierr.Error{Code: clierr.Usage, Message: "--pick is not supported yet"}
	}

	if sa.switchTask {
		return &clierr.Error{Code: clierr.Usage, Message: "--switch is not supported yet"}
	}

	if sa.task != "" {
		return &clierr.Error{Code: clierr.Usage, Message: "--task is not supported yet"}
	}

	return nil
}

func executeUpdates(ctx context.Context, a *app, s startArgs, startUTC time.Time) (output.StartDoc, error) {
	//TODO switch timer

	dbEntry, err := a.db.StartNewTimer(ctx, startUTC, s.description)
	if err != nil {
		return output.StartDoc{}, err
	}

	return output.NewStartDoc(mapDBEntry(dbEntry),
		nil, nil), nil
}
