package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/taskcache"
	"github.com/aims519/timetrack-notion/internal/timeparse"
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
			if len(args) != 1 {
				return &clierr.Error{Code: clierr.Usage, Message: "invalid command arguments, run 'attach [selector]'"}
			}
			selector := args[0]
			if selector == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "[selector] cannot be empty"}
			}

			if cmd.Flag("entry").Changed && len(entryIDPrefix) < 7 {
				return &clierr.Error{Code: clierr.Usage, Message: "--entry ID cannot be empty and must be at least 7 characters long"}
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

			nowUTC := time.Now().UTC()
			entry, err := a.db.AttachTaskToEntry(cmd.Context(), entryIDPrefix, order, taskID, all, nowUTC)
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

	attCmd.Flags().StringVar(&entryIDPrefix, "entry", "", "specify an entry to attach a task to by ID prefix (min 7 characters)")
	attCmd.Flags().BoolVar(&all, "all", false, "allow attaching tasks already marked 'done'")

	return attCmd
}

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
