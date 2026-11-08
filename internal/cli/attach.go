package cli

import (
	"fmt"
	"strconv"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/taskcache"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

func newAttachCmd(a *app) *cobra.Command {
	var entryID string
	attCmd := &cobra.Command{
		Use:   "attach [selector]",
		Short: "Link a Notion task to a time entry. Task is matched based on the selector provided",
		Long:  "If no --entry ID is provided, defaults to the currently running entry",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return &clierr.Error{Code: clierr.Usage, Message: "invalid command arguments, run 'attach [selector]'"}
			}
			selector := args[0]
			if selector == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "[selector] cannot be empty"}
			}

			if cmd.Flag("entry").Changed && entryID == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "--entry ID cannot be empty"}
			}

			// Database lookup (full ID, short ID, order)
			allTaskIDs, err := a.db.AllTaskIDs(cmd.Context())
			if err != nil {
				return err
			}

			shortIDMap, err := taskcache.ShortIDs(allTaskIDs)
			if err != nil {
				return err
			}

			order, taskID, found := resolveTaskLookupID(selector, shortIDMap)
			if !found {
				return &clierr.Error{Code: clierr.NotFound, Message: fmt.Sprintf("unable to find matching task for selector, %q", selector), Details: map[string]any{"selector": selector}}
			}

			entry, err := a.db.AttachTaskToEntry(cmd.Context(), order, taskID)
			if err != nil {
				return err
			}

			doc := output.NewEntryDoc(mapDBEntryWithShortTask(entry, shortIDMap), nil)
			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}
			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, doc, loc)
		},
	}

	attCmd.Flags().StringVar(&entryID, "entry", "", "specify an entry to attach a task to")

	return attCmd
}

func resolveTaskLookupID(selector string, shortIDMap map[string]string) (order *float64, taskID *string, match bool) {
	// Is selector a number?
	flt, err := strconv.ParseFloat(selector, 64)
	if err == nil {
		return &flt, nil, true
	}

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

	return nil, nil, false
}
