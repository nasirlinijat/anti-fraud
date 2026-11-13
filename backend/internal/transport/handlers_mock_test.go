package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

const (
	mockServiceKey  = "mock-service-key"
	mockOperatorKey = "mock-operator-key"
	mockAnalystKey  = "mock-analyst-key"
)

var errServiceDown = errors.New("database is down")

func send(mux http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func mockOperators(t *testing.T) platform.OperatorKeys {
	t.Helper()
	keys, err := platform.ParseOperatorKeys(`[{"operator_id":"op-1","key":"`+mockOperatorKey+`"}]`, mockServiceKey, platform.AnalystKeys{})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func wantResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, bodyParts ...string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	for _, part := range bodyParts {
		if !strings.Contains(rec.Body.String(), part) {
			t.Errorf("body %s, want it to contain %s", rec.Body.String(), part)
		}
	}
}

func TestScoreHandlerWithMockedService(t *testing.T) {
	newMux := func(t *testing.T) (*http.ServeMux, *mocks.MockScoreService) {
		service := mocks.NewMockScoreService(gomock.NewController(t))
		mux := http.NewServeMux()
		RegisterScoreRoutes(mux, NewScoreHandler(service), mockServiceKey)
		return mux, service
	}

	t.Run("200 with the decision", func(t *testing.T) {
		mux, service := newMux(t)
		score, version := 0.42, "lgbm-test"
		want := models.Purchase{TransactionID: "txn-1", EventTime: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC), AmountMinor: 1999, PlayerID: "player-1", CardToken: "card-1"}
		service.EXPECT().Score(gomock.Any(), want, gomock.Any()).Return(models.Decision{
			RulesetVersion: 3,
			Result:         scoring.Result{Outcome: scoring.OutcomeReview, Score: 60, FiredRules: []scoring.FiredRule{{Name: scoring.RuleHighAmount, Points: 60}}},
			ModelScore:     &score,
			ModelVersion:   &version,
		}, nil)
		rec := send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody)
		wantResponse(t, rec, http.StatusOK, `"transaction_id":"txn-1"`, `"outcome":"review"`, `"score":60`, `"ruleset_version":3`, `"model_score":0.42`, `"model_version":"lgbm-test"`)
	})

	t.Run("no fired rules is an empty list", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Decision{Result: scoring.Result{Outcome: scoring.OutcomeAllow}}, nil)
		wantResponse(t, send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody), http.StatusOK, `"fired_rules":[]`, `"model_score":null`)
	})

	t.Run("409 for a reused transaction ID", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Decision{}, models.ErrDuplicateTransaction)
		wantResponse(t, send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody), http.StatusConflict, `"transaction_id":"is already scored"`)
	})

	t.Run("500 hides the internal error", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Decision{}, errServiceDown)
		rec := send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody)
		wantResponse(t, rec, http.StatusInternalServerError, "internal error")
		if strings.Contains(rec.Body.String(), errServiceDown.Error()) {
			t.Fatal("the internal error reached the client")
		}
	})

	t.Run("bad requests never reach the service", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		for _, tc := range []struct {
			key, body string
			status    int
		}{
			{"", minimalBody, http.StatusUnauthorized},
			{"wrong", minimalBody, http.StatusUnauthorized},
			{mockServiceKey, `{`, http.StatusBadRequest},
			{mockServiceKey, `{"transaction_id":"txn-1"}`, http.StatusBadRequest},
			{mockServiceKey, strings.Replace(minimalBody, `"amount_minor": 1999`, `"amount_minor": -1`, 1), http.StatusBadRequest},
			{mockServiceKey, strings.Replace(minimalBody, `"card_token"`, `"unknown_field"`, 1), http.StatusBadRequest},
		} {
			if rec := send(mux, http.MethodPost, "/score", tc.key, tc.body); rec.Code != tc.status {
				t.Errorf("key %q body %s: status %d, want %d", tc.key, tc.body, rec.Code, tc.status)
			}
		}
	})
}

