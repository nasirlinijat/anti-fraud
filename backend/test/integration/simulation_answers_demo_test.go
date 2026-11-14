//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
	"github.com/nasirlinijat/anti-fraud/backend/test/testdb"
)

func TestSeededSimulationAnswerDemo(t *testing.T) {
	db, err := testdb.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Stop(); err != nil {
			t.Error(err)
		}
	})
	owner := demoPool(t, db.OwnerURL)
	evaluation := demoPool(t, db.LabelsURL)
	runtime := demoPool(t, db.RuntimeURL)

	cfg := simulator.DefaultConfig()
	cfg.Run = "sim-af202-demo"
	cfg.Seed = 42
	cfg.MissingFieldShare = 0
	cfg.Scenarios = []simulator.Pattern{simulator.PatternLowAndSlow, simulator.PatternNewBigSpender}
	purchases, err := simulator.GenerateContext(t.Context(), cfg, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(t.Context(), `INSERT INTO simulation_runs (id, operator_id, request_id, settings, status)
		VALUES ($1, 'demo-admin', $1, '{}', 'completed')`, cfg.Run); err != nil {
		t.Fatal(err)
	}
	scoreDemoPurchases(t, runtime, evaluation, purchases, cfg.Run)
	page, err := labels.NewSimulationAnswers(evaluation).Page(t.Context(), labels.SimulationAnswerQuery{
		RunID: cfg.Run, Limit: 50, Exceptions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var missed, friction string
	for _, row := range page.Items {
		if row.Answer == "fraud" && row.Outcome == "allow" {
			missed = row.TransactionID
		}
		if row.Answer == "legitimate" && (row.Outcome == "review" || row.Outcome == "block") {
			friction = row.TransactionID
		}
	}
	if missed == "" || friction == "" {
		t.Fatalf("seed %d did not produce both exception categories: %+v", cfg.Seed, page.Items)
	}
	t.Logf("seed %d, ruleset v1, model off: missed fraud %s; legitimate friction %s", cfg.Seed, missed, friction)
}

func scoreDemoPurchases(t *testing.T, runtime, evaluation *pgxpool.Pool, purchases []simulator.Purchase, run string) {
	t.Helper()
	const apiKey = "seeded-demo-score-key"
	rules := storage.NewRulesetStore(runtime)
	rs, err := rules.Active(t.Context())
	if err != nil || rs.Version != 1 {
		t.Fatalf("demo requires seeded active ruleset v1, got %d: %v", rs.Version, err)
	}
	service := score.NewService(rules, storage.NewBlocklistStore(runtime), storage.NewPurchaseStore(runtime))
	mux := http.NewServeMux()
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(service), apiKey)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &simulator.Client{BaseURL: server.URL, APIKey: apiKey, HTTP: server.Client()}
	scorer := labels.Scorer{Pool: evaluation, Client: client, RunID: run}
	for _, p := range purchases {
		if _, err := scorer.ScorePurchase(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
}

func demoPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
