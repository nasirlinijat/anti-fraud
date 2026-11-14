package ruleset_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"go.uber.org/mock/gomock"
)

func previewRuleset(version int, highWeight int) scoring.Ruleset {
	return scoring.Ruleset{
		Version: version, ReviewThreshold: 50, BlockThreshold: 80,
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleHighAmount: {Enabled: true, Weight: highWeight, Limit: 50000},
		},
		Model: scoring.ModelSettings{Mode: scoring.ModelAssist},
	}
}

func TestPreviewReplaysRecentDecisionsWithTheDraft(t *testing.T) {
	svc, store := newService(t)
	svc.UseModelThresholds(scoring.ModelPolicy{ClearBelow: 0.2, FlagAt: 0.9})
	high := scoring.FiredRule{Name: scoring.RuleHighAmount, Points: 60}
	decisions := []models.ReplayDecision{
		{PurchaseID: 1, TransactionID: "t-1", Outcome: "review", RuleScore: 60, FiredRules: []scoring.FiredRule{high}, ModelScore: ptr(0.5), RulesetVersion: 3},
		{PurchaseID: 2, TransactionID: "t-2", Outcome: "allow", RuleScore: 0, ModelScore: ptr(0.3), RulesetVersion: 3},
		{PurchaseID: 3, TransactionID: "t-3", Outcome: "review", RuleScore: 60, FiredRules: []scoring.FiredRule{high}, ModelScore: nil, RulesetVersion: 2},
	}
	store.EXPECT().Active(gomock.Any()).Return(previewRuleset(3, 60), nil)
	store.EXPECT().RecentDecisions(gomock.Any(), ruleset.PreviewDecisions).Return(decisions, nil)
	store.EXPECT().Version(gomock.Any(), int32(3)).Return(previewRuleset(3, 60), nil).Times(1)
	store.EXPECT().Version(gomock.Any(), int32(2)).Return(previewRuleset(2, 60), nil).Times(1)

	got, err := svc.Preview(context.Background(), validDraft())
	if err != nil {
		t.Fatal(err)
	}
	want := models.RulesetPreview{
		Decisions:          3,
		Before:             models.PreviewCounts{Allow: 1, Review: 2},
		After:              models.PreviewCounts{Allow: 3},
		Changed:            2,
		Uncertain:          1,
		UncertainRules:     []string{},
		MissingModelScores: 1,
	}
	if got.Decisions != want.Decisions || got.Before != want.Before || got.After != want.After || got.Changed != want.Changed ||
		got.Uncertain != want.Uncertain || got.MissingModelScores != want.MissingModelScores || len(got.UncertainRules) != 0 {
		t.Fatalf("preview = %+v, want %+v", got, want)
	}
	if len(got.Changes) != 2 || got.Changes[0].TransactionID != "t-1" || got.Changes[0].Before != "review" || got.Changes[0].After != "allow" ||
		got.Changes[0].RuleScoreAfter != 30 || got.Changes[0].Uncertain || !got.Changes[1].Uncertain {
		t.Fatalf("changes = %+v", got.Changes)
	}
}

func TestPreviewOfTheActiveRulesetChangesNothing(t *testing.T) {
	svc, store := newService(t)
	active := previewRuleset(3, 30)
	high := scoring.FiredRule{Name: scoring.RuleHighAmount, Points: 30}
	store.EXPECT().Active(gomock.Any()).Return(active, nil)
	store.EXPECT().RecentDecisions(gomock.Any(), gomock.Any()).Return([]models.ReplayDecision{
		{PurchaseID: 1, Outcome: "allow", RuleScore: 30, FiredRules: []scoring.FiredRule{high}, RulesetVersion: 3},
		{PurchaseID: 2, Outcome: "allow", RulesetVersion: 3},
	}, nil)
	store.EXPECT().Version(gomock.Any(), int32(3)).Return(active, nil)

	got, err := svc.Preview(context.Background(), validDraft())
	if err != nil || got.Changed != 0 || got.Uncertain != 0 || got.Before != got.After {
		t.Fatalf("preview of the active ruleset = %+v, %v; want no changes", got, err)
	}
}

func TestPreviewListsAtMostFiftyChanges(t *testing.T) {
	svc, store := newService(t)
	var decisions []models.ReplayDecision
	for i := range 60 {
		decisions = append(decisions, models.ReplayDecision{PurchaseID: int64(i), TransactionID: fmt.Sprint("t-", i), Outcome: "block", RuleScore: 90, RulesetVersion: 3})
	}
	store.EXPECT().Active(gomock.Any()).Return(previewRuleset(3, 30), nil)
	store.EXPECT().RecentDecisions(gomock.Any(), gomock.Any()).Return(decisions, nil)
	store.EXPECT().Version(gomock.Any(), int32(3)).Return(previewRuleset(3, 30), nil)

	got, err := svc.Preview(context.Background(), validDraft())
	if err != nil || got.Changed != 60 || len(got.Changes) != 50 || got.After.Allow != 60 {
		t.Fatalf("preview = changed %d, listed %d, after %+v, %v", got.Changed, len(got.Changes), got.After, err)
	}
}

func TestPreviewRejectsAnInvalidDraftWithoutReading(t *testing.T) {
	svc, _ := newService(t)
	draft := validDraft()
	draft.BlockThreshold = 10
	_, err := svc.Preview(context.Background(), draft)
	var invalid *ruleset.ValidationError
	if !errors.As(err, &invalid) || invalid.Fields["block_threshold"] == "" {
		t.Fatalf("got %v, want a validation error for block_threshold", err)
	}
}