func TestReviewHandlerWithMockedService(t *testing.T) {
	newMux := func(t *testing.T) (*http.ServeMux, *mocks.MockReviewService) {
		service := mocks.NewMockReviewService(gomock.NewController(t))
		keys, err := platform.ParseAnalystKeys(`[{"analyst_id":"analyst-1","key":"`+mockAnalystKey+`"}]`, mockServiceKey)
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		RegisterReviewRoutes(mux, NewReviewHandler(service, true), platform.Access{Analysts: keys})
		return mux, service
	}
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	t.Run("queue page with a next cursor", func(t *testing.T) {
		mux, service := newMux(t)
		next := models.QueueCursor{DecidedAt: at, PurchaseID: 5, ScoreKey: -60}
		service.EXPECT().Queue(gomock.Any(), models.QueueByRuleScore, (*models.QueueCursor)(nil), (*string)(nil)).
			Return(review.Page{Items: []models.QueueItem{{PurchaseID: 5, RuleScore: 60, DecidedAt: at}}, Next: &next}, nil)
		wantResponse(t, send(mux, http.MethodGet, "/reviews", mockAnalystKey, ""), http.StatusOK, `"next_cursor":"`, `"model_enabled":true`)
	})

	t.Run("case not found", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Case(gomock.Any(), int64(9)).Return(models.CaseItem{}, models.ErrReviewNotFound)
		wantResponse(t, send(mux, http.MethodGet, "/reviews/9", mockAnalystKey, ""), http.StatusNotFound, "review case not found")
	})

	decisions := []struct {
		name   string
		err    error
		status int
	}{
		{"saved", nil, http.StatusCreated},
		{"invalid decision", review.ErrInvalidDecision, http.StatusBadRequest},
		{"block device needs reject", review.ErrBlockDeviceNeedsReject, http.StatusBadRequest},
		{"purchase not found", review.ErrPurchaseNotFound, http.StatusNotFound},
		{"not under review", review.ErrNotUnderReview, http.StatusConflict},
		{"already decided", review.ErrAlreadyDecided, http.StatusConflict},
		{"database down", errServiceDown, http.StatusInternalServerError},
	}
	for _, tt := range decisions {
		t.Run("decision "+tt.name, func(t *testing.T) {
			mux, service := newMux(t)
			service.EXPECT().Decide(gomock.Any(), int64(7), "reject", "analyst-1", true).
				Return(models.Resolution{Status: models.ResolutionSaved, ResolvedAt: at, DeviceBlocked: true}, tt.err)
			rec := send(mux, http.MethodPost, "/reviews/7/decision", mockAnalystKey, `{"decision":"reject","block_device":true}`)
			wantResponse(t, rec, tt.status)
			if tt.err == nil {
				wantResponse(t, rec, tt.status, `"analyst_id":"analyst-1"`, `"device_blocked":true`)
			}
		})
	}

	t.Run("requests without an analyst key never reach the service", func(t *testing.T) {
		mux, _ := newMux(t)
		for _, key := range []string{"", mockServiceKey, "wrong"} {
			if rec := send(mux, http.MethodGet, "/reviews", key, ""); rec.Code != http.StatusUnauthorized {
				t.Errorf("key %q: status %d, want 401", key, rec.Code)
			}
		}
	})
}

func TestRulesetHandlerWithMockedService(t *testing.T) {
	newMux := func(t *testing.T) (*http.ServeMux, *mocks.MockRulesetService) {
		service := mocks.NewMockRulesetService(gomock.NewController(t))
		mux := http.NewServeMux()
		RegisterRulesetRoutes(mux, NewRulesetHandler(service), platform.Access{ServiceKey: mockServiceKey, Operators: mockOperators(t)})
		return mux, service
	}
	body := `{"review_threshold":50,"block_threshold":80,"note":"n","rules":{"high_amount":{"enabled":true,"weight":30,"limit":5000}}}`

	t.Run("publish sends the draft and the operator", func(t *testing.T) {
		mux, service := newMux(t)
		draft := gomock.Cond(func(d ruleset.Draft) bool {
			rule := d.Rules[scoring.RuleHighAmount]
			return d.ReviewThreshold == 50 && d.BlockThreshold == 80 && rule != nil && *rule.Weight == 30
		})
		service.EXPECT().Publish(gomock.Any(), draft, "op-1").Return(scoring.Ruleset{Version: 12, ReviewThreshold: 50, BlockThreshold: 80}, nil)
		wantResponse(t, send(mux, http.MethodPost, "/rulesets", mockOperatorKey, body), http.StatusCreated, `"version":12`)
	})

	t.Run("publish maps a validation error to 400 with the fields", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Publish(gomock.Any(), gomock.Any(), "op-1").
			Return(scoring.Ruleset{}, &ruleset.ValidationError{Fields: map[string]string{"block_threshold": "must be positive and greater than review_threshold"}})
		wantResponse(t, send(mux, http.MethodPost, "/rulesets", mockOperatorKey, body), http.StatusBadRequest, `"block_threshold":"must be positive`)
	})

	t.Run("publish needs an operator key", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		wantResponse(t, send(mux, http.MethodPost, "/rulesets", mockServiceKey, body), http.StatusUnauthorized)
	})

	t.Run("unknown version is 404", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Version(gomock.Any(), int32(99)).Return(scoring.Ruleset{}, models.ErrRulesetNotFound)
		wantResponse(t, send(mux, http.MethodGet, "/rulesets/99", mockServiceKey, ""), http.StatusNotFound)
	})

	t.Run("read errors are 500", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Active(gomock.Any()).Return(scoring.Ruleset{}, errServiceDown)
		service.EXPECT().List(gomock.Any()).Return(nil, errServiceDown)
		wantResponse(t, send(mux, http.MethodGet, "/rulesets/active", mockServiceKey, ""), http.StatusInternalServerError)
		wantResponse(t, send(mux, http.MethodGet, "/rulesets", mockServiceKey, ""), http.StatusInternalServerError)
	})
}

