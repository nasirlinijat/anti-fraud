package labels

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResetStore exposes only the fixed demo reset operation through evaluation credentials.
type ResetStore struct{ pool *pgxpool.Pool }

// NewResetStore verifies the connection target and reset permission before use.
func NewResetStore(ctx context.Context, runtime, evaluation *pgxpool.Pool) (*ResetStore, error) {
	if runtime == nil || evaluation == nil {
		return nil, errors.New("reset requires evaluation access")
	}
	a, b := runtime.Config().ConnConfig, evaluation.Config().ConnConfig
	if a.Host != b.Host || a.Port != b.Port || a.Database != b.Database || !sameFallbackTargets(a.Fallbacks, b.Fallbacks) {
		return nil, errors.New("reset requires matching runtime and evaluation database targets")
	}
	var runtimeSchema, evaluationSchema string
	if err := runtime.QueryRow(ctx, `SELECT current_schema()`).Scan(&runtimeSchema); err != nil {
		return nil, errors.New("verify reset database schema")
	}
	var allowed bool
	err := evaluation.QueryRow(ctx, `SELECT current_schema(), has_function_privilege(current_user, 'reset_demo_data()', 'EXECUTE')`).Scan(&evaluationSchema, &allowed)
	if err != nil || !allowed {
		return nil, errors.New("apply reset migration and evaluation grants")
	}
	if runtimeSchema != evaluationSchema {
		return nil, errors.New("reset requires matching runtime and evaluation schemas")
	}
	return &ResetStore{pool: evaluation}, nil
}

// Reset deletes demonstration state in one transaction.
func (s *ResetStore) Reset(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT reset_demo_data()`)
		return err
	})
}

func sameFallbackTargets(a, b []*pgconn.FallbackConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Host != b[i].Host || a[i].Port != b[i].Port {
			return false
		}
	}
	return true
}
