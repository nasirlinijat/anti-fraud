package score_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score/mocks"
	"go.uber.org/mock/gomock"
)

var errDatabase = errors.New("database unavailable")

var eventTime = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

const mockModelVersion = "mock-model-v1"

type scoreDeps struct {
	rulesets  *mocks.MockRulesets
	blocklist *mocks.MockBlocklist
	purchases *mocks.MockPurchases
	model     *mocks.MockModel
	service   *score.Service
}

func newScoreDeps(t *testing.T) scoreDeps {
	ctrl := gomock.NewController(t)
	d := scoreDeps{
		rulesets:  mocks.NewMockRulesets(ctrl),
		blocklist: mocks.NewMockBlocklist(ctrl),
		purchases: mocks.NewMockPurchases(ctrl),
		model:     mocks.NewMockModel(ctrl),
	}
	d.service = score.NewService(d.rulesets, d.blocklist, d.purchases)
	return d
}

func (d scoreDeps) useModel(t *testing.T, policy scoring.ModelPolicy) {
	t.Helper()
	d.model.EXPECT().Features().Return(features.Names).AnyTimes()
	d.model.EXPECT().Version().Return(mockModelVersion).AnyTimes()
	if err := d.service.UseModel(d.model, policy); err != nil {
		t.Fatal(err)
	}
}

func rulesetWith(mode scoring.ModelMode) scoring.Ruleset {
	return scoring.Ruleset{
		Version:         7,
		ReviewThreshold: 50,
		BlockThreshold:  80,
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleHighAmount:   {Enabled: true, Weight: 60, Limit: 10_000},
			scoring.RuleCardVelocity: {Enabled: true, Weight: 30, Limit: 3},
		},
		Model: scoring.ModelSettings{Mode: mode},
	}
}

func purchaseOf(amountMinor int64) models.Purchase {
	return models.Purchase{TransactionID: "txn-1", EventTime: eventTime, AmountMinor: amountMinor, PlayerID: "player-1", CardToken: "card-1"}
}

// expectUnsaved sets up a purchase with no saved decision, no blocklist match
// and an empty history, so the rules run.
func (d scoreDeps) expectUnsaved(p models.Purchase, rs scoring.Ruleset, cardUses int) {
	d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
	d.rulesets.EXPECT().Active(gomock.Any()).Return(rs, nil)
	d.blocklist.EXPECT().Match(gomock.Any(), models.BlocklistSubject{CardToken: p.CardToken, PlayerID: p.PlayerID}).Return(models.BlocklistEntry{}, false, nil)
	d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, nil)
	d.purchases.EXPECT().CardPurchasesLastHour(gomock.Any(), p.CardToken, p.EventTime).Return(cardUses, nil)
}

func TestScoreWithRulesOnly(t *testing.T) {
	tests := []struct {
		name        string
		amountMinor int64
		cardUses    int
		want        scoring.Outcome
		wantScore   int
	}{
		{"small purchase is allowed", 100, 0, scoring.OutcomeAllow, 0},
		{"high amount goes to review", 20_000, 0, scoring.OutcomeReview, 60},
		{"high amount and card velocity are blocked", 20_000, 5, scoring.OutcomeBlock, 90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			p := purchaseOf(tt.amountMinor)
			d.expectUnsaved(p, rulesetWith(scoring.ModelOff), tt.cardUses)
			var saved models.Decision
			d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ models.Purchase, dec models.Decision) (int64, error) {
					saved = dec
					return 1, nil
				})

			got, err := d.service.Score(context.Background(), p, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got.Result.Outcome != tt.want || got.Result.Score != tt.wantScore || got.RulesetVersion != 7 {
				t.Fatalf("decision = %+v, want %s with score %d on version 7", got.Result, tt.want, tt.wantScore)
			}
			if got.ModelScore != nil || got.ModelVersion != nil {
				t.Fatal("a model score was set without a model")
			}
			if saved.Result.Outcome != tt.want || len(saved.ModelInputs) == 0 {
				t.Fatalf("saved %+v, want the same outcome with model inputs", saved)
			}
		})
	}
}

func TestScoreReturnsTheSavedDecisionOfARetry(t *testing.T) {
	d := newScoreDeps(t)
	p := purchaseOf(100)
	saved := models.Decision{RulesetVersion: 3, Result: scoring.Result{Outcome: scoring.OutcomeReview, Score: 60}}
	d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(&saved, nil)

	got, err := d.service.Score(context.Background(), p, time.Now())
	if err != nil || got.RulesetVersion != 3 || got.Result.Outcome != scoring.OutcomeReview {
		t.Fatalf("Score = %+v, %v; want the saved decision", got, err)
	}
}

