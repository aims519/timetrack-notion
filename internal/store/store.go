package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const driverName = "sqlite"
const openMaxConnections = 1

//go:embed migrations/*.sql
var embedded embed.FS

type Store struct {
	connectionPool *sql.DB
}

func Open(ctx context.Context, dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "dbPath empty", Details: map[string]any{"path": dbPath}}
	}

	if strings.Contains(dbPath, "?") {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "dbPath contains invalid character ?", Details: map[string]any{"path": dbPath}}
	}

	dsn := buildDSN(dbPath)
	connPool, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.Internal, Message: fmt.Sprintf("dsn or driver name incorrect: %v", err), Cause: err}
	}

	err = connPool.PingContext(ctx)
	if err != nil {
		_ = connPool.Close()
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: fmt.Sprintf("can't open database at %q: %v", dbPath, err), Cause: err, Details: map[string]any{"path": dbPath}}
	}

	connPool.SetMaxOpenConns(openMaxConnections)

	err = applyMigrations(ctx, connPool, dbPath)
	if err != nil {
		_ = connPool.Close()
		return nil, err
	}

	return &Store{connectionPool: connPool}, nil
}

func (s *Store) Close() error {
	err := s.connectionPool.Close()
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to close", Cause: err}
	}
	return nil
}

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