func TestBlocklistHandlerWithMockedService(t *testing.T) {
	newMux := func(t *testing.T) (*http.ServeMux, *mocks.MockBlocklistService) {
		service := mocks.NewMockBlocklistService(gomock.NewController(t))
		mux := http.NewServeMux()
		RegisterBlocklistRoutes(mux, NewBlocklistHandler(service), platform.Access{ServiceKey: mockServiceKey, Operators: mockOperators(t)})
		return mux, service
	}
	body := `{"type":"card","value":"card-1","reason":"stolen"}`
	entry := models.BlocklistEntry{Type: models.BlocklistCard, Value: "card-1", Reason: "stolen"}

	tests := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"added", nil, http.StatusCreated, `"id":4`},
		{"invalid", &blocklist.ValidationError{Fields: map[string]string{"value": "is required"}}, http.StatusBadRequest, `"value":"is required"`},
		{"duplicate", models.ErrBlocklistDuplicate, http.StatusConflict, "already on the blocklist"},
		{"database down", errServiceDown, http.StatusInternalServerError, "internal error"},
	}
	for _, tt := range tests {
		t.Run("add "+tt.name, func(t *testing.T) {
			mux, service := newMux(t)
			service.EXPECT().Add(gomock.Any(), entry, "op-1").Return(models.StoredBlocklistEntry{ID: 4, Type: entry.Type, Value: entry.Value, Reason: entry.Reason}, tt.err)
			wantResponse(t, send(mux, http.MethodPost, "/blocklist", mockOperatorKey, body), tt.status, tt.body)
		})
	}

	t.Run("remove unknown entry is 404", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Remove(gomock.Any(), int64(12), "op-1").Return(models.ErrBlocklistEntryNotFound)
		wantResponse(t, send(mux, http.MethodDelete, "/blocklist/12", mockOperatorKey, ""), http.StatusNotFound)
	})

	t.Run("remove", func(t *testing.T) {
		mux, service := newMux(t)
		service.EXPECT().Remove(gomock.Any(), int64(12), "op-1").Return(nil)
		wantResponse(t, send(mux, http.MethodDelete, "/blocklist/12", mockOperatorKey, ""), http.StatusNoContent)
	})
}

func TestStatsHandlerWithMockedService(t *testing.T) {
	service := mocks.NewMockStatsService(gomock.NewController(t))
	mux := http.NewServeMux()
	h := NewStatsHandler(service)
	h.now = func() time.Time { return statsNow }
	RegisterStatsRoutes(mux, h, platform.Access{ServiceKey: mockServiceKey})

	window := models.StatsWindow{Since: statsNow.Add(-time.Hour), Until: statsNow, BucketWidth: 5 * time.Minute}
	service.EXPECT().Window("1h", statsNow).Return(window, nil)
	service.EXPECT().Summary(gomock.Any(), window).Return(models.StatsSummary{ReviewQueue: 2, TopRules: []models.RuleCount{}}, nil)
	wantResponse(t, send(mux, http.MethodGet, "/stats?period=1h", mockServiceKey, ""), http.StatusOK, `"period":"1h"`, `"review_queue":2`)

	service.EXPECT().Window("2d", statsNow).Return(models.StatsWindow{}, errors.New("must be one of 15m, 1h, 24h, 7d, all"))
	wantResponse(t, send(mux, http.MethodGet, "/stats?period=2d", mockServiceKey, ""), http.StatusBadRequest, `"period":"must be one of`)

	after := int64(5)
	service.EXPECT().Recent(gomock.Any(), &after).Return(models.RecentPipeline{After: &after, NextAfter: 9}, nil)
	wantResponse(t, send(mux, http.MethodGet, "/stats/pipeline/recent?after=5", mockServiceKey, ""), http.StatusOK, `"after":5`, `"next_after":9`)
}
