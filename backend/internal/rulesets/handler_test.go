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

const operatorTestKey = "operator-key"

func testOperators(t *testing.T) platform.OperatorKeys {
	t.Helper()
	keys, err := platform.ParseOperatorKeys(`[{"operator_id":"ruleset-operator","key":"`+operatorTestKey+`"}]`, "test-key", platform.AnalystKeys{})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func rulesetRequest(mux *http.ServeMux, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(platform.APIKeyHeader, key)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestRulesetRoutesRejectInvalidRequests(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, "test-key", testOperators(t))
	for _, tc := range []struct{ method, path string }{
		{"GET", "/rulesets"},
		{"GET", "/rulesets/active"},
		{"GET", "/rulesets/1"},
		{"POST", "/rulesets"},
	} {
		if w := rulesetRequest(mux, tc.method, tc.path, "", "wrong"); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, w.Code)
		}
	}
	for _, badVersion := range []string{"0", "-1", "abc", "2147483648"} {
		if w := rulesetRequest(mux, "GET", "/rulesets/"+badVersion, "", "test-key"); w.Code != http.StatusBadRequest {
			t.Errorf("GET /rulesets/%s status = %d, want 400", badVersion, w.Code)
		}
	}

	w := rulesetRequest(mux, "POST", "/rulesets", `{
		"review_threshold": 50, "block_threshold": 40,
		"note": "`+strings.Repeat("n", 501)+`",
		"rules": {"high_amount": {"enabled": true, "weight": -1}, "unknown": {}}
	}`, operatorTestKey)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
	}
	var got platform.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"block_threshold", "note", "rules.high_amount.weight", "rules.high_amount.limit", "rules.unknown"} {
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
	}`, operatorTestKey)
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
	for _, path := range []string{
		"../../migrations/000002_create_rulesets.up.sql",
		"../../migrations/000015_add_new_device_spike_settings.up.sql",
		"../../migrations/000016_add_ruleset_note.up.sql",
		"../../migrations/000018_configuration_audit.up.sql",
		"../../migrations/000019_add_model_settings_to_rulesets.up.sql",
	} {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := isolated.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}

	mux := http.NewServeMux()
	RegisterRoutes(mux, NewStore(isolated), "test-key", testOperators(t))
	const requests = 4
	if w := rulesetRequest(mux, "POST", "/rulesets", "", "test-key"); w.Code != http.StatusUnauthorized {
		t.Fatalf("service key publishing status = %d, want 401", w.Code)
	}
	responses := make(chan *httptest.ResponseRecorder, requests)
	for i := range requests {
		go func() {
			body := fmt.Sprintf(`{"review_threshold":40,"block_threshold":70,"note":"  note %d  ","rules":{"high_amount":{"enabled":true,"weight":%d,"limit":1000}}}`, i, 10+i)
			responses <- rulesetRequest(mux, "POST", "/rulesets", body, operatorTestKey)
		}()
	}
	type versionedConfig struct {
		Version   int       `json:"version"`
		CreatedAt time.Time `json:"created_at"`
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
		if got.CreatedAt.IsZero() || strings.TrimSpace(got.Note) != got.Note || got.Note == "" {
			t.Errorf("unexpected published metadata: %+v", got)
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

	listRes := rulesetRequest(mux, "GET", "/rulesets", "", "test-key")
	var history struct {
		Versions []VersionSummary `json:"versions"`
	}
	if listRes.Code != http.StatusOK || json.Unmarshal(listRes.Body.Bytes(), &history) != nil {
		t.Fatalf("GET /rulesets: %d %s", listRes.Code, listRes.Body)
	}
	if len(history.Versions) != requests+1 || history.Versions[0].Version != requests+1 || history.Versions[requests].Version != 1 {
		t.Errorf("unexpected version history: %+v", history.Versions)
	}

	v2Res := rulesetRequest(mux, "GET", "/rulesets/2", "", "test-key")
	var v2 versionedConfig
	if v2Res.Code != http.StatusOK || json.Unmarshal(v2Res.Body.Bytes(), &v2) != nil {
		t.Fatalf("GET /rulesets/2: %d %s", v2Res.Code, v2Res.Body)
	}
	if !reflect.DeepEqual(v2.configPayload, versions[2].configPayload) {
		t.Errorf("GET /rulesets/2 = %+v, want %+v", v2.configPayload, versions[2].configPayload)
	}

	if missing := rulesetRequest(mux, "GET", "/rulesets/999", "", "test-key"); missing.Code != http.StatusNotFound {
		t.Errorf("GET /rulesets/999 status = %d, want 404", missing.Code)
	}

	var audited int
	if err := isolated.QueryRow(ctx, `
        SELECT count(*) FROM configuration_audit_events a
        JOIN rulesets r ON r.version = a.resource_id
        WHERE a.action = 'ruleset.publish' AND a.actor_id = 'ruleset-operator'
    `).Scan(&audited); err != nil || audited != requests {
		t.Fatalf("audited publications = %d, error = %v, want %d", audited, err, requests)
	}

	var oldWeight int
	err = isolated.QueryRow(ctx, `SELECT weight FROM ruleset_rules WHERE ruleset_version = 1 AND rule_name = 'high_amount'`).Scan(&oldWeight)
	if err != nil || oldWeight != 30 {
		t.Errorf("seed weight = %d, error = %v, want 30", oldWeight, err)
	}
}

func TestPublishRejectsInvalidDeviceCardCount(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, "test-key", testOperators(t))
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
			rec := rulesetRequest(mux, "POST", "/rulesets", body, operatorTestKey)
			var got platform.ErrorResponse
			if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Fields[tc.field] == "" {
				t.Errorf("status = %d, body = %s, want 400 with %s", rec.Code, rec.Body, tc.field)
			}
		})
	}
}

func TestPublishRejectsInvalidNewDeviceSpike(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, "test-key", testOperators(t))
	for _, tc := range []struct {
		name, rule string
		fields     []string
	}{
		{"missing settings on disabled rule", `{"enabled":false,"weight":50}`,
			[]string{"limit", "window_days", "min_amount_minor", "amount_multiplier"}},
		{"zero and negative values", `{"enabled":true,"weight":-1,"limit":0,"window_days":0,"min_amount_minor":-1,"amount_multiplier":1}`,
			[]string{"weight", "limit", "window_days", "min_amount_minor", "amount_multiplier"}},
		{"window too large", `{"enabled":true,"weight":50,"limit":3,"window_days":3651,"min_amount_minor":1000,"amount_multiplier":3}`,
			[]string{"window_days"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"review_threshold":50,"block_threshold":80,"rules":{"new_device_spike":%s}}`, tc.rule)
			rec := rulesetRequest(mux, "POST", "/rulesets", body, operatorTestKey)
			var got platform.ErrorResponse
			if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
				t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body)
			}
			for _, field := range tc.fields {
				if got.Fields["rules.new_device_spike."+field] == "" {
					t.Errorf("missing error for %s in %s", field, rec.Body)
				}
			}
		})
	}
}

