package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

func newAttachCmd(a *app) *cobra.Command {
	var entryIDPrefix string
	var all bool
	attCmd := &cobra.Command{
		Use:   "attach [selector]",
		Short: "Link a Notion task to a time entry. Task is matched based on the selector provided",
		Long:  "If no --entry ID prefix is provided, defaults to the currently running entry, then the most recent",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !a.canPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()) {
				return &clierr.Error{Code: clierr.Usage, Message: "[selector] argument is required when prompts are disabled (no terminal or --no-input)"}
			}

			// present task picker
			var selector string
			if len(args) == 0 {
				allTasks, err := taskListForPicker(cmd.Context(), a.db, all)
				if err != nil {
					return err
				}

				if len(allTasks) == 0 {
					message := "no tasks available to attach, try running 'tt task refresh'"
					if !all {
						message += ". Or use --all in 'tt attach' to include completed tasks"
					}
					return &clierr.Error{Code: clierr.NotFound, Message: message}
				}

				pickerForm := taskPickerForm(allTasks, &selector).
					WithInput(cmd.InOrStdin()).
					WithOutput(cmd.ErrOrStderr())

				err = pickerForm.RunWithContext(cmd.Context())
				if errors.Is(err, context.Canceled) || errors.Is(err, huh.ErrUserAborted) {
					return &clierr.Error{Code: clierr.Cancelled, Message: "task picker cancelled", Cause: err}
				}
				if err != nil {
					return &clierr.Error{Code: clierr.Internal, Message: "error running task picker form", Cause: err}
				}
			} else if len(args) == 1 {
				selector = args[0]
			} else {
				return &clierr.Error{Code: clierr.Usage, Message: "invalid command arguments, run 'attach [selector]'"}
			}

			if selector == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "[selector] cannot be empty"}
			}

			if cmd.Flag("entry").Changed && len(entryIDPrefix) < 7 {
				return &clierr.Error{Code: clierr.Usage, Message: "--entry ID cannot be empty and must be at least 7 characters long"}
			}

			// Database lookup (full ID, short ID, order)
			shortIDMap, err := buildShortIDMap(cmd.Context(), a.db)
			if err != nil {
				return err
			}

			order, taskID, found := resolveTaskLookupID(selector, shortIDMap)
			if !found {
				return &clierr.Error{Code: clierr.NotFound, Message: fmt.Sprintf("unable to find matching task for selector, %q", selector), Details: map[string]any{"selector": selector}}
			}

			nowUTC := time.Now().UTC()
			entry, err := a.db.AttachTaskToEntry(cmd.Context(), strings.ToLower(entryIDPrefix), order, taskID, all, nowUTC)

			var ambiguousTaskErr *store.AmbiguousTaskError
			if errors.As(err, &ambiguousTaskErr) {
				listDoc := mapDbTasks(ambiguousTaskErr.Candidates, shortIDMap, false, nil)
				return &clierr.Error{Code: clierr.AmbiguousSelector, Message: "unable to find unique task", Details: map[string]any{"selector": selector, "candidates": listDoc.Tasks}}
			}

			var ambiguousEntryErr *store.AmbiguousEntryError
			if errors.As(err, &ambiguousEntryErr) {
				return ambiguousEntryError(entryIDPrefix, ambiguousEntryErr.Candidates, shortIDMap)
			}

			if errors.Is(err, store.ErrTaskNotFound) {
				return &clierr.Error{Code: clierr.NotFound, Message: store.ErrTaskNotFound.Error(), Details: map[string]any{"selector": selector}}
			}
			if err != nil {
				return err
			}

			doc := output.NewAttachDoc(mapDBEntry(entry, shortIDMap), nil)
			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}
			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, doc, loc)
		},
	}

	attCmd.Flags().StringVar(&entryIDPrefix, "entry", "", "specify an entry to attach a task to by ID prefix (min 7 characters)")
	attCmd.Flags().BoolVar(&all, "all", false, "allow attaching tasks already marked 'done'")

	return attCmd
}

// ambiguousEntryError maps the store entry objects returned in its error into the output format
func ambiguousEntryError(selector string, candidates []store.Entry, shortIDMap map[string]string) *clierr.Error {
	var mapped []output.Entry
	for _, c := range candidates {
		mapped = append(mapped, mapDBEntry(c, shortIDMap))
	}
	return &clierr.Error{Code: clierr.AmbiguousSelector, Message: "unable to find unique entry using selector", Details: map[string]any{"selector": selector, "candidates": mapped}}
}

// resolveTaskLookupID takes the selector argument passed in by the user and works out what kind of selector it is.
// The checks are made in order: pageID -> shortID -> order
func resolveTaskLookupID(selector string, shortIDMap map[string]string) (order *float64, taskID *string, match bool) {
	// Does selector match longID?
	if _, found := shortIDMap[selector]; found {
		return nil, &selector, true
	}

	// Does selector match short ID?
	for long, short := range shortIDMap {
		if selector == short {
			return nil, &long, true
		}
	}

	// Is selector a number?
	flt, err := strconv.ParseFloat(selector, 64)
	if err == nil {
		return &flt, nil, true
	}

	return nil, nil, false
}

// taskPickerForm returns a picker form that is triggered if the user
// doesn't pass in a selector argument to the attach command
func taskPickerForm(tasks []output.TaskItem, targetTaskID *string) *huh.Form {
	var taskOptions []huh.Option[string]

	longestOrder := 0
	longestShortID := 0
	for _, t := range tasks {
		if t.Order != nil {
			if orderLen := len(strconv.FormatFloat(*t.Order, 'f', -1, 64)); orderLen > longestOrder {
				longestOrder = orderLen
			}
		}

		if shortIDLen := len(t.ShortID); shortIDLen > longestShortID {
			longestShortID = shortIDLen
		}
	}

	for _, t := range tasks {
		var orderStr string
		if t.Order != nil {
			orderStr = strconv.FormatFloat(*t.Order, 'f', -1, 64)
		} else {
			orderStr = "-"
		}

		label := fmt.Sprintf("%-*s  %-*s  %s", longestOrder, orderStr, longestShortID, t.ShortID, t.Name)
		option := huh.NewOption(label, t.PageID)
		taskOptions = append(taskOptions, option)
	}

	keyMap := huh.NewDefaultKeyMap()
	keyMap.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"))

	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose a task to attach. Esc to cancel").
				Options(taskOptions...).
				Value(targetTaskID),
		),
	).WithKeyMap(keyMap)
}

// taskListForPicker retrieves the list of available tasks to be passed to the picker form
func taskListForPicker(ctx context.Context, s *store.Store, all bool) ([]output.TaskItem, error) {
	// Fetch all tasks
	tasks, _, err := s.ListTasks(ctx, store.NoLimit, all, nil)
	if err != nil {
		return nil, err
	}

	// Fetch short ID mappings for all tasks
	shortIDMap, err := buildShortIDMap(ctx, s)
	if err != nil {
		return nil, err
	}

	doc := mapDbTasks(tasks, shortIDMap, false, nil)

	return doc.Tasks, nil
}
