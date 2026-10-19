package store

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedded embed.FS

func applyMigrations(ctx context.Context, connPool *sql.DB, dbPath string) error {
	fsys, err := fs.Sub(embedded, "migrations")
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "embedded migrations aren't accessible", Cause: err, Details: map[string]any{"embedded_folder": "migrations"}}
	}

	p, err := goose.NewProvider(
		goose.DialectSQLite3, connPool, fsys,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "goose provider not created", Cause: err}
	}

	current, latest, err := p.GetVersions(ctx)
	if err != nil {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to verify database version", Cause: err, Details: map[string]any{"path": dbPath}}
	}
	if current > latest {
		return &clierr.Error{Code: clierr.DatabaseTooNew, Message: "detected version newer than target version", Details: map[string]any{"database_version": current, "supported_version": latest}}
	}

	_, err = p.Up(ctx)
	if err != nil {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "migrations couldn't be applied", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	return nil
}
