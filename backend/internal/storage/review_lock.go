package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The single-bigint advisory namespace is reserved for review purchase IDs;
// ruleset and simulation locks use the separate two-integer namespace.
const lockReview = `SELECT pg_advisory_xact_lock($1::bigint)`

// beginReview acquires the lock before any state query, so the next statement
// gets a fresh snapshot after a competing claim, release, or decision commits.
func (s *ReviewStore) beginReview(ctx context.Context, purchaseID int64) (pgx.Tx, error) {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin review transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, lockReview, purchaseID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("lock review %d: %w", purchaseID, err)
	}
	return tx, nil
}
