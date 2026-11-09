package cli

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

// newAttachCmd builds and returns the tt attach command with associated flags
func newAttachCmd(a *app) *cobra.Command {
	var entryIDPrefix string
	var all bool
	attCmd := &cobra.Command{
		Use:   "attach [selector]",
		Short: "Link a Notion task to a time entry. Task is matched based on the selector provided",
		Long:  "If no --entry ID prefix is provided, defaults to the currently running entry, then the most recent",
		RunE: func(cmd *cobra.Command, args []string) error {
			canPrompt := a.canPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if len(args) == 0 && !canPrompt {
				return &clierr.Error{Code: clierr.Usage, Message: "[selector] argument is required when prompts are disabled (no terminal or --no-input)"}
			}

			// Check if Notion context is connected
			err := requireConnectedContext(cmd.Context(), a.db, store.DefaultContextID)
			if err != nil {
				return err
			}

			// Database lookup (full ID, short ID, order)
			shortIDMap, err := buildShortIDMap(cmd.Context(), a.db)
			if err != nil {
				return err
			}

			var selector string
			if len(args) == 0 {
				// present task picker
				allTasks, err := loadAllTasks(cmd.Context(), a.db, all, shortIDMap, "attach")
				if err != nil {
					return err
				}

				pickedTaskPageID, err := runPickerForm(cmd.Context(), allTasks, cmd.InOrStdin(), cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				selector = pickedTaskPageID
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

			taskSelector, err := buildTaskSelector(selector, all, shortIDMap)
			if err != nil {
				return err
			}
			taskPageID, err := a.db.ResolveTask(cmd.Context(), taskSelector)
			var ambigErr *store.AmbiguousTaskError
			if canPrompt && errors.As(err, &ambigErr) {
				pickedCandidate, err := runPickerForm(cmd.Context(),
					mapDbTasks(ambigErr.Candidates, shortIDMap, false, nil).Tasks,
					cmd.InOrStdin(),
					cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				taskPageID = &pickedCandidate
			} else if err != nil {
				return handleTaskErrors(err, shortIDMap, selector, entryIDPrefix)
			}

			nowUTC := time.Now().UTC()
			entry, err := a.db.AttachTaskToEntry(cmd.Context(), strings.ToLower(entryIDPrefix), *taskPageID, nowUTC)

			if err != nil {
				return handleTaskErrors(err, shortIDMap, selector, entryIDPrefix)
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

// handleTaskErrors takes errors returned from commands that attach tasks to entries, and provides the appropriate context
// for the final CLI error
func handleTaskErrors(err error, shortIDMap map[string]string, taskSelector, entryIDPrefix string) error {
	var ambiguousTaskErr *store.AmbiguousTaskError
	if errors.As(err, &ambiguousTaskErr) {
		listDoc := mapDbTasks(ambiguousTaskErr.Candidates, shortIDMap, false, nil)
		return &clierr.Error{Code: clierr.AmbiguousSelector, Message: "unable to find unique task", Details: map[string]any{"selector": taskSelector, "candidates": listDoc.Tasks}}
	}

	var ambiguousEntryErr *store.AmbiguousEntryError
	if errors.As(err, &ambiguousEntryErr) {
		return ambiguousEntryError(entryIDPrefix, ambiguousEntryErr.Candidates, shortIDMap)
	}

	if errors.Is(err, store.ErrTaskNotFound) {
		return &clierr.Error{Code: clierr.NotFound, Message: store.ErrTaskNotFound.Error(), Details: map[string]any{"selector": taskSelector}}
	}

	return err
}
