package rulesets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

func rulesetRequest(mux *http.ServeMux, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(platform.APIKeyHeader, key)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestRulesetRoutesRejectInvalidRequests(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, "test-key")
	for method, path := range map[string]string{"GET": "/rulesets/active", "POST": "/rulesets"} {
		if w := rulesetRequest(mux, method, path, "", "wrong"); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", method, path, w.Code)
		}
	}

	w := rulesetRequest(mux, "POST", "/rulesets", `{
		"review_threshold": 50, "block_threshold": 40,
		"rules": {"high_amount": {"enabled": true, "weight": -1}, "unknown": {}}
	}`, "test-key")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
	}
	var got platform.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"block_threshold", "rules.high_amount.weight", "rules.high_amount.limit", "rules.unknown"} {
		if got.Fields[field] == "" {
			t.Errorf("missing error for %q in %v", field, got.Fields)
		}
	}

	w = rulesetRequest(mux, "POST", "/rulesets", `{
		"review_threshold": 40, "block_threshold": 70,
		"rules": {
			"high_amount": {"enabled": true, "weight": 2147483647, "limit": 1000},
			"country_mismatch": {"enabled": true, "weight": 1}
		}
	}`, "test-key")
	if w.Code != http.StatusBadRequest {
		t.Errorf("overflowing rule score status = %d, want 400: %s", w.Code, w.Body)
	}
}

func TestConcurrentRulesetPublishing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := fmt.Sprintf("rulesets_publish_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config := pool.Config()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	isolated, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(isolated.Close)
	migration, err := os.ReadFile("../../migrations/000002_create_rulesets.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	RegisterRoutes(mux, NewStore(isolated), "test-key")
	const requests = 4
	responses := make(chan *httptest.ResponseRecorder, requests)
	for i := range requests {
		go func() {
			body := fmt.Sprintf(`{"review_threshold":40,"block_threshold":70,"rules":{"high_amount":{"enabled":true,"weight":%d,"limit":1000}}}`, 10+i)
			responses <- rulesetRequest(mux, "POST", "/rulesets", body, "test-key")
		}()
	}
	type versionedConfig struct {
		Version int `json:"version"`
		configPayload
	}
	versions := make(map[int]versionedConfig)
	for range requests {
		w := <-responses
		if w.Code != http.StatusCreated {
			t.Errorf("publish status = %d, want 201: %s", w.Code, w.Body)
			continue
		}
		var got versionedConfig
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Error(err)
			continue
		}
		versions[got.Version] = got
	}
	for version := 2; version <= requests+1; version++ {
		if _, ok := versions[version]; !ok {
			t.Errorf("version %d was not published", version)
		}
	}

	w := rulesetRequest(mux, "GET", "/rulesets/active", "", "test-key")
	var active versionedConfig
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &active) != nil {
		t.Fatalf("could not read active version: %d %s", w.Code, w.Body)
	}
	if active.Version != requests+1 || !reflect.DeepEqual(active.configPayload, versions[requests+1].configPayload) {
		t.Errorf("active configuration does not match the last published version: %s", w.Body)
	}

	var oldWeight int
	err = isolated.QueryRow(ctx, `SELECT weight FROM ruleset_rules WHERE ruleset_version = 1 AND rule_name = 'high_amount'`).Scan(&oldWeight)
	if err != nil || oldWeight != 30 {
		t.Errorf("seed weight = %d, error = %v, want 30", oldWeight, err)
	}
}

func TestPublishRejectsInvalidDeviceCardCount(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, "test-key")
	for _, tc := range []struct {
		name  string
		rules string
		field string
	}{
		{"missing limit", `"device_card_count":{"enabled":true,"weight":20}`, "rules.device_card_count.limit"},
		{"zero limit", `"device_card_count":{"enabled":true,"weight":20,"limit":0}`, "rules.device_card_count.limit"},
		{"negative limit", `"device_card_count":{"enabled":false,"weight":20,"limit":-1}`, "rules.device_card_count.limit"},
		{"negative weight", `"device_card_count":{"enabled":true,"weight":-1,"limit":5}`, "rules.device_card_count.weight"},
		{"total weight overflow", `"device_card_count":{"enabled":true,"weight":2147483647,"limit":5},"high_amount":{"enabled":true,"weight":1,"limit":1000}`, "rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"review_threshold":50,"block_threshold":80,"rules":{%s}}`, tc.rules)
			rec := rulesetRequest(mux, "POST", "/rulesets", body, "test-key")
			var got platform.ErrorResponse
			if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Fields[tc.field] == "" {
				t.Errorf("status = %d, body = %s, want 400 with %s", rec.Code, rec.Body, tc.field)
			}
		})
	}
}
