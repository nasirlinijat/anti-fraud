package ruleset

import (
	"context"
	"errors"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

type fakeStore struct {
	published *scoring.Ruleset
	report    *scoring.ModelSettings
}

func (f *fakeStore) Active(context.Context) (scoring.Ruleset, error) {
	return scoring.Ruleset{}, nil
}

func (f *fakeStore) Version(context.Context, int32) (scoring.Ruleset, error) {
	return scoring.Ruleset{}, nil
}

func (f *fakeStore) List(context.Context) ([]models.RulesetSummary, error) {
	return nil, nil
}

func (f *fakeStore) Publish(_ context.Context, rs scoring.Ruleset, _ string, report *scoring.ModelSettings) (scoring.Ruleset, error) {
	f.published, f.report = &rs, report
	return rs, nil
}

func ptr[T any](v T) *T { return &v }

func validDraft() Draft {
	return Draft{
		ReviewThreshold: 50,
		BlockThreshold:  80,
		Note:            "  raise the high amount weight  ",
		Rules: map[scoring.RuleName]*DraftRule{
			scoring.RuleHighAmount: {Enabled: ptr(true), Weight: ptr(int32(30)), Limit: ptr(int64(50000))},
		},
	}
}

func TestPublishRejectsAnInvalidDraftBeforeSaving(t *testing.T) {
	store := &fakeStore{}
	d := validDraft()
	d.BlockThreshold = 40
	d.Rules["no_such_rule"] = &DraftRule{}
	_, err := NewService(store).Publish(context.Background(), d, "operator-1")
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	if invalid.Fields["block_threshold"] == "" || invalid.Fields["rules.no_such_rule"] == "" {
		t.Fatalf("fields = %v", invalid.Fields)
	}
	if store.published != nil {
		t.Fatal("an invalid draft reached the store")
	}
}

func TestPublishPassesTheModelReportThresholds(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)
	if _, err := svc.Publish(context.Background(), validDraft(), "operator-1"); err != nil {
		t.Fatal(err)
	}
	if store.report != nil {
		t.Fatalf("report thresholds = %+v without a model, want none", store.report)
	}
	if store.published.Note != "raise the high amount weight" {
		t.Fatalf("note = %q, want it trimmed", store.published.Note)
	}

	svc.UseModelThresholds(scoring.ModelPolicy{ClearBelow: 0.02, FlagAt: 0.4, PlayerBlendWeight: 0.3})
	if _, err := svc.Publish(context.Background(), validDraft(), "operator-1"); err != nil {
		t.Fatal(err)
	}
	if store.report == nil || *store.report.ClearBelow != 0.02 || *store.report.FlagAt != 0.4 {
		t.Fatalf("report thresholds = %+v, want 0.02 and 0.4", store.report)
	}
}
