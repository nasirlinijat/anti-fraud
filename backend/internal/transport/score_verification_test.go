package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// verificationRuleset enables only the gateway verification rules, so each
// test sees which of them fired.
var verificationRuleset = scoring.Ruleset{
	Version:         8,
	ReviewThreshold: 50,
	BlockThreshold:  80,
	Rules: map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleCVVMismatch:   {Enabled: true, Weight: 30},
		scoring.RuleAVSMismatch:   {Enabled: true, Weight: 15},
		scoring.RuleThreeDSFailed: {Enabled: true, Weight: 45},
	},
}

func withVerification(results string) string {
	return strings.Replace(minimalBody, `"card_token": "card-1"`, `"card_token": "card-1"`+results, 1)
}

func TestScoreSavesVerificationResults(t *testing.T) {
	purchases := &fakePurchases{}
	body := withVerification(`, "cvv_result": " not_checked ", "avs_result": "partial", "three_ds_result": "attempted"`)
	rec := serve(t, fakeRulesets{rs: verificationRuleset}, purchases, testKey, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	want := scoring.PaymentVerification{CVV: scoring.CVVNotChecked, AVS: scoring.AVSPartial, ThreeDS: scoring.ThreeDSAttempted}
	if got := purchases.saved[0].Verification(); got != want {
		t.Errorf("saved verification = %+v, want %+v", got, want)
	}
}

func TestScoreTreatsEmptyVerificationResultsAsOmitted(t *testing.T) {
	purchases := &fakePurchases{}
	body := withVerification(`, "cvv_result": "", "avs_result": "  ", "three_ds_result": null`)
	rec := serve(t, fakeRulesets{rs: verificationRuleset}, purchases, testKey, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	p := purchases.saved[0]
	if p.CVVResult != nil || p.AVSResult != nil || p.ThreeDSResult != nil {
		t.Errorf("saved results = %v, %v, %v, want all omitted", p.CVVResult, p.AVSResult, p.ThreeDSResult)
	}
}

func TestScoreVerificationRules(t *testing.T) {
	tests := []struct {
		name    string
		results string
		want    []scoring.FiredRule
		outcome scoring.Outcome
	}{
		{"all checks pass", `, "cvv_result": "match", "avs_result": "match", "three_ds_result": "authenticated"`, []scoring.FiredRule{}, scoring.OutcomeAllow},
		{"results omitted", ``, []scoring.FiredRule{}, scoring.OutcomeAllow},
		{"checks skipped", `, "cvv_result": "not_checked", "avs_result": "not_checked", "three_ds_result": "not_enrolled"`, []scoring.FiredRule{}, scoring.OutcomeAllow},
		{"partial address match", `, "avs_result": "partial"`, []scoring.FiredRule{}, scoring.OutcomeAllow},
		{"every check fails", `, "cvv_result": "mismatch", "avs_result": "mismatch", "three_ds_result": "failed"`, []scoring.FiredRule{
			{Name: scoring.RuleCVVMismatch, Points: 30, Message: "The card security code (CVV) did not match"},
			{Name: scoring.RuleAVSMismatch, Points: 15, Message: "The billing address (AVS) did not match the card issuer's records"},
			{Name: scoring.RuleThreeDSFailed, Points: 45, Message: "The cardholder failed 3-D Secure authentication"},
		}, scoring.OutcomeBlock},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, fakeRulesets{rs: verificationRuleset}, &fakePurchases{}, testKey, withVerification(tt.results))

			got := decodeJSON[scoreResponse](t, rec)
			if !reflect.DeepEqual(got.FiredRules, tt.want) || got.Outcome != tt.outcome {
				t.Errorf("fired %+v with %s, want %+v with %s", got.FiredRules, got.Outcome, tt.want, tt.outcome)
			}
		})
	}
}

func TestScoreNamesTheAcceptedVerificationResults(t *testing.T) {
	rec := serve(t, fakeRulesets{rs: verificationRuleset}, &fakePurchases{}, testKey,
		withVerification(`, "cvv_result": "yes", "avs_result": "no", "three_ds_result": "maybe"`))

	got := decodeJSON[platform.ErrorResponse](t, rec)
	want := map[string]string{
		"cvv_result":      "must be match, mismatch, or not_checked",
		"avs_result":      "must be match, partial, mismatch, or not_checked",
		"three_ds_result": "must be authenticated, attempted, failed, or not_enrolled",
	}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("fields = %v, want %v", got.Fields, want)
	}
}

func TestCaseShowsVerificationResults(t *testing.T) {
	cvv, avs, threeDS := scoring.CVVMismatch, scoring.AVSPartial, scoring.ThreeDSFailed
	tests := []struct {
		name string
		item models.CaseItem
		want map[string]any
	}{
		{"sent", models.CaseItem{CVVResult: &cvv, AVSResult: &avs, ThreeDSResult: &threeDS},
			map[string]any{"cvv_result": "mismatch", "avs_result": "partial", "three_ds_result": "failed"}},
		{"not sent", models.CaseItem{},
			map[string]any{"cvv_result": nil, "avs_result": nil, "three_ds_result": nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := caseFunc(func(_ context.Context, id int64) (models.CaseItem, error) {
				item := tt.item
				item.PurchaseID, item.RulesetVersion = id, 1
				return item, nil
			})

			w := requestCase(t, store, "/reviews/7", queueTestKey)

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
				t.Fatalf("status = %d, err = %v, body = %s", w.Code, err, w.Body)
			}
			for field, want := range tt.want {
				if got, present := body[field]; !present || got != want {
					t.Errorf("%s = %v (present %v), want %v", field, got, present, want)
				}
			}
		})
	}
}
