package cli

import (
	"context"
	"time"

	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

func newStatusCmd(a *app) *cobra.Command {
	statusCmd := &cobra.Command{
		Use:     "status",
		Example: "",
		Short:   "get the current status of time entries",
		Long:    "",
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}

			nowUTC := time.Now().UTC()
			statusDoc, err := executeStatusUpdates(cmd.Context(), a, nowUTC, loc)
			if err != nil {
				return err
			}

			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, statusDoc, loc)
		},
	}

	return statusCmd
}

func executeStatusUpdates(ctx context.Context, a *app, nowUTC time.Time, loc *time.Location) (output.StatusDoc, error) {
	year, month, day := nowUTC.In(loc).Date()
	localStart := time.Date(year, month, day, 0, 0, 0, 0, loc)
	localEnd := localStart.AddDate(0, 0, 1)

	dbStatus, err := a.db.ReadStatus(ctx, localStart.UTC(), localEnd.UTC())
	if err != nil {
		return output.StatusDoc{}, err
	}

	return mapDBStatus(dbStatus, nowUTC), nil
}
