// Package labels stores evaluation answers outside the scoring boundary.
package labels

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"strings"
	"time"
)

type Label struct {
	TransactionID, RunID, Source string
	Scenario                     *string
	Fraud                        bool
	RequestJSON                  []byte
}

func (l Label) Validate() error {
	if l.TransactionID == "" || l.RunID == "" {
		return errors.New("transaction and run IDs are required")
	}
	if l.Source == "simulator" && l.Scenario != nil && *l.Scenario != "" {
		return nil
	}
	if l.Source == "ieee-cis" && l.Scenario == nil {
		return nil
	}
	return errors.New("invalid label source or scenario")
}
func same(ctx context.Context, c platform.DBTX, table string, l Label) error {
	var matches bool
	// table is selected only by the two private callers below, never user input.
	err := c.QueryRow(ctx, `SELECT ROW(run_id,source,scenario,is_fraud) IS NOT DISTINCT FROM ROW($2::text,$3::text,$4::text,$5::boolean) FROM `+table+` WHERE transaction_id=$1`, l.TransactionID, l.RunID, l.Source, l.Scenario, l.Fraud).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("conflicting label for transaction %s", l.TransactionID)
	}
	return nil
}
func Prepare(ctx context.Context, c platform.DBTX, l Label) error {
	if len(l.RequestJSON) == 0 {
		return errors.New("pending label requires a purchase snapshot")
	}
	if err := l.Validate(); err != nil {
		return err
	}
	_, err := c.Exec(ctx, `INSERT INTO pending_purchase_labels(transaction_id,run_id,source,scenario,is_fraud,request_json) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(transaction_id) DO NOTHING`, l.TransactionID, l.RunID, l.Source, l.Scenario, l.Fraud, l.RequestJSON)
	if err != nil {
		return err
	}
	if err := same(ctx, c, "pending_purchase_labels", l); err != nil {
		return err
	}
	var matches bool
	if err := c.QueryRow(ctx, `SELECT request_json IS NOT DISTINCT FROM $2::jsonb AND rejection_reason IS NULL FROM pending_purchase_labels WHERE transaction_id=$1`, l.TransactionID, l.RequestJSON).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return ErrPurchaseMismatch
	}
	return nil
}

// Put inserts only for a saved decision. Existing truth is immutable.
func Put(ctx context.Context, c platform.DBTX, l Label) error {
	if err := l.Validate(); err != nil {
		return err
	}
	var exists bool
	if err := c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purchases p JOIN decisions d ON d.purchase_id=p.id WHERE p.transaction_id=$1)`, l.TransactionID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("transaction %s has no saved decision", l.TransactionID)
	}
	_, err := c.Exec(ctx, `INSERT INTO purchase_labels(transaction_id,run_id,source,scenario,is_fraud) VALUES($1,$2,$3,$4,$5) ON CONFLICT(transaction_id) DO NOTHING`, l.TransactionID, l.RunID, l.Source, l.Scenario, l.Fraud)
	if err != nil {
		return err
	}
	return same(ctx, c, "purchase_labels", l)
}
func Finalize(ctx context.Context, pool *pgxpool.Pool, l Label, checkpoint func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if len(l.RequestJSON) > 0 {
			if err := verifyPurchase(ctx, tx, l); err != nil {
				return err
			}
		}
		if err := Put(ctx, tx, l); err != nil {
			return err
		}
		if checkpoint != nil {
			if err := checkpoint(tx); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM pending_purchase_labels WHERE transaction_id=$1`, l.TransactionID)
		return err
	})
}

// RecoverTx finalizes saved pending purchases without sending any score requests.
func RecoverTx(ctx context.Context, tx pgx.Tx, run string) (int, error) {
	rows, err := tx.Query(ctx, `SELECT l.transaction_id,l.run_id,l.source,l.scenario,l.is_fraud,l.request_json FROM pending_purchase_labels l JOIN purchases p USING(transaction_id) JOIN decisions d ON d.purchase_id=p.id WHERE ($1='' OR l.run_id=$1) AND l.rejection_reason IS NULL AND l.request_json IS NOT NULL`, run)
	if err != nil {
		return 0, err
	}
	var pending []Label
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.TransactionID, &l.RunID, &l.Source, &l.Scenario, &l.Fraud, &l.RequestJSON); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, l := range pending {
		if err := verifyPurchase(ctx, tx, l); err != nil {
			if errors.Is(err, ErrPurchaseMismatch) {
				if _, e := tx.Exec(ctx, `UPDATE pending_purchase_labels SET rejection_reason='purchase snapshot mismatch' WHERE transaction_id=$1`, l.TransactionID); e != nil {
					return 0, e
				}
				continue
			}
			return 0, err
		}
		if err := Put(ctx, tx, l); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM pending_purchase_labels WHERE transaction_id=$1`, l.TransactionID); err != nil {
			return 0, err
		}
		recovered++
	}
	return recovered, nil
}
func Recover(ctx context.Context, pool *pgxpool.Pool, run string) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { var err error; n, err = RecoverTx(ctx, tx, run); return err })
	return n, err
}
func Preflight(ctx context.Context, pool *pgxpool.Pool) error {
	var ok bool
	err := pool.QueryRow(ctx, `SELECT has_table_privilege(current_user,'purchase_labels','SELECT') AND has_table_privilege(current_user,'purchase_labels','INSERT') AND has_table_privilege(current_user,'pending_purchase_labels','SELECT') AND has_table_privilege(current_user,'pending_purchase_labels','INSERT') AND has_table_privilege(current_user,'pending_purchase_labels','DELETE') AND NOT has_table_privilege(current_user,'purchases','INSERT')`).Scan(&ok)
	if err != nil || !ok {
		return errors.New("configure LABELS_DATABASE_URL with the restricted evaluation role and apply migrations/grants")
	}
	return nil
}

// Scorer observes private purchase metadata while the client sends only Request.
type Scorer struct {
	Pool   *pgxpool.Pool
	Client simulator.Scorer
	RunID  string
}

func (s *Scorer) Score(context.Context, simulator.Request) (simulator.Result, error) {
	return simulator.Result{}, errors.New("purchase metadata required")
}
func (s *Scorer) ScorePurchase(ctx context.Context, p simulator.Purchase) (simulator.Result, error) {
	l := FromPurchase(s.RunID, p)
	if err := Prepare(ctx, s.Pool, l); err != nil {
		return simulator.Result{}, simulator.FatalError{Err: err}
	}
	// Cancellation stops scheduling; a dispatched purchase and its label can settle.
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	result, err := s.Client.Score(call, p.Request)
	if err != nil {
		Reject(call, s.Pool, l.TransactionID, err)
		return result, simulator.FatalError{Err: err}
	}
	if err := Finalize(call, s.Pool, l, nil); err != nil {
		return result, simulator.FatalError{Err: err}
	}
	return result, nil
}

// SendAccountEvent forwards an account event to the client. Account events
// carry no labels, so nothing is recorded for them. A client that cannot send
// account events skips them.
func (s *Scorer) SendAccountEvent(ctx context.Context, e simulator.AccountEvent) error {
	accounts, ok := s.Client.(simulator.AccountEventSender)
	if !ok {
		return nil
	}
	return accounts.SendAccountEvent(ctx, e)
}
func FromPurchase(run string, p simulator.Purchase) Label {
	scenario := string(p.Pattern)
	return Label{TransactionID: strings.TrimSpace(p.Request.TransactionID), RunID: run, Source: "simulator", Scenario: &scenario, Fraud: p.Pattern.Fraud(), RequestJSON: requestSnapshot(p.Request)}
}
