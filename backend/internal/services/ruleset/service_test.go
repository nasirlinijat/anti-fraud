package ruleset_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset/mocks"
	"go.uber.org/mock/gomock"
)

func ptr[T any](v T) *T { return &v }

func newService(t *testing.T) (*ruleset.Service, *mocks.MockStore) {
	store := mocks.NewMockStore(gomock.NewController(t))
	return ruleset.NewService(store), store
}

func validDraft() ruleset.Draft {
	return ruleset.Draft{
		ReviewThreshold: 50,
		BlockThreshold:  80,
		Note:            "  raise the high amount weight  ",
		Rules: map[scoring.RuleName]*ruleset.DraftRule{
			scoring.RuleHighAmount: {Enabled: ptr(true), Weight: ptr(int32(30)), Limit: ptr(int64(50000))},
		},
	}
}

func TestPublishRejectsAnInvalidDraftBeforeSaving(t *testing.T) {
	tests := []struct {
		name       string
		change     func(d *ruleset.Draft)
		wantFields []string
	}{
		{"block threshold not above review", func(d *ruleset.Draft) { d.BlockThreshold = 40 }, []string{"block_threshold"}},
		{"zero review threshold", func(d *ruleset.Draft) { d.ReviewThreshold = 0 }, []string{"review_threshold"}},
		{"unknown rule", func(d *ruleset.Draft) { d.Rules["no_such_rule"] = &ruleset.DraftRule{} }, []string{"rules.no_such_rule"}},
		{"missing rules", func(d *ruleset.Draft) { d.Rules = nil }, []string{"rules"}},
		{"rule without enabled and weight", func(d *ruleset.Draft) {
			d.Rules[scoring.RuleHighAmount] = &ruleset.DraftRule{Limit: ptr(int64(1))}
		}, []string{"rules.high_amount.enabled", "rules.high_amount.weight"}},
		{"spike settings on another rule", func(d *ruleset.Draft) {
			d.Rules[scoring.RuleHighAmount].WindowDays = ptr(int32(30))
		}, []string{"rules.high_amount"}},
		{"model thresholds out of order", func(d *ruleset.Draft) {
			d.Model = &ruleset.DraftModel{Mode: scoring.ModelAssist, ClearBelow: ptr(0.5), FlagAt: ptr(0.2)}
		}, []string{"model.clear_below"}},
		{"unknown model mode", func(d *ruleset.Draft) { d.Model = &ruleset.DraftModel{Mode: "always"} }, []string{"model.mode"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			d := validDraft()
			tt.change(&d)

			_, err := svc.Publish(context.Background(), d, "operator-1")
			var invalid *ruleset.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			for _, field := range tt.wantFields {
				if invalid.Fields[field] == "" {
					t.Errorf("no problem for %s in %v", field, invalid.Fields)
				}
			}
		})
	}
}

func TestPublishSavesTheConvertedRuleset(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Publish(gomock.Any(), gomock.Any(), "operator-1", (*scoring.ModelSettings)(nil)).DoAndReturn(
		func(_ context.Context, rs scoring.Ruleset, _ string, _ *scoring.ModelSettings) (scoring.Ruleset, error) {
			if rs.Note != "raise the high amount weight" || rs.ReviewThreshold != 50 || rs.BlockThreshold != 80 {
				t.Errorf("saved %+v, want the trimmed note and the thresholds", rs)
			}
			if rule := rs.Rules[scoring.RuleHighAmount]; !rule.Enabled || rule.Weight != 30 || rule.Limit != 50000 {
				t.Errorf("high_amount = %+v", rule)
			}
			rs.Version = 8
			return rs, nil
		})

	got, err := svc.Publish(context.Background(), validDraft(), "operator-1")
	if err != nil || got.Version != 8 {
		t.Fatalf("Publish = version %d, %v; want version 8", got.Version, err)
	}
}

func TestPublishPassesTheModelReportThresholds(t *testing.T) {
	svc, store := newService(t)
	svc.UseModelThresholds(scoring.ModelPolicy{ClearBelow: 0.02, FlagAt: 0.4, PlayerBlendWeight: 0.3})
	report := gomock.Cond(func(s *scoring.ModelSettings) bool {
		return s != nil && *s.ClearBelow == 0.02 && *s.FlagAt == 0.4
	})
	store.EXPECT().Publish(gomock.Any(), gomock.Any(), "operator-1", report).Return(scoring.Ruleset{Version: 9}, nil)

	if _, err := svc.Publish(context.Background(), validDraft(), "operator-1"); err != nil {
		t.Fatal(err)
	}
}

func TestPublishReturnsAStoreError(t *testing.T) {
	svc, store := newService(t)
	storeErr := errors.New("database unavailable")
	store.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(scoring.Ruleset{}, storeErr)
	if _, err := svc.Publish(context.Background(), validDraft(), "operator-1"); !errors.Is(err, storeErr) {
		t.Fatalf("err = %v, want the store error", err)
	}
}

func TestReadsGoToTheStore(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Active(gomock.Any()).Return(scoring.Ruleset{Version: 4}, nil)
	store.EXPECT().Version(gomock.Any(), int32(99)).Return(scoring.Ruleset{}, models.ErrRulesetNotFound)
	store.EXPECT().List(gomock.Any()).Return([]models.RulesetSummary{{Version: 4}}, nil)

	if rs, err := svc.Active(context.Background()); err != nil || rs.Version != 4 {
		t.Fatalf("Active = %d, %v", rs.Version, err)
	}
	if _, err := svc.Version(context.Background(), 99); !errors.Is(err, models.ErrRulesetNotFound) {
		t.Fatalf("Version err = %v, want ErrRulesetNotFound", err)
	}
	if list, err := svc.List(context.Background()); err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
}