func TestScoreBlocksABlocklistMatchWithoutRunningTheRules(t *testing.T) {
	d := newScoreDeps(t)
	p := purchaseOf(100)
	d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
	d.rulesets.EXPECT().Active(gomock.Any()).Return(rulesetWith(scoring.ModelOff), nil)
	d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{Type: models.BlocklistCard, Value: "card-1"}, true, nil)
	d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, nil)
	d.purchases.EXPECT().CardPurchasesLastHour(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

	got, err := d.service.Score(context.Background(), p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Outcome != scoring.OutcomeBlock || len(got.Result.FiredRules) != 1 || got.Result.FiredRules[0].Name != scoring.RuleBlocklist {
		t.Fatalf("result = %+v, want a block by the blocklist only", got.Result)
	}
}

func TestScoreReadsOnlyTheHistoryTheEnabledRulesNeed(t *testing.T) {
	device := "device-1"
	tests := []struct {
		name          string
		rules         map[scoring.RuleName]scoring.RuleConfig
		wantDevice    int
		wantTrustHist int
	}{
		{"device rules off", nil, 0, 0},
		{"device card count on", map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleDeviceCardCount: {Enabled: true, Weight: 30, Limit: 3},
		}, 1, 0},
		{"new device spike on", map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleNewDeviceSpike: {Enabled: true, Weight: 30, Limit: 1, WindowDays: 30, MinAmountMinor: 50, AmountMultiplier: 3},
		}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			p := purchaseOf(100)
			p.DeviceID = &device
			rs := rulesetWith(scoring.ModelOff)
			for name, rule := range tt.rules {
				rs.Rules[name] = rule
			}
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
			d.rulesets.EXPECT().Active(gomock.Any()).Return(rs, nil)
			d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{}, false, nil)
			d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, nil)
			d.purchases.EXPECT().CardPurchasesLastHour(gomock.Any(), gomock.Any(), gomock.Any()).Return(0, nil)
			d.purchases.EXPECT().DeviceCardsLastHour(gomock.Any(), device, p.CardToken, p.EventTime).Return(int64(1), nil).Times(tt.wantDevice)
			d.purchases.EXPECT().TrustedHistory(gomock.Any(), p, int32(30)).Return(&scoring.TrustedPurchaseHistory{}, nil).Times(tt.wantTrustHist)
			d.purchases.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)

			if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScoreReturnsTheWinnerOfADuplicateSave(t *testing.T) {
	t.Run("winner found", func(t *testing.T) {
		d := newScoreDeps(t)
		p := purchaseOf(100)
		d.expectUnsaved(p, rulesetWith(scoring.ModelOff), 0)
		winner := models.Decision{RulesetVersion: 6, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}
		gomock.InOrder(
			d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(0), models.ErrDuplicateTransaction),
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(&winner, nil),
		)
		got, err := d.service.Score(context.Background(), p, time.Now())
		if err != nil || got.RulesetVersion != 6 {
			t.Fatalf("Score = %+v, %v; want the winner's decision", got, err)
		}
	})

	t.Run("winner missing", func(t *testing.T) {
		d := newScoreDeps(t)
		p := purchaseOf(100)
		d.expectUnsaved(p, rulesetWith(scoring.ModelOff), 0)
		gomock.InOrder(
			d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(0), models.ErrDuplicateTransaction),
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil),
		)
		if _, err := d.service.Score(context.Background(), p, time.Now()); err == nil {
			t.Fatal("a missing winner was not reported")
		}
	})

	t.Run("different purchase with the same transaction", func(t *testing.T) {
		d := newScoreDeps(t)
		p := purchaseOf(100)
		d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, models.ErrDuplicateTransaction)
		if _, err := d.service.Score(context.Background(), p, time.Now()); !errors.Is(err, models.ErrDuplicateTransaction) {
			t.Fatalf("err = %v, want ErrDuplicateTransaction", err)
		}
	})
}

func TestScoreReturnsEveryStoreError(t *testing.T) {
	tests := []struct {
		name   string
		expect func(d scoreDeps, p models.Purchase)
	}{
		{"find saved decision", func(d scoreDeps, p models.Purchase) {
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, errDatabase)
		}},
		{"active ruleset", func(d scoreDeps, p models.Purchase) {
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
			d.rulesets.EXPECT().Active(gomock.Any()).Return(scoring.Ruleset{}, errDatabase)
		}},
		{"blocklist", func(d scoreDeps, p models.Purchase) {
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
			d.rulesets.EXPECT().Active(gomock.Any()).Return(rulesetWith(scoring.ModelOff), nil)
			d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{}, false, errDatabase)
		}},
		{"feature history", func(d scoreDeps, p models.Purchase) {
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
			d.rulesets.EXPECT().Active(gomock.Any()).Return(rulesetWith(scoring.ModelOff), nil)
			d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{}, false, nil)
			d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, errDatabase)
		}},
		{"card history", func(d scoreDeps, p models.Purchase) {
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
			d.rulesets.EXPECT().Active(gomock.Any()).Return(rulesetWith(scoring.ModelOff), nil)
			d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{}, false, nil)
			d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, nil)
			d.purchases.EXPECT().CardPurchasesLastHour(gomock.Any(), gomock.Any(), gomock.Any()).Return(0, errDatabase)
		}},
		{"save", func(d scoreDeps, p models.Purchase) {
			d.expectUnsaved(p, rulesetWith(scoring.ModelOff), 0)
			d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(0), errDatabase)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			p := purchaseOf(100)
			tt.expect(d, p)
			if _, err := d.service.Score(context.Background(), p, time.Now()); !errors.Is(err, errDatabase) {
				t.Fatalf("err = %v, want the store error", err)
			}
		})
	}
}

