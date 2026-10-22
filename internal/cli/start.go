package cli

import (
	"fmt"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
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

var sa startArgs

func newStartCmd(a *app) *cobra.Command {
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

			if err := validateFlags(sa, time.Now()); err != nil {
				return err
			}

			fmt.Printf("description: %s\n", sa.description)
			fmt.Printf("task: %s\n", sa.task)
			fmt.Printf("switch: %v\n", sa.switchTask)
			fmt.Printf("pick: %v\n", sa.pickTask)
			return nil
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
// or flags with malformed values
func validateFlags(s startArgs, now time.Time) error {
	if s.ago != "" && s.at != "" {
		return &clierr.Error{Code: clierr.Usage, Message: "only one of --ago or --at values are accepted"}
	}

	if s.task == "" && s.description == "" {
		return &clierr.Error{Code: clierr.Usage, Message: "at least one of [description] or --task must be provided"}
	}

	if s.ago != "" {
		_, err := timeparse.ParseAgo(s.ago, now)
		if err != nil {
			return err
		}
	}

	loc, err := timeparse.CurrentLocation()
	if err != nil {
		return err
	}
	if s.at != "" {
		_, err := timeparse.Parse(s.at, now, loc)
		if err != nil {
			return err
		}
	}

	return nil
}
