package score_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"go.uber.org/mock/gomock"
)

type recordedMetrics struct {
	decisions []models.Decision
}

func (r *recordedMetrics) Decided(d models.Decision) {
	r.decisions = append(r.decisions, d)
}

func TestMetricsCountOnlyDecisionsThisRequestSaved(t *testing.T) {
	t.Run("new decision", func(t *testing.T) {
		d := newScoreDeps(t)
		recorded := &recordedMetrics{}
		d.service.UseMetrics(recorded)
		p := purchaseOf(20_000)
		d.expectUnsaved(p, rulesetWith(scoring.ModelOff), 0)
		d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

		got, err := d.service.Score(context.Background(), p, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(recorded.decisions) != 1 || recorded.decisions[0].Result.Outcome != got.Result.Outcome || recorded.decisions[0].RulesetVersion != 7 {
			t.Fatalf("recorded %+v, want the one saved decision", recorded.decisions)
		}
	})

	t.Run("retry of a saved decision", func(t *testing.T) {
		d := newScoreDeps(t)
		recorded := &recordedMetrics{}
		d.service.UseMetrics(recorded)
		p := purchaseOf(100)
		saved := models.Decision{RulesetVersion: 3, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}
		d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(&saved, nil)

		if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(recorded.decisions) != 0 {
			t.Fatalf("a retry was counted again: %+v", recorded.decisions)
		}
	})

	t.Run("another request saved it first", func(t *testing.T) {
		d := newScoreDeps(t)
		recorded := &recordedMetrics{}
		d.service.UseMetrics(recorded)
		p := purchaseOf(100)
		d.expectUnsaved(p, rulesetWith(scoring.ModelOff), 0)
		winner := models.Decision{RulesetVersion: 6, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}
		gomock.InOrder(
			d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(0), models.ErrDuplicateTransaction),
			d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(&winner, nil),
		)
		if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(recorded.decisions) != 0 {
			t.Fatalf("the losing request counted the winner's decision: %+v", recorded.decisions)
		}
	})

	t.Run("save failed", func(t *testing.T) {
		d := newScoreDeps(t)
		recorded := &recordedMetrics{}
		d.service.UseMetrics(recorded)
		p := purchaseOf(100)
		d.expectUnsaved(p, rulesetWith(scoring.ModelOff), 0)
		d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(0), errors.New("database down"))
		if _, err := d.service.Score(context.Background(), p, time.Now()); err == nil {
			t.Fatal("a failed save was not returned")
		}
		if len(recorded.decisions) != 0 {
			t.Fatalf("an unsaved decision was counted: %+v", recorded.decisions)
		}
	})
}
