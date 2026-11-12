//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

const passwordResetRuleset = `{
	"review_threshold": 50,
	"block_threshold": 80,
	"note": "integration test",
	"rules": {
		"recent_password_reset": {"enabled": true, "weight": 60, "limit": 24}
	}
}`

func TestAPasswordResetBeforeAPurchaseSendsItToReview(t *testing.T) {
	a := newApp(t)
	resetAt := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	reset := fmt.Sprintf(`{"event_id":"it-reset-1","player_id":"player-it-reset","event_type":"password_reset","event_time":%q}`, resetAt)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/account-events", serviceKey, reset), http.StatusCreated)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/account-events", serviceKey, reset), http.StatusOK)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/rulesets", operatorKey, passwordResetRuleset), http.StatusCreated)

	got := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-reset", 1999, "")), http.StatusOK)

	if got.Outcome != "review" || len(got.FiredRules) != 1 || got.FiredRules[0].Name != "recent_password_reset" {
		t.Fatalf("response %+v, want a review from recent_password_reset", got)
	}
}

func TestAccountEventsNeedTheServiceKey(t *testing.T) {
	a := newApp(t)
	body := `{"event_id":"it-login-1","player_id":"player-it","event_type":"login","event_time":"2026-11-08T10:00:00Z"}`
	expect[map[string]any](t, a.do(t, http.MethodPost, "/account-events", operatorKey, body), http.StatusUnauthorized)
}
