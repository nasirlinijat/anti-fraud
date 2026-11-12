package score_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"go.uber.org/mock/gomock"
)

// rulesetWithAccountRules returns the mock ruleset with the account event
// rules added and a review threshold that one of them reaches.
func rulesetWithAccountRules(rules map[scoring.RuleName]scoring.RuleConfig) scoring.Ruleset {
	rs := rulesetWith(scoring.ModelOff)
	rs.ReviewThreshold = 20
	maps.Copy(rs.Rules, rules)
	return rs
}

func TestScoreReadsAccountEventsOnlyForEnabledRules(t *testing.T) {
	d := newScoreDeps(t)
	p := purchaseOf(100)
	d.expectUnsaved(p, rulesetWithAccountRules(map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleRecentPasswordReset: {Enabled: false, Weight: 30, Limit: 24},
	}), 0)
	d.accounts.EXPECT().History(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

	if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestScoreReadsAccountEventsOverTheLongestEnabledWindow(t *testing.T) {
	d := newScoreDeps(t)
	p := purchaseOf(100)
	d.expectUnsaved(p, rulesetWithAccountRules(map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleRecentPasswordReset: {Enabled: true, Weight: 30, Limit: 24},
		scoring.RuleNewDeviceLogin:      {Enabled: true, Weight: 25, Limit: 72},
	}), 0)
	reset := eventTime.Add(-2 * time.Hour)
	d.accounts.EXPECT().History(gomock.Any(), "player-1", eventTime.Add(-72*time.Hour), eventTime).
		Return(scoring.AccountHistory{LastPasswordReset: &reset}, nil)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

	got, err := d.service.Score(context.Background(), p, time.Now())

	if err != nil {
		t.Fatal(err)
	}
	fired := got.Result.FiredRules
	if got.Result.Outcome != scoring.OutcomeReview || len(fired) != 1 || fired[0].Name != scoring.RuleRecentPasswordReset {
		t.Fatalf("result = %+v, want a review from recent_password_reset alone", got.Result)
	}
}

func TestScoreKeepsAccountEventsOutOfTheModelInputs(t *testing.T) {
	inputsWith := func(rs scoring.Ruleset, history scoring.AccountHistory) map[string]float64 {
		d := newScoreDeps(t)
		p := purchaseOf(100)
		d.expectUnsaved(p, rs, 0)
		d.accounts.EXPECT().History(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(history, nil).AnyTimes()
		var saved models.Decision
		d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ models.Purchase, dec models.Decision) (int64, error) {
				saved = dec
				return 1, nil
			})
		if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
			t.Fatal(err)
		}
		return saved.ModelInputs
	}
	reset := eventTime.Add(-time.Hour)
	history := scoring.AccountHistory{LastPasswordReset: &reset, NewDeviceLogins: []time.Time{reset}, NewCountryLogins: []time.Time{reset}}
	enabled := rulesetWithAccountRules(map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleRecentPasswordReset: {Enabled: true, Weight: 30, Limit: 24},
		scoring.RuleNewDeviceLogin:      {Enabled: true, Weight: 25, Limit: 24},
		scoring.RuleNewCountryLogin:     {Enabled: true, Weight: 20, Limit: 24},
	})

	withEvents := inputsWith(enabled, history)
	withoutEvents := inputsWith(rulesetWith(scoring.ModelOff), scoring.AccountHistory{})

	if len(withEvents) != len(features.Names) || !maps.Equal(withEvents, withoutEvents) {
		t.Fatalf("model inputs changed with account events:\n%v\n%v", withEvents, withoutEvents)
	}
}

func TestScoreReturnsAnAccountHistoryError(t *testing.T) {
	d := newScoreDeps(t)
	p := purchaseOf(100)
	d.expectUnsaved(p, rulesetWithAccountRules(map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleNewCountryLogin: {Enabled: true, Weight: 20, Limit: 24},
	}), 0)
	d.accounts.EXPECT().History(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(scoring.AccountHistory{}, errDatabase)

	_, err := d.service.Score(context.Background(), p, time.Now())

	if !errors.Is(err, errDatabase) {
		t.Fatalf("err = %v, want the store error", err)
	}
}
