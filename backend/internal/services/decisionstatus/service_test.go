package decisionstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/decisionstatus/mocks"
	"go.uber.org/mock/gomock"
)

func TestLookupSeparatesScoringAndReview(t *testing.T) {
	at := time.Date(2026, 11, 14, 18, 0, 0, 0, time.FixedZone("Baku", 14400))
	for _, tc := range []struct {
		outcome  scoring.Outcome
		decision string
		want     string
	}{
		{"allow", "", "not_required"}, {"block", "", "not_required"}, {"review", "", "pending"}, {"review", "approve", "approved"}, {"review", "reject", "rejected"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			store := mocks.NewMockStore(gomock.NewController(t))
			saved := models.SavedPurchaseStatus{TransactionID: "id", Outcome: tc.outcome}
			if tc.decision != "" {
				saved.ReviewDecision = &tc.decision
				saved.ResolvedAt = &at
			}
			store.EXPECT().Find(gomock.Any(), "id").Return(saved, nil)
			got, err := NewService(store).Lookup(context.Background(), "id")
			if err != nil || got.Outcome != tc.outcome || got.ReviewStatus != tc.want {
				t.Fatalf("%+v %v", got, err)
			}
			if tc.decision == "" && got.ResolvedAt != nil {
				t.Fatal("unexpected resolution time")
			}
			if tc.decision != "" && (got.ResolvedAt == nil || !got.ResolvedAt.Equal(at) || got.ResolvedAt.Location() != time.UTC) {
				t.Fatal("resolution must be UTC")
			}
		})
	}
}
func TestLookupPreservesStoreErrors(t *testing.T) {
	store := mocks.NewMockStore(gomock.NewController(t))
	store.EXPECT().Find(gomock.Any(), "missing").Return(models.SavedPurchaseStatus{}, models.ErrPurchaseNotFound)
	_, err := NewService(store).Lookup(context.Background(), "missing")
	if !errors.Is(err, models.ErrPurchaseNotFound) {
		t.Fatal(err)
	}
}

func TestLookupRejectsIncompleteSavedResolutions(t *testing.T) {
	for _, decision := range []string{"approve", "unexpected"} {
		t.Run(decision, func(t *testing.T) {
			store := mocks.NewMockStore(gomock.NewController(t))
			store.EXPECT().Find(gomock.Any(), "x").Return(models.SavedPurchaseStatus{Outcome: scoring.OutcomeReview, ReviewDecision: &decision}, nil)
			if _, err := NewService(store).Lookup(context.Background(), "x"); err == nil {
				t.Fatal("invalid saved resolution accepted")
			}
		})
	}
}