func TestScoreWithTheModel(t *testing.T) {
	policy := scoring.ModelPolicy{ClearBelow: 0.05, FlagAt: 0.6}
	tests := []struct {
		name        string
		mode        scoring.ModelMode
		amountMinor int64
		cardUses    int
		modelScore  float64
		want        scoring.Outcome
	}{
		{"assist: a high score sends an allowed purchase to review, never to block", scoring.ModelAssist, 100, 0, 0.99, scoring.OutcomeReview},
		{"assist: a low score clears a review", scoring.ModelAssist, 20_000, 0, 0.01, scoring.OutcomeAllow},
		{"assist: a low score never unblocks a block", scoring.ModelAssist, 20_000, 5, 0.0, scoring.OutcomeBlock},
		{"assist: a middle score keeps the rule outcome", scoring.ModelAssist, 20_000, 0, 0.3, scoring.OutcomeReview},
		{"shadow: the score is saved but never changes the outcome", scoring.ModelShadow, 100, 0, 0.99, scoring.OutcomeAllow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			d.useModel(t, policy)
			p := purchaseOf(tt.amountMinor)
			d.expectUnsaved(p, rulesetWith(tt.mode), tt.cardUses)
			d.model.EXPECT().Predict(gomock.Len(len(features.Names))).Return(tt.modelScore, nil)
			d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

			got, err := d.service.Score(context.Background(), p, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got.Result.Outcome != tt.want {
				t.Fatalf("outcome = %s, want %s", got.Result.Outcome, tt.want)
			}
			if got.ModelScore == nil || *got.ModelScore != tt.modelScore || got.ModelVersion == nil || *got.ModelVersion != mockModelVersion {
				t.Fatalf("model score %v, version %v; want %v from %s", got.ModelScore, got.ModelVersion, tt.modelScore, mockModelVersion)
			}
		})
	}
}

func TestScoreSkipsTheModelWhenTheRulesetTurnsItOff(t *testing.T) {
	d := newScoreDeps(t)
	d.useModel(t, scoring.ModelPolicy{ClearBelow: 0.05, FlagAt: 0.6})
	p := purchaseOf(100)
	d.expectUnsaved(p, rulesetWith(scoring.ModelOff), 0)
	d.model.EXPECT().Predict(gomock.Any()).Times(0)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

	got, err := d.service.Score(context.Background(), p, time.Now())
	if err != nil || got.ModelScore != nil {
		t.Fatalf("Score = %+v, %v; want no model score", got, err)
	}
}

func TestScoreBlendsThePlayersEarlierScores(t *testing.T) {
	policy := scoring.ModelPolicy{ClearBelow: 0.05, FlagAt: 0.6, PlayerBlendWeight: 0.5}
	d := newScoreDeps(t)
	d.useModel(t, policy)
	p := purchaseOf(100)
	d.expectUnsaved(p, rulesetWith(scoring.ModelAssist), 0)
	d.model.EXPECT().Predict(gomock.Any()).Return(0.2, nil)
	d.purchases.EXPECT().PlayerEarlierModelScores(gomock.Any(), p.PlayerID, p.EventTime, p.TransactionID).Return(1.6, int64(2), nil)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

	got, err := d.service.Score(context.Background(), p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := policy.Blend(0.2, 1.6, 2)
	if *got.ModelScore != want || *got.ModelScoreUnblended != 0.2 {
		t.Fatalf("score %v, unblended %v; want %v and 0.2", *got.ModelScore, *got.ModelScoreUnblended, want)
	}
	if got.Result.Outcome != scoring.ApplyModel(scoring.OutcomeAllow, want, policy) {
		t.Fatalf("outcome %s does not use the blended score", got.Result.Outcome)
	}
}

func TestScoreReturnsAModelError(t *testing.T) {
	d := newScoreDeps(t)
	d.useModel(t, scoring.ModelPolicy{ClearBelow: 0.05, FlagAt: 0.6})
	p := purchaseOf(100)
	d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
	d.rulesets.EXPECT().Active(gomock.Any()).Return(rulesetWith(scoring.ModelAssist), nil)
	d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{}, false, nil)
	d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, nil)
	d.model.EXPECT().Predict(gomock.Any()).Return(0.0, errors.New("bad tree"))

	if _, err := d.service.Score(context.Background(), p, time.Now()); err == nil {
		t.Fatal("a model error was not returned")
	}
}
