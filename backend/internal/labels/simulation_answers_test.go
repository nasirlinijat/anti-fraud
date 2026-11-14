package labels_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

type answerFixture struct {
	transaction, outcome, answer, source, labelRun string
}

func TestSimulationAnswersPaginateSavedDecisionsAndFinalizedAnswers(t *testing.T) {
	owner, evaluation := setup(t)
	const run = "sim-af202"
	const other = "sim-af202-txn-other"
	saveAnswerRun(t, owner, run)
	saveAnswerRun(t, owner, other)
	for _, fixture := range []answerFixture{
		{run + "-txn-1", "allow", "fraud", "simulator", run},
		{run + "-txn-2", "review", "legitimate", "simulator", run},
		{run + "-txn-3", "block", "legitimate", "simulator", run},
		{run + "-txn-4", "allow", "pending", "simulator", run},
		{run + "-txn-5", "allow", "fraud", "simulator", other},
		{run + "-txn-6", "allow", "fraud", "ieee-cis", run},
		{run + "-txn-7", "review", "fraud", "simulator", run},
		{other + "-txn-8", "allow", "fraud", "simulator", other},
	} {
		saveAnswerPurchase(t, owner, fixture)
	}

	reader := labels.NewSimulationAnswers(evaluation)
	all := readAnswerPage(t, reader, labels.SimulationAnswerQuery{RunID: run, Limit: 20})
	assertAnswers(t, all, []string{"7:fraud:review", "6:unknown:allow", "5:unknown:allow", "4:unknown:allow", "3:legitimate:block", "2:legitimate:review", "1:fraud:allow"}, run)
	filter := labels.SimulationAnswerQuery{RunID: run, Limit: 2, Exceptions: true}
	first := readAnswerPage(t, reader, filter)
	assertAnswers(t, first, []string{"3:legitimate:block", "2:legitimate:review"}, run)
	if first.NextCursor == "" {
		t.Fatal("missing next cursor")
	}
	before, err := strconv.ParseInt(first.NextCursor, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	filter.Before = before
	second := readAnswerPage(t, reader, filter)
	assertAnswers(t, second, []string{"1:fraud:allow"}, run)
	if second.NextCursor != "" {
		t.Fatalf("unexpected next cursor %q", second.NextCursor)
	}
	if _, err := reader.Page(t.Context(), labels.SimulationAnswerQuery{RunID: "no-such-run", Limit: 20}); !errors.Is(err, models.ErrSimulationNotFound) {
		t.Fatalf("missing run error = %v", err)
	}
}

func saveAnswerRun(t *testing.T, owner *pgxpool.Pool, run string) {
	t.Helper()
	_, err := owner.Exec(t.Context(), `INSERT INTO simulation_runs (id, operator_id, request_id, settings, status) VALUES ($1, 'af202-test', $1, '{}', 'completed')`, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := owner.Exec(context.Background(), `DELETE FROM simulation_runs WHERE id = $1`, run); err != nil {
			t.Error(err)
		}
	})
}

func saveAnswerPurchase(t *testing.T, owner *pgxpool.Pool, f answerFixture) {
	t.Helper()
	purchase(t, owner, f.transaction)
	_, err := owner.Exec(t.Context(), `UPDATE decisions SET outcome = $2 WHERE purchase_id = (SELECT id FROM purchases WHERE transaction_id = $1)`, f.transaction, f.outcome)
	if err != nil {
		t.Fatal(err)
	}
	if f.answer == "pending" {
		_, err = owner.Exec(t.Context(), `INSERT INTO pending_purchase_labels (transaction_id, run_id, source, scenario, is_fraud) VALUES ($1, $2, 'simulator', 'normal', true)`, f.transaction, f.labelRun)
	} else {
		_, err = owner.Exec(t.Context(), `INSERT INTO purchase_labels (transaction_id, run_id, source, scenario, is_fraud) VALUES ($1, $2, $3, CASE WHEN $3 = 'simulator' THEN 'normal' END, $4)`, f.transaction, f.labelRun, f.source, f.answer == "fraud")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func readAnswerPage(t *testing.T, reader *labels.SimulationAnswers, q labels.SimulationAnswerQuery) labels.SimulationAnswerPage {
	t.Helper()
	page, err := reader.Page(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func assertAnswers(t *testing.T, page labels.SimulationAnswerPage, want []string, run string) {
	t.Helper()
	var got []string
	for _, row := range page.Items {
		got = append(got, strings.TrimPrefix(row.TransactionID, run+"-txn-")+":"+row.Answer+":"+string(row.Outcome))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("answers = %v, want %v", got, want)
	}
}
