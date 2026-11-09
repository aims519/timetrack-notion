package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

type startArgs struct {
	taskSelector string
	description  *string
	pickTask     bool
	switchTask   bool
	at           string
	ago          string
	allTasks     bool
}

// newStartCmd builds and returns the tt start command with associated flags
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
				sa.description = &args[0]
			}

			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}

			canPrompt := a.canPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			startUTC, err := validateStartFlags(sa, time.Now(), loc, canPrompt)
			if err != nil {
				return err
			}

			// Check if Notion context is connected
			if sa.pickTask || sa.taskSelector != "" {
				err = requireConnectedContext(cmd.Context(), a.db, store.DefaultContextID)
				if err != nil {
					return err
				}
			}

			shortIDMap, err := buildShortIDMap(cmd.Context(), a.db)
			if err != nil {
				return err
			}

			taskPageID, err := resolveStartTaskPageID(cmd.Context(), a.db, sa, shortIDMap, cmd.InOrStdin(), cmd.ErrOrStderr(), canPrompt)
			if err != nil {
				return handleTaskErrors(err, shortIDMap, sa.taskSelector, "")
			}

			startDoc, err := executeStartUpdates(cmd.Context(), a, sa, startUTC, shortIDMap, taskPageID)
			if err != nil {
				return err
			}

			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, startDoc, loc)
		},
	}

	startCmd.Flags().BoolVar(&sa.switchTask, "switch", false, "stop a previously running time tracking entry and start a new one")
	startCmd.Flags().StringVarP(&sa.taskSelector, "task", "t", "", "task selector: can be a full page ID, short ID, or task order number")
	startCmd.Flags().BoolVar(&sa.pickTask, "pick", false, "open a picker to choose a task")
	startCmd.Flags().StringVar(&sa.at, "at", "", "start time tracking at: can be today in HH:MM format or RFC3339 (YYYY-MM-DDTHH:mm:ss)")
	startCmd.Flags().StringVar(&sa.ago, "ago", "", "start time tracking from a time in the past: expects a duration (e.g. \"1h10m\", \"2h\", \"30m\")")
	startCmd.Flags().BoolVar(&sa.allTasks, "all", false, "include tasks already marked 'done'")

	return startCmd
}

// validateStartFlags checks for invalid combinations of flags, required flags that are missing,
// or flags with malformed values. Returns the requested start time of the entry.
func validateStartFlags(s startArgs, now time.Time, loc *time.Location, canPrompt bool) (time.Time, error) {
	if s.ago != "" && s.at != "" {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "only one of --ago or --at values are accepted"}
	}

	if (s.taskSelector == "" && !s.pickTask) && s.description == nil {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "at least one of [description] or --task/--pick must be provided"}
	}

	if s.taskSelector != "" && s.pickTask {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "either --task or --pick can be set, not both"}
	}

	if s.pickTask && !canPrompt {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "--pick is not allowed when prompts are disabled (no terminal or --no-input)"}
	}

	if s.description != nil && *s.description == "" {
		return time.Time{}, &clierr.Error{Code: clierr.Usage, Message: "[description] must not be empty if provided"}
	}

	if s.ago != "" {
		return timeparse.ParseAgo(s.ago, now)
	}

	if s.at != "" {
		return timeparse.Parse(s.at, now, loc)
	}

	return now, nil
}

func buildTaskSelector(selector string, all bool, shortIDMap map[string]string) (store.TaskSelector, error) {
	task := store.TaskSelector{All: all}
	if selector != "" {
		order, pageID, found := resolveTaskLookupID(selector, shortIDMap)
		if !found {
			return store.TaskSelector{}, &clierr.Error{Code: clierr.NotFound, Message: fmt.Sprintf("unable to find matching task for selector, %q", selector), Details: map[string]any{"selector": selector}}
		}
		task.Order = order
		task.PageID = pageID
	}

	return task, nil
}

func executeStartUpdates(ctx context.Context, a *app, s startArgs, startUTC time.Time, shortIDMap map[string]string, taskPageID *string) (output.StartDoc, error) {
	if s.switchTask {
		startedEntry, stoppedEntry, err := a.db.SwitchTimer(ctx, startUTC, taskPageID, s.description)
		if err != nil {
			return output.StartDoc{}, err
		}

		return output.NewStartDoc(mapDBEntry(startedEntry, shortIDMap),
			mapNullableDBEntry(stoppedEntry, shortIDMap), nil), nil
	}

	dbEntry, err := a.db.StartNewTimer(ctx, startUTC, taskPageID, s.description)
	if err != nil {
		return output.StartDoc{}, handleTaskErrors(err, shortIDMap, s.taskSelector, "")
	}

	return output.NewStartDoc(mapDBEntry(dbEntry, shortIDMap),
		nil, nil), nil
}

// resolveStartTaskPageID either presents the user with a picker form, or tries to match the given task selector to
// a task pageID
func resolveStartTaskPageID(ctx context.Context, s *store.Store, args startArgs, shortIDMap map[string]string, stdIn io.Reader, stdErr io.Writer, canPrompt bool) (*string, error) {
	if args.pickTask {
		allTasks, err := loadAllTasks(ctx, s, args.allTasks, shortIDMap, "start")
		if err != nil {
			return nil, err
		}

		selectedPageID, err := runPickerForm(ctx, allTasks, stdIn, stdErr)
		return &selectedPageID, err
	}

	selector, err := buildTaskSelector(args.taskSelector, args.allTasks, shortIDMap)
	if err != nil {
		return nil, err
	}

	// if the task fails to resolve with an ambiguous task error, present the user
	// with a picker to choose between candidates. Only if prompting is allowed
	taskPageID, err := s.ResolveTask(ctx, selector)
	var ambigErr *store.AmbiguousTaskError
	if canPrompt && errors.As(err, &ambigErr) {
		pickedCandidate, err := runPickerForm(ctx,
			mapDbTasks(ambigErr.Candidates, shortIDMap, false, nil).Tasks,
			stdIn,
			stdErr)
		if err != nil {
			return nil, err
		}
		taskPageID = &pickedCandidate
	} else if err != nil {
		return nil, err
	}

	return taskPageID, nil
}
