package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

type fakeStore struct {
	items      []models.QueueItem
	limit      int
	resolution models.Resolution
	resolved   bool
}

func (f *fakeStore) Queue(_ context.Context, limit int, _ models.QueueOrder, _ *models.QueueCursor) ([]models.QueueItem, error) {
	f.limit = limit
	return f.items, nil
}

func (f *fakeStore) Case(context.Context, int64) (models.CaseItem, error) {
	return models.CaseItem{}, nil
}

func (f *fakeStore) Resolve(context.Context, int64, string, string, bool) (models.Resolution, error) {
	f.resolved = true
	return f.resolution, nil
}

func TestDecideChecksTheDecisionBeforeSaving(t *testing.T) {
	for _, tc := range []struct {
		name        string
		decision    string
		blockDevice bool
		want        error
	}{
		{"unknown decision", "maybe", false, ErrInvalidDecision},
		{"block device on approve", "approve", true, ErrBlockDeviceNeedsReject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			_, err := NewService(store).Decide(context.Background(), 1, tc.decision, "analyst-1", tc.blockDevice)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if store.resolved {
				t.Fatal("an invalid decision reached the store")
			}
		})
	}
}

func TestDecideMapsTheStoreStatus(t *testing.T) {
	for _, tc := range []struct {
		status models.ResolutionStatus
		want   error
	}{
		{models.ResolutionSaved, nil},
		{models.ResolutionNotFound, ErrPurchaseNotFound},
		{models.ResolutionNotReview, ErrNotUnderReview},
		{models.ResolutionAlreadyResolved, ErrAlreadyDecided},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			store := &fakeStore{resolution: models.Resolution{Status: tc.status, DeviceBlocked: true}}
			got, err := NewService(store).Decide(context.Background(), 1, "reject", "analyst-1", true)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == nil && !got.DeviceBlocked {
				t.Fatal("saved resolution lost DeviceBlocked")
			}
		})
	}
	store := &fakeStore{resolution: models.Resolution{Status: "unexpected"}}
	if _, err := NewService(store).Decide(context.Background(), 1, "approve", "analyst-1", false); err == nil {
		t.Fatal("an unknown store status was accepted")
	}
}

func TestQueueReturnsOnePageAndTheNextCursor(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	items := make([]models.QueueItem, PageSize+1)
	for i := range items {
		items[i] = models.QueueItem{PurchaseID: int64(i + 1), RuleScore: 60, DecidedAt: at}
	}
	store := &fakeStore{items: items}
	page, err := NewService(store).Queue(context.Background(), models.QueueByRuleScore, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.limit != PageSize+1 || len(page.Items) != PageSize {
		t.Fatalf("asked for %d, returned %d items", store.limit, len(page.Items))
	}
	want := models.QueueCursor{DecidedAt: at, PurchaseID: PageSize, ScoreKey: -60}
	if page.Next == nil || *page.Next != want {
		t.Fatalf("next cursor = %+v, want %+v", page.Next, want)
	}

	store.items = items[:3]
	page, err = NewService(store).Queue(context.Background(), models.QueueByRuleScore, nil)
	if err != nil || len(page.Items) != 3 || page.Next != nil {
		t.Fatalf("last page = %d items, next %v, err %v", len(page.Items), page.Next, err)
	}
}

func TestCursorForModelOrderPutsMissingScoresLast(t *testing.T) {
	score := 0.8
	with := CursorFor(models.QueueByModelScore, models.QueueItem{ModelScore: &score})
	without := CursorFor(models.QueueByModelScore, models.QueueItem{})
	if with.ScoreKey != -0.8 || without.ScoreKey != 1 {
		t.Fatalf("score keys = %v and %v, want -0.8 and 1", with.ScoreKey, without.ScoreKey)
	}
}
