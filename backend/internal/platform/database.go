package platform

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a PostgreSQL connection pool. It returns an error only if the
// URL is invalid. Connections are opened in the background, so the service can
// start while the database is unreachable and report that through GET /ready.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, databaseURL)
}
