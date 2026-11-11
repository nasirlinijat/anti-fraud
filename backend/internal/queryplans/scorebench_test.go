package queryplans

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

const (
	scorebenchRows   = 500_000
	scorebenchWarmup = 100
	scorebenchTimed  = 1000
	scorebenchModel  = "lgbm-ieee-v5-combined"
)

var scorebenchEnd = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type scorebenchRequest struct {
	simulator.Request
	ClientOS         string `json:"client_os"`
	ClientBrowser    string `json:"client_browser"`
	ScreenResolution string `json:"screen_resolution"`
}

type scorebenchClient struct {
	baseURL string
	key     string
	http    *http.Client
}

func (c scorebenchClient) json(ctx context.Context, method, path string, payload, response any, status int) (time.Duration, error) {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.key)
	started := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	elapsed := time.Since(started)
	if err != nil {
		return 0, err
	}
	if res.StatusCode != status {
		return 0, fmt.Errorf("%s %s returned %d: %s", method, path, res.StatusCode, bytes.TrimSpace(data))
	}
	return elapsed, json.Unmarshal(data, response)
}

// TestScoreBenchmark is run by scripts/scorebench.sh against its disposable stack.
func TestScoreBenchmark(t *testing.T) {
	dsn := os.Getenv("SCOREBENCH_DATABASE_URL")
	if dsn == "" {
		t.Skip("run scripts/scorebench.sh to benchmark scoring")
	}
	dbConfig, err := pgx.ParseConfig(dsn)
	if err != nil || dbConfig.Host != "127.0.0.1" || dbConfig.Database != "scorebench" || dbConfig.User != "scorebench" {
		t.Fatal("expected the dedicated loopback scorebench database")
	}
	apiURL, err := url.Parse(os.Getenv("SCOREBENCH_API_URL"))
	if err != nil || apiURL.Scheme != "http" || apiURL.Hostname() != "127.0.0.1" || apiURL.Port() == "" {
		t.Fatal("expected the dedicated loopback scorebench API")
	}
	key := os.Getenv("SCOREBENCH_API_KEY")
	if key == "" {
		t.Fatal("SCOREBENCH_API_KEY is required")
	}
	ctx := t.Context()
	conn, err := pgx.ConnectConfig(ctx, dbConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	client := scorebenchClient{baseURL: apiURL.String(), key: key, http: &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}

	var purchases, decisions, runningPurchases, players int
	err = conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchases), (SELECT count(*) FROM decisions),
		purchases, players FROM feature_counts WHERE kind = 'all' AND key = ''`).
		Scan(&purchases, &decisions, &runningPurchases, &players)
	if err != nil || purchases != scorebenchRows || decisions != scorebenchRows || runningPurchases != scorebenchRows || players != 10_000 {
		t.Fatalf("fixture purchases=%d decisions=%d feature purchases=%d players=%d: %v",
			purchases, decisions, runningPurchases, players, err)
	}
	var pgVersion string
	if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&pgVersion); err != nil {
		t.Fatal(err)
	}

	operatorKey := os.Getenv("SCOREBENCH_OPERATOR_KEY")
	if operatorKey == "" || operatorKey == key {
		t.Fatal("a distinct SCOREBENCH_OPERATOR_KEY is required")
	}
	version := publishScorebenchRuleset(t, client, operatorKey)
	profiles := readScorebenchProfiles(t, conn)
	rng := rand.New(rand.NewPCG(42, 42))
	warmup := scorebenchWorkload(rng, profiles, "warmup", 0, scorebenchWarmup)
	timed := scorebenchWorkload(rng, profiles, "timed", scorebenchWarmup, scorebenchTimed)
	runScorebenchRequests(t, client, version, warmup)
	if _, err := conn.Exec(ctx, "SELECT pg_stat_statements_reset()"); err != nil {
		t.Fatal(err)
	}
	timings, fired := runScorebenchRequests(t, client, version, timed)

	slices.Sort(timings)
	p50, p95, p99 := scorebenchPercentile(timings, 0.50), scorebenchPercentile(timings, 0.95), scorebenchPercentile(timings, 0.99)
	t.Logf("PostgreSQL %s, history=%d, seed=42, warm-up=%d, timed=%d, ruleset=%d, model=%s",
		pgVersion, purchases, len(warmup), len(timed), version, scorebenchModel)
	t.Logf("HTTP p50=%s p95=%s p99=%s; device_card_count fired=%d, new_device_spike fired=%d",
		p50, p95, p99, fired["device_card_count"], fired["new_device_spike"])
	if p95 >= 50*time.Millisecond {
		t.Errorf("HTTP p95 %s is at or above 50ms", p95)
	}
	if fired["device_card_count"] == 0 || fired["new_device_spike"] == 0 {
		t.Error("the timed workload must fire both new rules")
	}
	logScorebenchStatements(t, conn)

	var saved int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM purchases p JOIN decisions d ON d.purchase_id = p.id
		WHERE p.transaction_id LIKE 'bench-timed-%' AND d.ruleset_version = $1 AND d.model_version = $2`,
		version, scorebenchModel).Scan(&saved)
	if err != nil || saved != len(timed) {
		t.Errorf("saved %d timed decisions under the expected ruleset and model, want %d: %v", saved, len(timed), err)
	}
	explainScorebenchReads(t, conn, profiles)
}

