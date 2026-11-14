package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

// rulesWithoutLimit are the rules that read a value reported for the purchase
// and have no threshold.
var rulesWithoutLimit = []scoring.RuleName{
	scoring.RuleCVVMismatch, scoring.RuleAVSMismatch, scoring.RuleThreeDSFailed,
	scoring.RuleDatacenterIP, scoring.RuleAnonymousIP,
}

func TestPublishRejectsInvalidRulesWithoutLimit(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRulesetRoutes(mux, NewRulesetHandler(ruleset.NewService(nil)), platform.Access{Sessions: consoleTestSessions})
	tests := []struct {
		name, rule, field string
	}{
		{"negative weight", `{"enabled":true,"weight":-1}`, "weight"},
		{"missing enabled", `{"weight":30}`, "enabled"},
		{"negative limit", `{"enabled":true,"weight":30,"limit":-1}`, "limit"},
		{"spike settings", `{"enabled":true,"weight":30,"window_days":30}`, ""},
	}
	for _, name := range rulesWithoutLimit {
		for _, tt := range tests {
			t.Run(string(name)+" "+tt.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"review_threshold":50,"block_threshold":80,"rules":{%q:%s}}`, name, tt.rule)
				rec := rulesetRequest(mux, "POST", "/rulesets", body, adminTestToken)

				field := "rules." + string(name)
				if tt.field != "" {
					field += "." + tt.field
				}
				var got platform.ErrorResponse
				if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Fields[field] == "" {
					t.Errorf("status = %d, body = %s, want 400 naming %s", rec.Code, rec.Body, field)
				}
			})
		}
	}
}

func TestPublishAcceptsRulesWithoutLimit(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRulesetRoutes(mux, NewRulesetHandler(ruleset.NewService(storage.NewRulesetStore(isolatedRulesetPool(t)))),
		platform.Access{Sessions: consoleTestSessions})
	body := `{"review_threshold":50,"block_threshold":80,"rules":{
		"cvv_mismatch":{"enabled":true,"weight":30},
		"avs_mismatch":{"enabled":false,"weight":15,"limit":0},
		"three_ds_failed":{"enabled":true,"weight":45},
		"datacenter_ip":{"enabled":true,"weight":30},
		"anonymous_ip":{"enabled":false,"weight":20}
	}}`

	published := rulesetRequest(mux, "POST", "/rulesets", body, adminTestToken)
	active := rulesetRequest(mux, "GET", "/rulesets/active", "", "test-key")

	if published.Code != http.StatusCreated {
		t.Fatalf("publish status = %d, want 201: %s", published.Code, published.Body)
	}
	var got versionedConfig
	if err := json.Unmarshal(active.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	type setting struct {
		enabled bool
		weight  int32
	}
	want := map[scoring.RuleName]setting{
		scoring.RuleCVVMismatch:   {true, 30},
		scoring.RuleAVSMismatch:   {false, 15},
		scoring.RuleThreeDSFailed: {true, 45},
		scoring.RuleDatacenterIP:  {true, 30},
		scoring.RuleAnonymousIP:   {false, 20},
	}
	if len(got.Rules) != len(want) {
		t.Fatalf("active rules = %v, want %v", got.Rules, want)
	}
	for name, w := range want {
		rule := got.Rules[name]
		if rule == nil || *rule.Enabled != w.enabled || *rule.Weight != w.weight || *rule.Limit != 0 {
			t.Errorf("%s = %+v, want %+v with limit 0", name, rule, w)
		}
	}
}
