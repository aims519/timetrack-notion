package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/aims519/timetrack-notion/internal/clierr"
	_ "modernc.org/sqlite"
)

const driverName = "sqlite"
const openMaxConnections = 1

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
		return nil, &clierr.Error{Code: clierr.Internal, Message: "dsn or driver name incorrect", Cause: err}
	}

	err = connPool.PingContext(ctx)
	if err != nil {
		_ = connPool.Close()
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "db PingContext() failed", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	connPool.SetMaxOpenConns(openMaxConnections)

	return &Store{connectionPool: connPool}, nil
}

func (s *Store) Close() error {
	err := s.connectionPool.Close()
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to close", Cause: err}
	}
	return nil
}
