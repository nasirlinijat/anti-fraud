package simulator

import "testing"

func TestEveryFraudPatternHasAVerificationProfile(t *testing.T) {
	for _, p := range Patterns {
		if _, ok := fraudVerification[p]; p.Fraud() && !ok {
			t.Errorf("fraud pattern %s has no entry in fraudVerification and would use the legitimate profile", p)
		}
	}
}
