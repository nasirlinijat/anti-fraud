package scoring

import (
	"reflect"
	"testing"
)

func TestVerificationRules(t *testing.T) {
	tests := []struct {
		name         string
		verification PaymentVerification
		want         [3]bool // cvv_mismatch, avs_mismatch, three_ds_failed
	}{
		{"nothing sent", PaymentVerification{}, [3]bool{false, false, false}},
		{"every check passes", PaymentVerification{CVVMatch, AVSMatch, ThreeDSAuthenticated}, [3]bool{false, false, false}},
		{"checks skipped", PaymentVerification{CVVNotChecked, AVSNotChecked, ThreeDSNotEnrolled}, [3]bool{false, false, false}},
		{"partial address and attempted 3-D Secure", PaymentVerification{AVS: AVSPartial, ThreeDS: ThreeDSAttempted}, [3]bool{false, false, false}},
		{"CVV mismatch only", PaymentVerification{CVV: CVVMismatch}, [3]bool{true, false, false}},
		{"AVS mismatch only", PaymentVerification{AVS: AVSMismatch}, [3]bool{false, true, false}},
		{"3-D Secure failed only", PaymentVerification{ThreeDS: ThreeDSFailed}, [3]bool{false, false, true}},
		{"every check fails", PaymentVerification{CVVMismatch, AVSMismatch, ThreeDSFailed}, [3]bool{true, true, true}},
	}
	for _, tt := range tests {
		in := Input{Verification: tt.verification}
		got := [3]bool{cvvMismatch(in, RuleConfig{}), avsMismatch(in, RuleConfig{}), threeDSFailed(in, RuleConfig{})}
		if got != tt.want {
			t.Errorf("%s: fired %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestEvaluateAddsVerificationRulePoints(t *testing.T) {
	rs := Ruleset{
		ReviewThreshold: 50,
		BlockThreshold:  80,
		Rules: map[RuleName]RuleConfig{
			RuleCVVMismatch:   {Enabled: true, Weight: 30},
			RuleAVSMismatch:   {Enabled: false, Weight: 15},
			RuleThreeDSFailed: {Enabled: true, Weight: 45},
		},
	}
	in := Input{Verification: PaymentVerification{CVVMismatch, AVSMismatch, ThreeDSFailed}}

	got := Evaluate(in, rs)

	want := Result{Outcome: OutcomeReview, Score: 75, FiredRules: []FiredRule{
		{Name: RuleCVVMismatch, Points: 30, Message: "The card security code (CVV) did not match"},
		{Name: RuleThreeDSFailed, Points: 45, Message: "The cardholder failed 3-D Secure authentication"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Evaluate = %+v, want %+v", got, want)
	}
}
