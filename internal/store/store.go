package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/aims519/timetrack-notion/internal/clierr"
	_ "modernc.org/sqlite"
)

const driverName = "sqlite"
const openMaxConnections = 1

type Store struct {
	connectionPool *sql.DB
	dbPath         string
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

	return &Store{connectionPool: connPool, dbPath: dbPath}, nil
}

func (s *Store) Close() error {
	err := s.connectionPool.Close()
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to close", Cause: err}
	}
	return nil
}
