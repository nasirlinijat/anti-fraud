package queryplans

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/stats"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/trainingdata"
)

// TestFeatureQueriesUseIndexes checks that every feature history, account
// history, and dashboard statistics query can be served by an index. Sequential scans are disabled, so a query without a
// usable index still shows a sequential scan in its plan.
func TestFeatureQueriesUseIndexes(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	if _, err := conn.Exec(ctx, "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable sequential scans: %v", err)
	}

	at := pgtype.Timestamptz{Time: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Valid: true}
	until := pgtype.Timestamptz{Time: at.Time.Add(time.Hour), Valid: true}
	width := pgtype.Interval{Microseconds: (5 * time.Minute).Microseconds(), Valid: true}
	monthBefore := pgtype.Timestamptz{Time: at.Time.AddDate(0, 0, -30), Valid: true}
	monthAfter := pgtype.Timestamptz{Time: at.Time.AddDate(0, 0, 30), Valid: true}
	device := pgtype.Text{String: "Windows", Valid: true}
	deviceID := pgtype.Text{String: "device-1", Valid: true}
	client := pgtype.Text{String: "Windows 10", Valid: true}
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{"PlayerHistory", features.PlayerHistorySQL, []any{at, int64(1000), "gmail.com", device, client, client, client, "player-1", "txn-1", deviceID}},
		{"CardHistory", features.CardHistorySQL, []any{at, "card-1", "txn-1", "100"}},
		{"DeviceIDHistory", features.DeviceIDHistorySQL, []any{"device-1", at, "txn-1"}},
		{"IssuerHistory", features.IssuerHistorySQL, []any{"1001", at, "txn-1"}},
		{"FeatureCounts", features.FeatureCountsSQL, []any{"gmail.com", device, client, client, client}},
		{"DecisionOverview", storage.DecisionOverviewSQL, []any{at, until}},
		{"BlockedValue", storage.BlockedValueSQL, []any{at, until}},
		{"CountDecisionBuckets", storage.CountDecisionBucketsSQL, []any{width, at, until}},
		{"TopFiredRules", storage.TopFiredRulesSQL, []any{at, until, int32(10)}},
		{"CountReviewQueue", storage.CountReviewQueueSQL, nil},
		{"CountModelEffects", storage.CountModelEffectsSQL, []any{at, until}},
		{"QualityMetrics", stats.QualityMetricsSQL, []any{"simulator", "run-1"}},
		{"QualityPending", stats.QualityPendingSQL, []any{"simulator", "run-1"}},
		{"QualityRuns", stats.QualityRunsSQL, []any{"simulator", "", 21}},
		{"ListModelInputs", trainingdata.ModelInputsPageSQL, []any{at, "txn-1", int32(5000)}},
		{"PlayerEarlierModelScores", storage.PlayerEarlierModelScoresSQL, []any{"player-1", at, "txn-1"}},
		{"CountDeviceDistinctCardsLastHour", storage.CountDeviceCardsLastHourSQL, []any{"device-1", at, "card-1"}},
		{"AssignmentReviewers", storage.AssignmentReviewersSQL, nil},
		{"OpenClaims", storage.OpenClaimsSQL, nil},
		{"NetworkDevicePlayers", storage.NetworkDevicePlayersSQL, []any{"device-1", monthBefore, monthAfter, "player-1", "player", int32(30)}},
		{"NetworkCardPlayers", storage.NetworkCardPlayersSQL, []any{"card-1", monthBefore, monthAfter, "player-1", "player", int32(30)}},
		{"NetworkPlayerCards", storage.NetworkPlayerCardsSQL, []any{"player-1", monthBefore, monthAfter, "card-1", "card", int32(30)}},
		{"NetworkDevicePlayerPurchases", storage.NetworkDevicePlayerPurchasesSQL, []any{"device-1", monthBefore, monthAfter, "player-2", int32(50)}},
		{"NetworkCardPlayerPurchases", storage.NetworkCardPlayerPurchasesSQL, []any{"card-1", monthBefore, monthAfter, "player-2", int32(50)}},
		{"NetworkPlayerCardPurchases", storage.NetworkPlayerCardPurchasesSQL, []any{"player-1", monthBefore, monthAfter, "card-2", int32(50)}},
		{"LinkedReviews", storage.LinkedReviewsSQL, []any{"device-1", "card-1", "player-1", monthBefore, monthAfter, int64(1), int32(50)}},
		{"AccountHistory", storage.AccountHistorySQL, []any{"player-1", pgtype.Timestamptz{Time: at.Time.Add(-24 * time.Hour), Valid: true}, at, int32(50)}},
		{"GetTrustedPurchaseHistory", storage.TrustedPurchaseHistorySQL, []any{
			"device-1", "player-1", pgtype.Timestamptz{Time: at.Time.AddDate(0, 0, -90), Valid: true}, at, "txn-1",
		}},
	}
	for _, q := range queries {
		t.Run(q.name, func(t *testing.T) {
			rows, err := conn.Query(ctx, "EXPLAIN "+q.query, q.args...)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatalf("read plan: %v", err)
			}
			plan := strings.Join(lines, "\n")
			if strings.Contains(plan, "Seq Scan") || !strings.Contains(plan, "Index") {
				t.Errorf("query does not use an index:\n%s", plan)
			}
		})
	}
}