func TestPublishValidatesModelSettings(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, "test-key", testOperators(t))
	for _, tc := range []struct {
		model string
		field string
	}{
		{`{"mode": "on"}`, "model.mode"},
		{`{"clear_below": 0.1, "flag_at": 0.5}`, "model.mode"},
		{`{"mode": "assist", "clear_below": 0.1}`, "model"},
		{`{"mode": "assist", "flag_at": 0.5}`, "model"},
		{`{"mode": "assist", "clear_below": -0.1, "flag_at": 0.5}`, "model.clear_below"},
		{`{"mode": "assist", "clear_below": 0.1, "flag_at": 1.5}`, "model.flag_at"},
		{`{"mode": "assist", "clear_below": 0.5, "flag_at": 0.5}`, "model.clear_below"},
		{`{"mode": "shadow", "clear_below": 0.6, "flag_at": 0.5}`, "model.clear_below"},
		{`{"mode": "assist", "blend": 0.5}`, "blend"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			w := rulesetRequest(mux, "POST", "/rulesets", `{
				"review_threshold": 50, "block_threshold": 80, "rules": {}, "model": `+tc.model+`
			}`, operatorTestKey)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
			}
			var got platform.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Fields[tc.field] == "" {
				t.Errorf("missing error for %q in %v", tc.field, got.Fields)
			}
		})
	}
}
