package platform

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX runs SQL statements. *pgxpool.Pool, *pgx.Conn, and pgx.Tx implement it.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewPool creates a PostgreSQL connection pool. It returns an error only if the
// URL is invalid. Connections are opened in the background, so the service can
// start while the database is unreachable and report that through GET /ready.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if TracingEnabled() {
		config.ConnConfig.Tracer = queryTracer{}
	}
	return pgxpool.NewWithConfig(ctx, config)
}

func DatabaseUnavailable(err error) bool {
	var connectErr *pgconn.ConnectError
	return errors.As(err, &connectErr) || pgconn.SafeToRetry(err)
}