func publishScorebenchRuleset(t *testing.T, client scorebenchClient, operatorKey string) int {
	t.Helper()
	var active struct {
		Version         int                        `json:"version,omitempty"`
		ReviewThreshold int                        `json:"review_threshold"`
		BlockThreshold  int                        `json:"block_threshold"`
		Rules           map[string]json.RawMessage `json:"rules"`
		Model           json.RawMessage            `json:"model"`
	}
	if _, err := client.json(t.Context(), http.MethodGet, "/rulesets/active", nil, &active, http.StatusOK); err != nil {
		t.Fatal(err)
	}
	if active.Version != 1 || active.Rules == nil {
		t.Fatalf("expected seeded ruleset version 1, got %d", active.Version)
	}
	var model struct{ Mode string }
	if json.Unmarshal(active.Model, &model) != nil || model.Mode != "assist" {
		t.Fatal("expected the seeded model settings in assist mode")
	}
	active.Rules["device_card_count"] = json.RawMessage(`{"enabled":true,"weight":20,"limit":5}`)
	active.Rules["new_device_spike"] = json.RawMessage(`{"enabled":true,"weight":50,"limit":3,"window_days":90,"min_amount_minor":1000,"amount_multiplier":3}`)
	active.Version = 0
	operator := client
	operator.key = operatorKey
	var published struct{ Version int }
	if _, err := operator.json(t.Context(), http.MethodPost, "/rulesets", active, &published, http.StatusCreated); err != nil {
		t.Fatal(err)
	}
	if published.Version != 2 {
		t.Fatalf("expected benchmark ruleset version 2, got %d", published.Version)
	}
	return published.Version
}

