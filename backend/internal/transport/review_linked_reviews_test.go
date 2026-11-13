package transport

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"go.uber.org/mock/gomock"
)

func TestLinkedReviewsSerializesQueueItems(t *testing.T) {
	mux, service := newNetworkPurchasesMux(t)
	device, holder := "device-1", "alice"
	score, version := 0.75, "model-v1"
	claimedAt := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	service.EXPECT().LinkedReviews(gomock.Any(), int64(9)).Return(models.LinkedReviews{WindowDays: 30, Total: 51, Items: []models.QueueItem{
		{
			PurchaseID: 12, TransactionID: "tx-12", EventTime: time.Date(2026, 11, 1, 12, 0, 0, 0, time.FixedZone("CET", 3600)),
			AmountMinor: 1999, PlayerID: "player-2", DeviceID: &device, RuleScore: 70, ModelScore: &score, ModelVersion: &version,
			FiredRules: []scoring.FiredRule{{Name: "high_amount", Points: 70, Message: "High amount"}},
			DecidedAt:  time.Date(2026, 11, 1, 11, 0, 1, 0, time.UTC), ClaimedBy: &holder, ClaimedAt: &claimedAt,
		},
		{PurchaseID: 13, TransactionID: "tx-13", EventTime: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
			AmountMinor: 500, PlayerID: "player-3", RuleScore: 60, DecidedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)},
	}}, nil)

	w := send(mux, http.MethodGet, "/reviews/9/network/reviews", queueTestKey, "")

	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
	}
	want := `{"window_days":30,"total":51,"items":[` +
		`{"purchase_id":"12","transaction_id":"tx-12","event_time":"2026-11-01T11:00:00Z","queued_at":"2026-11-01T11:00:01Z","amount_minor":"1999",` +
		`"player_id":"player-2","device_id":"device-1","rule_score":70,"model_score":0.75,"model_version":"model-v1",` +
		`"fired_rules":[{"name":"high_amount","points":70,"message":"High amount"}],"claimed_by":"alice","claimed_at":"2026-11-02T08:00:00Z"},` +
		`{"purchase_id":"13","transaction_id":"tx-13","event_time":"2026-11-01T09:00:00Z","queued_at":"2026-11-01T09:00:00Z","amount_minor":"500",` +
		`"player_id":"player-3","device_id":null,"rule_score":60,"model_score":null,"model_version":null,` +
		`"fired_rules":[],"claimed_by":null,"claimed_at":null}]}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
}

func TestLinkedReviewsWithoutItemsIsAnEmptyList(t *testing.T) {
	mux, service := newNetworkPurchasesMux(t)
	service.EXPECT().LinkedReviews(gomock.Any(), int64(5)).Return(models.LinkedReviews{WindowDays: 30}, nil)

	w := send(mux, http.MethodGet, "/reviews/5/network/reviews", queueTestKey, "")

	wantResponse(t, w, http.StatusOK, `"total":0`, `"items":[]`)
}

func TestLinkedReviewsRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		key    string
		status int
		body   string
	}{
		{"missing key", "/reviews/1/network/reviews", "", http.StatusUnauthorized, ""},
		{"service key", "/reviews/1/network/reviews", serviceTestKey, http.StatusUnauthorized, ""},
		{"invalid purchase ID", "/reviews/01/network/reviews", queueTestKey, http.StatusBadRequest, "invalid purchase ID"},
		{"zero purchase ID", "/reviews/0/network/reviews", queueTestKey, http.StatusBadRequest, "invalid purchase ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, service := newNetworkPurchasesMux(t)
			service.EXPECT().LinkedReviews(gomock.Any(), gomock.Any()).Times(0)

			w := send(mux, http.MethodGet, tc.target, tc.key, "")

			wantResponse(t, w, tc.status, tc.body)
		})
	}
}

func TestLinkedReviewsMapsServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"not under review", models.ErrReviewNotFound, http.StatusNotFound, `{"error":"review case not found"}`},
		{"database down", errServiceDown, http.StatusInternalServerError, `{"error":"internal error"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, service := newNetworkPurchasesMux(t)
			service.EXPECT().LinkedReviews(gomock.Any(), int64(5)).Return(models.LinkedReviews{}, tc.err)

			w := send(mux, http.MethodGet, "/reviews/5/network/reviews", queueTestKey, "")

			if w.Code != tc.status || strings.TrimSpace(w.Body.String()) != tc.body {
				t.Fatalf("status = %d, body = %s; want %d %s", w.Code, w.Body, tc.status, tc.body)
			}
		})
	}
}
