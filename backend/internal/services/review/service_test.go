package review_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review/mocks"
	"go.uber.org/mock/gomock"
)

func newService(t *testing.T) (*review.Service, *mocks.MockStore) {
	store := mocks.NewMockStore(gomock.NewController(t))
	return review.NewService(store), store
}

func TestDecideChecksTheDecisionBeforeSaving(t *testing.T) {
	tests := []struct {
		name        string
		decision    string
		blockDevice bool
		want        error
	}{
		{"unknown decision", "maybe", false, review.ErrInvalidDecision},
		{"empty decision", "", false, review.ErrInvalidDecision},
		{"block device on approve", "approve", true, review.ErrBlockDeviceNeedsReject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			_, err := svc.Decide(context.Background(), 1, tt.decision, "analyst-1", tt.blockDevice)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDecideMapsTheStoreStatus(t *testing.T) {
	tests := []struct {
		status models.ResolutionStatus
		want   error
	}{
		{models.ResolutionSaved, nil},
		{models.ResolutionNotFound, review.ErrPurchaseNotFound},
		{models.ResolutionNotReview, review.ErrNotUnderReview},
		{models.ResolutionAlreadyResolved, review.ErrAlreadyDecided},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Resolve(gomock.Any(), int64(1), "reject", "analyst-1", true).
				Return(models.Resolution{Status: tt.status, DeviceBlocked: true}, nil)

			got, err := svc.Decide(context.Background(), 1, "reject", "analyst-1", true)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.want == nil && !got.DeviceBlocked {
				t.Fatal("saved resolution lost DeviceBlocked")
			}
		})
	}

	t.Run("unknown status", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Resolve(gomock.Any(), int64(1), "approve", "analyst-1", false).Return(models.Resolution{Status: "unexpected"}, nil)
		if _, err := svc.Decide(context.Background(), 1, "approve", "analyst-1", false); err == nil {
			t.Fatal("an unknown store status was accepted")
		}
	})

	t.Run("store error", func(t *testing.T) {
		svc, store := newService(t)
		storeErr := errors.New("database unavailable")
		store.EXPECT().Resolve(gomock.Any(), int64(1), "approve", "analyst-1", false).Return(models.Resolution{}, storeErr)
		if _, err := svc.Decide(context.Background(), 1, "approve", "analyst-1", false); !errors.Is(err, storeErr) {
			t.Fatalf("err = %v, want the store error", err)
		}
	})
}

func queueItems(n int, at time.Time) []models.QueueItem {
	items := make([]models.QueueItem, n)
	for i := range items {
		items[i] = models.QueueItem{PurchaseID: int64(i + 1), RuleScore: 60, DecidedAt: at}
	}
	return items
}

func TestQueueReturnsOnePageAndTheNextCursor(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	after := &models.QueueCursor{DecidedAt: at, PurchaseID: 500, ScoreKey: -70}

	t.Run("full page has a next cursor", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Queue(gomock.Any(), review.PageSize+1, models.QueueByRuleScore, after).Return(queueItems(review.PageSize+1, at), nil)

		page, err := svc.Queue(context.Background(), models.QueueByRuleScore, after)
		if err != nil {
			t.Fatal(err)
		}
		want := models.QueueCursor{DecidedAt: at, PurchaseID: review.PageSize, ScoreKey: -60}
		if len(page.Items) != review.PageSize || page.Next == nil || *page.Next != want {
			t.Fatalf("page has %d items and next %+v, want %d and %+v", len(page.Items), page.Next, review.PageSize, want)
		}
	})

	t.Run("last page has none", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Queue(gomock.Any(), review.PageSize+1, models.QueueByModelScore, nil).Return(queueItems(3, at), nil)

		page, err := svc.Queue(context.Background(), models.QueueByModelScore, nil)
		if err != nil || len(page.Items) != 3 || page.Next != nil {
			t.Fatalf("last page = %d items, next %v, err %v", len(page.Items), page.Next, err)
		}
	})

	t.Run("store error", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Queue(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("database unavailable"))
		if _, err := svc.Queue(context.Background(), models.QueueByRuleScore, nil); err == nil {
			t.Fatal("a store error was not returned")
		}
	})
}

func TestCaseReturnsTheStoreResult(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Case(gomock.Any(), int64(9)).Return(models.CaseItem{}, models.ErrReviewNotFound)
	if _, err := svc.Case(context.Background(), 9); !errors.Is(err, models.ErrReviewNotFound) {
		t.Fatalf("err = %v, want ErrReviewNotFound", err)
	}
}

func TestCursorForModelOrderPutsMissingScoresLast(t *testing.T) {
	score := 0.8
	with := review.CursorFor(models.QueueByModelScore, models.QueueItem{ModelScore: &score})
	without := review.CursorFor(models.QueueByModelScore, models.QueueItem{})
	if with.ScoreKey != -0.8 || without.ScoreKey != 1 {
		t.Fatalf("score keys = %v and %v, want -0.8 and 1", with.ScoreKey, without.ScoreKey)
	}
}