func readScorebenchProfiles(t *testing.T, conn *pgx.Conn) [3][]scorebenchRequest {
	t.Helper()
	rows, err := conn.Query(t.Context(), `SELECT DISTINCT ON (player_id, card_token)
		player_id, card_token, card_issuer, card_network, card_type, card_country, product_code,
		billing_region, billing_country, email_domain, device_type, device_name, device_id, ip_country,
		account_created_at, client_os, client_browser, screen_resolution
		FROM purchases WHERE transaction_id LIKE 'bench-history-%'
		ORDER BY player_id, card_token, event_time`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var profiles [3][]scorebenchRequest
	for rows.Next() {
		var p scorebenchRequest
		if err := rows.Scan(&p.PlayerID, &p.CardToken, &p.CardIssuer, &p.CardNetwork, &p.CardType, &p.CardCountry, &p.ProductCode,
			&p.BillingRegion, &p.BillingCountry, &p.EmailDomain, &p.DeviceType, &p.DeviceName, &p.DeviceID, &p.IPCountry,
			&p.AccountCreatedAt, &p.ClientOS, &p.ClientBrowser, &p.ScreenResolution); err != nil {
			t.Fatal(err)
		}
		bucket := 2
		if p.CardIssuer == nil {
			bucket = 0
		} else if *p.CardIssuer == "bench-common-issuer" {
			bucket = 1
		}
		profiles[bucket] = append(profiles[bucket], p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(profiles[0]) != 2000 || len(profiles[1]) != 2000 || len(profiles[2]) != 16000 {
		t.Fatal("fixture card profiles do not match the benchmark issuer mix")
	}
	return profiles
}

func scorebenchWorkload(rng *rand.Rand, profiles [3][]scorebenchRequest, phase string, offset, count int) []scorebenchRequest {
	cohorts, issuers := make([]int, count), make([]int, count)
	for i := range count {
		// Each list is stratified 80/10/10 and shuffled independently.
		switch i % 10 {
		case 0, 1:
			cohorts[i], issuers[i] = i%10+1, i%10
		default:
			issuers[i] = 2
		}
	}
	rng.Shuffle(count, func(i, j int) { cohorts[i], cohorts[j] = cohorts[j], cohorts[i] })
	rng.Shuffle(count, func(i, j int) { issuers[i], issuers[j] = issuers[j], issuers[i] })
	requests := make([]scorebenchRequest, count)
	for i := range requests {
		bucket := profiles[issuers[i]]
		p := bucket[rng.IntN(len(bucket))]
		p.TransactionID = fmt.Sprintf("bench-%s-%04d", phase, i+1)
		p.EventTime = scorebenchEnd.Add(time.Duration(offset+i+1) * time.Millisecond)
		p.AmountMinor = int64(200 + rng.IntN(2801))
		switch cohorts[i] {
		case 1:
			device := fmt.Sprintf("bench-shared-%d", rng.IntN(20))
			p.DeviceID = &device
		case 2:
			device := "bench-spike-" + p.TransactionID
			p.DeviceID = &device
			p.AmountMinor = 10_000 + int64(rng.IntN(10_000))
		}
		requests[i] = p
	}
	return requests
}

func runScorebenchRequests(t *testing.T, client scorebenchClient, version int, requests []scorebenchRequest) ([]time.Duration, map[string]int) {
	t.Helper()
	timings := make([]time.Duration, 0, len(requests))
	fired := make(map[string]int)
	for i, req := range requests {
		var res struct {
			TransactionID  string                  `json:"transaction_id"`
			RulesetVersion int                     `json:"ruleset_version"`
			ModelVersion   string                  `json:"model_version"`
			FiredRules     []struct{ Name string } `json:"fired_rules"`
		}
		elapsed, err := client.json(t.Context(), http.MethodPost, "/score", req, &res, http.StatusOK)
		if err != nil {
			t.Fatalf("request %d of %d (%s): %v", i+1, len(requests), req.TransactionID, err)
		}
		if res.TransactionID != req.TransactionID || res.RulesetVersion != version || res.ModelVersion != scorebenchModel {
			t.Fatalf("request %s got unexpected decision metadata: %+v", req.TransactionID, res)
		}
		timings = append(timings, elapsed)
		for _, rule := range res.FiredRules {
			fired[rule.Name]++
		}
	}
	return timings, fired
}

// scorebenchPercentile uses nearest rank on sorted, nonempty samples.
func scorebenchPercentile(sorted []time.Duration, percentile float64) time.Duration {
	return sorted[int(math.Ceil(percentile*float64(len(sorted))))-1]
}

func TestScorebenchPercentile(t *testing.T) {
	samples := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	for _, tc := range []struct {
		percentile float64
		want       time.Duration
	}{{0.50, 10}, {0.95, 19}, {0.99, 20}} {
		if got := scorebenchPercentile(samples, tc.percentile); got != tc.want {
			t.Errorf("percentile %.2f = %d, want %d", tc.percentile, got, tc.want)
		}
	}
}

func logScorebenchStatements(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	rows, err := conn.Query(t.Context(), `SELECT query, calls, total_exec_time, mean_exec_time
		FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		ORDER BY total_exec_time DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type statement struct {
		name        string
		calls       int64
		total, mean float64
	}
	var statements []statement
	for rows.Next() {
		var s statement
		if err := rows.Scan(&s.name, &s.calls, &s.total, &s.mean); err != nil {
			t.Fatal(err)
		}
		s.name = strings.TrimSpace(s.name)
		if strings.HasPrefix(s.name, "-- name:") {
			s.name, _, _ = strings.Cut(s.name, "\n")
			s.name = strings.TrimPrefix(s.name, "-- name: ")
		}
		statements = append(statements, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, rank := range []string{"total", "mean"} {
		if rank == "mean" {
			slices.SortFunc(statements, func(a, b statement) int {
				switch {
				case a.mean > b.mean:
					return -1
				case a.mean < b.mean:
					return 1
				default:
					return 0
				}
			})
		}
		t.Logf("Slowest SQL by %s execution time", rank)
		for _, s := range statements[:min(8, len(statements))] {
			t.Logf("%s: %d calls, total %.2f ms, mean %.2f ms", s.name, s.calls, s.total, s.mean)
		}
	}
}

func explainScorebenchReads(t *testing.T, conn *pgx.Conn, profiles [3][]scorebenchRequest) {
	t.Helper()
	at := scorebenchEnd.Add(2 * time.Second)
	p := profiles[2][0]
	const txn = "bench-plan"
	for _, q := range []struct {
		name, sql, index string
		args             []any
	}{
		{"IssuerHistory typical", features.IssuerHistorySQL, "purchases_issuer_event_time", []any{*p.CardIssuer, at, txn}},
		{"IssuerHistory missing", features.IssuerHistorySQL, "purchases_issuer_event_time", []any{"", at, txn}},
		{"IssuerHistory common", features.IssuerHistorySQL, "purchases_issuer_event_time", []any{"bench-common-issuer", at, txn}},
		{"PlayerHistory", features.PlayerHistorySQL, "purchases_player_event_time", []any{
			at, int64(10_000), *p.EmailDomain, p.DeviceName, p.ClientOS, p.ClientBrowser, p.ScreenResolution, p.PlayerID, txn}},
		{"CardHistory", features.CardHistorySQL, "purchases_card_event_time", []any{at, p.CardToken, txn}},
		{"PlayerEarlierModelScores", score.PlayerEarlierModelScoresSQL, "purchases_player_event_time", []any{p.PlayerID, at, txn}},
		{"CardPurchasesLastHour", score.CountCardPurchasesLastHourSQL, "purchases_card_event_time", []any{p.CardToken, at}},
		{"DeviceDistinctCardsLastHour", score.CountDeviceCardsLastHourSQL, "purchases_device_event_time", []any{"bench-shared-0", at, p.CardToken}},
		{"TrustedPurchaseHistory", score.TrustedPurchaseHistorySQL, "purchases_player_event_time", []any{"bench-plan-device", p.PlayerID, at.AddDate(0, 0, -90), at, txn}},
	} {
		rows, err := conn.Query(t.Context(), "EXPLAIN (ANALYZE, BUFFERS) "+q.sql, q.args...)
		if err != nil {
			t.Errorf("explain %s: %v", q.name, err)
			continue
		}
		lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Errorf("read %s plan: %v", q.name, err)
			continue
		}
		plan := strings.Join(lines, "\n")
		t.Logf("%s plan:\n%s", q.name, plan)
		if !strings.Contains(plan, q.index) || strings.Contains(plan, "Seq Scan") {
			t.Errorf("%s must use %s with no sequential scans", q.name, q.index)
		}
	}
}
