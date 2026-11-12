package stats

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func qualityFixture(t *testing.T) (*pgxpool.Pool, *QualityStore, string) {
	t.Helper()
	owner, writer := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_LABELS_DATABASE_URL")
	if owner == "" || writer == "" {
		t.Skip("owner and evaluation database URLs required")
	}
	a, err := pgxpool.New(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	b, err := pgxpool.New(t.Context(), writer)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	run := "af75-" + t.Name()
	t.Cleanup(func() {
		defer a.Close()
		defer b.Close()
		for _, sql := range []string{
			`DELETE FROM pending_purchase_labels WHERE run_id=$1`,
			`DELETE FROM purchase_labels WHERE run_id=$1`,
			`DELETE FROM review_resolutions WHERE purchase_id IN (SELECT id FROM purchases WHERE player_id=$1)`,
			`DELETE FROM decisions WHERE purchase_id IN (SELECT id FROM purchases WHERE player_id=$1)`,
			`DELETE FROM purchases WHERE player_id=$1`,
			`DELETE FROM simulation_runs WHERE id=$1`,
		} {
			if _, err := a.Exec(context.Background(), sql, run); err != nil {
				t.Error(err)
			}
		}
	})
	return a, NewQualityStore(b), run
}

type qualityRow struct {
	id, source, outcome string
	fraud               bool
	amount              int64
}

func qualityPurchase(t *testing.T, a *pgxpool.Pool, run string, row qualityRow) {
	t.Helper()
	_, err := a.Exec(t.Context(), `WITH p AS (
 INSERT INTO purchases(transaction_id,event_time,amount_minor,player_id,card_token) VALUES($1,now(),$2,$3,$1) RETURNING id
 ),d AS (INSERT INTO decisions(purchase_id,ruleset_version,outcome,rule_score,fired_rules,model_inputs)
 SELECT id,1,$4,0,'[]','{}' FROM p)
 INSERT INTO purchase_labels(transaction_id,run_id,source,scenario,is_fraud)
 VALUES($1,$3,$5,CASE WHEN $5='simulator' THEN 'fixture' END,$6)`, row.id, row.amount, run, row.outcome, row.source, row.fraud)
	if err != nil {
		t.Fatal(err)
	}
}

func TestQualityHandCalculatedMetricsAndSourceIsolation(t *testing.T) {
	a, s, run := qualityFixture(t)
	for i, row := range []struct {
		outcome string
		fraud   bool
		amount  int64
	}{
		{"review", true, 100}, {"block", true, 200}, {"allow", true, 400},
		{"review", false, 800}, {"allow", false, 1000}, {"allow", false, 2000},
	} {
		qualityPurchase(t, a, run, qualityRow{run + string(rune('a'+i)), "simulator", row.outcome, row.fraud, row.amount})
	}
	qualityPurchase(t, a, run, qualityRow{run + "ieee", "ieee-cis", "allow", true, 9999})
	qualityPurchase(t, a, run, qualityRow{run + "other", "simulator", "block", true, 9000})
	if _, err := a.Exec(t.Context(), `UPDATE purchase_labels SET run_id=$2 WHERE transaction_id=$1`, run+"other", run+"-other"); err != nil {
		t.Fatal(err)
	}
	q, err := s.Quality(t.Context(), "simulator", run)
	if err != nil {
		t.Fatal(err)
	}
	if q.TruePositives != 2 || q.FalsePositives != 1 || q.TrueNegatives != 2 || q.FalseNegatives != 1 || q.LabelledDecisions != 6 {
		t.Fatal(q)
	}
	if *q.Precision != 2.0/3 || *q.Recall != 2.0/3 || *q.FalsePositiveRate != 1.0/3 {
		t.Fatal(q)
	}
	if q.FraudValueFlaggedMinor != "300" || q.FraudValueAllowedMinor != "400" || q.Coverage.ScoredDecisions != nil {
		t.Fatal(q)
	}
	replay, err := s.Quality(t.Context(), "ieee-cis", run)
	if err != nil || replay.LabelledDecisions != 1 || replay.FalseNegatives != 1 {
		t.Fatal(replay, err)
	}
	// Analyst resolution does not rewrite the original scoring outcome.
	_, err = a.Exec(t.Context(), `INSERT INTO review_resolutions(purchase_id,decision,analyst_id) SELECT id,'approve','af75-analyst' FROM purchases WHERE transaction_id=$1`, run+"a")
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Quality(t.Context(), "simulator", run)
	if err != nil || after.TruePositives != 2 {
		t.Fatal(after, err)
	}
}

func TestQualityCoverageExcludesUnfinalizedAnswers(t *testing.T) {
	a, s, run := qualityFixture(t)
	qualityPurchase(t, a, run, qualityRow{run + "saved", "simulator", "allow", false, 100})
	for _, entry := range []struct{ id, reason string }{{"pending", ""}, {"rejected", "rejected"}, {"saved", ""}} {
		_, err := a.Exec(t.Context(), `INSERT INTO pending_purchase_labels(transaction_id,run_id,source,scenario,is_fraud,request_json,rejection_reason)
  VALUES($1,$2,'simulator','fixture',true,'{}',nullif($3,''))`, run+entry.id, run, entry.reason)
		if err != nil {
			t.Fatal(err)
		}
	}
	q, err := s.Quality(t.Context(), "simulator", run)
	if err != nil || q.LabelledDecisions != 1 || q.Coverage.Pending != 1 || q.Coverage.Quarantined != 1 {
		t.Fatal(q, err)
	}
	if q.TrueNegatives != 1 || q.FalseNegatives != 0 {
		t.Fatal(q)
	}
}

func TestQualityUnknownAndEmptyConsoleRuns(t *testing.T) {
	a, s, run := qualityFixture(t)
	if _, err := s.Quality(t.Context(), "simulator", run); err != ErrQualityRunNotFound {
		t.Fatal(err)
	}
	_, err := a.Exec(t.Context(), `INSERT INTO simulation_runs(id,operator_id,request_id,settings,status) VALUES($1,'af75',$1,'{}','completed')`, run)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Quality(t.Context(), "simulator", run)
	if err != nil || q.LabelledDecisions != 0 || q.Precision != nil || q.Coverage.ScoredDecisions == nil || *q.Coverage.ScoredDecisions != 0 {
		t.Fatal(q, err)
	}
	if _, err := s.Quality(t.Context(), "ieee-cis", run); err != ErrQualityRunNotFound {
		t.Fatal(err)
	}
	page, err := s.Runs(t.Context(), "simulator", run[:len(run)-1], 1)
	if err != nil || len(page.RunIDs) != 1 || page.RunIDs[0] != run {
		t.Fatal(page, err)
	}
}

func TestQualityRunsIncludeBothSourcesAndPagination(t *testing.T) {
	a, s, run := qualityFixture(t)
	qualityPurchase(t, a, run, qualityRow{run + "sim", "simulator", "allow", false, 100})
	qualityPurchase(t, a, run, qualityRow{run + "replay", "ieee-cis", "allow", true, 100})
	for _, source := range []string{"simulator", "ieee-cis"} {
		page, err := s.Runs(t.Context(), source, run[:len(run)-1], 1)
		if err != nil || len(page.RunIDs) != 1 || page.RunIDs[0] != run || page.Source != source {
			t.Fatal(page, err)
		}
		next, err := s.Runs(t.Context(), source, run, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range next.RunIDs {
			if id == run {
				t.Fatal("cursor included previous run")
			}
		}
	}
}

func TestQualityMoneyDoesNotOverflow(t *testing.T) {
	a, s, run := qualityFixture(t)
	for _, id := range []string{"a", "b"} {
		qualityPurchase(t, a, run, qualityRow{run + id, "simulator", "block", true, 9223372036854775807})
	}
	q, err := s.Quality(t.Context(), "simulator", run)
	if err != nil || q.FraudValueFlaggedMinor != "18446744073709551614" {
		t.Fatal(q, err)
	}
}

func TestQualityCannotUseScoringCredentials(t *testing.T) {
	dsn := os.Getenv("TEST_RUNTIME_DATABASE_URL")
	if dsn == "" {
		t.Skip("runtime database URL required")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = NewQualityStore(pool).Quality(t.Context(), "simulator", "any")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatal(err)
	}
}

func TestQualityPendingOnlyRunsAndPagination(t *testing.T) {
	a, s, run := qualityFixture(t)
	for _, suffix := range []string{"", "-z"} {
		id := run + suffix
		_, err := a.Exec(t.Context(), `INSERT INTO pending_purchase_labels(transaction_id,run_id,source,scenario,is_fraud,request_json)
  VALUES($1,$1,'simulator','fixture',true,'{}')`, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, err := a.Exec(context.Background(), `DELETE FROM pending_purchase_labels WHERE run_id=$1`, run+"-z")
		if err != nil {
			t.Error(err)
		}
	})
	q, err := s.Quality(t.Context(), "simulator", run)
	if err != nil || q.LabelledDecisions != 0 || q.Coverage.Pending != 1 || q.Precision != nil {
		t.Fatal(q, err)
	}
	page, err := s.Runs(t.Context(), "simulator", run[:len(run)-1], 1)
	if err != nil || page.NextCursor != run {
		t.Fatal(page, err)
	}
	next, err := s.Runs(t.Context(), "simulator", page.NextCursor, 1)
	if err != nil || len(next.RunIDs) != 1 || next.RunIDs[0] != run+"-z" || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	empty, err := s.Runs(t.Context(), "ieee-cis", run, 100)
	if err != nil || len(empty.RunIDs) != 0 || empty.RunIDs == nil {
		t.Fatal(empty, err)
	}
}
