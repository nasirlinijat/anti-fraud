package review_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"go.uber.org/mock/gomock"
)

var errStoreDown = errors.New("database is down")

func purchaseIDs(n int) []int64 {
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return ids
}

func TestBatchesCheckTheListBeforeAnyStoreCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []int64
		want error
	}{
		{"empty", nil, review.ErrInvalidBatchSize},
		{"more than 50", purchaseIDs(51), review.ErrInvalidBatchSize},
		{"duplicate", []int64{4, 5, 4}, review.ErrDuplicatePurchaseID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Claim(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			store.EXPECT().Resolve(gomock.Any(), gomock.Any()).Times(0)
			ctx := context.Background()

			_, claimErr := svc.ClaimMany(ctx, tc.ids, "rita")
			_, keyErr := svc.DecideMany(ctx, review.BatchDecision{PurchaseIDs: tc.ids, Decision: "approve", AnalystID: "analyst-1"})
			_, sessionErr := svc.DecideMany(ctx, review.BatchDecision{PurchaseIDs: tc.ids, Decision: "approve", AnalystID: "rita"})

			for _, err := range []error{claimErr, keyErr, sessionErr} {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			}
		})
	}
}

func TestBatchesAcceptFiftyPurchases(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Claim(gomock.Any(), gomock.Any(), "rita").Return(models.Claim{Status: models.ClaimSaved, ClaimedBy: "rita"}, nil).Times(50)

	results, err := svc.ClaimMany(context.Background(), purchaseIDs(50), "rita")

	if err != nil || len(results) != 50 {
		t.Fatalf("ClaimMany = %d results, %v; want 50", len(results), err)
	}
}

func TestDecideManyChecksTheDecisionBeforeAnyStoreCall(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision string
		blocks   models.ReviewBlocks
		want     error
	}{
		{"unknown decision", "maybe", models.ReviewBlocks{}, review.ErrInvalidDecision},
		{"block device on approve", "approve", models.ReviewBlocks{Device: true}, review.ErrBlockNeedsReject},
		{"block card on approve", "approve", models.ReviewBlocks{Card: true}, review.ErrBlockNeedsReject},
		{"block player on approve", "approve", models.ReviewBlocks{Player: true}, review.ErrBlockNeedsReject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Resolve(gomock.Any(), gomock.Any()).Times(0)
			d := review.BatchDecision{PurchaseIDs: []int64{1, 2}, Decision: tc.decision, AnalystID: "rita", Blocks: tc.blocks}

			_, keyErr := svc.DecideMany(context.Background(), d)
			_, sessionErr := svc.DecideMany(context.Background(), d)

			if !errors.Is(keyErr, tc.want) || !errors.Is(sessionErr, tc.want) {
				t.Fatalf("errors = %v and %v, want %v", keyErr, sessionErr, tc.want)
			}
		})
	}
}

func TestClaimManyReportsEachPurchase(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	svc, store := newService(t)
	claims := []struct {
		id    int64
		claim models.Claim
		err   error
	}{
		{1, models.Claim{Status: models.ClaimSaved, ClaimedBy: "rita", ClaimedAt: at}, nil},
		{2, models.Claim{Status: models.ClaimAlreadyYours, ClaimedBy: "rita", ClaimedAt: at}, nil},
		{3, models.Claim{Status: models.ClaimHeldByOther, ClaimedBy: "sam", ClaimedAt: at}, nil},
		{4, models.Claim{Status: models.ClaimAlreadyResolved}, nil},
		{5, models.Claim{Status: models.ClaimNotFound}, nil},
		{6, models.Claim{Status: models.ClaimNotReview}, nil},
		{7, models.Claim{}, errStoreDown},
		{8, models.Claim{Status: "unexpected"}, nil},
		{9, models.Claim{Status: models.ClaimSaved, ClaimedBy: "rita", ClaimedAt: at}, nil},
	}
	var calls []any
	for _, c := range claims {
		calls = append(calls, store.EXPECT().Claim(gomock.Any(), c.id, "rita").Return(c.claim, c.err))
	}
	gomock.InOrder(calls...)

	results, err := svc.ClaimMany(context.Background(), []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}, "rita")

	if err != nil {
		t.Fatal(err)
	}
	rita := models.Claim{Status: models.ClaimSaved, ClaimedBy: "rita", ClaimedAt: at}
	want := []review.ClaimResult{
		{PurchaseID: 1, Status: review.BatchClaimed, Claim: rita},
		{PurchaseID: 2, Status: review.BatchAlreadyYours, Claim: models.Claim{Status: models.ClaimAlreadyYours, ClaimedBy: "rita", ClaimedAt: at}},
		{PurchaseID: 3, Status: review.BatchHeldByOther, Claim: models.Claim{Status: models.ClaimHeldByOther, ClaimedBy: "sam", ClaimedAt: at}},
		{PurchaseID: 4, Status: review.BatchAlreadyDecided},
		{PurchaseID: 5, Status: review.BatchNotFound},
		{PurchaseID: 6, Status: review.BatchNotReview},
		{PurchaseID: 7, Status: review.BatchError, Err: errStoreDown},
		{PurchaseID: 8, Status: review.BatchError},
		{PurchaseID: 9, Status: review.BatchClaimed, Claim: rita},
	}
	if results[7].Err == nil {
		t.Fatal("an unknown store status has no error")
	}
	results[7].Err = nil
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results =\n%+v\nwant\n%+v", results, want)
	}
}

func TestDecideManyReportsEachPurchase(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	svc, store := newService(t)
	statuses := []struct {
		id     int64
		status models.ResolutionStatus
		err    error
	}{
		{1, models.ResolutionSaved, nil},
		{2, models.ResolutionAlreadyResolved, nil},
		{3, models.ResolutionNotClaimed, nil},
		{4, models.ResolutionNotFound, nil},
		{5, models.ResolutionNotReview, nil},
		{6, "", errStoreDown},
		{7, models.ResolutionSaved, nil},
	}
	saved := models.Resolution{Status: models.ResolutionSaved, ResolvedAt: at, Blocked: models.ReviewBlocks{Card: true}}
	var calls []any
	for _, s := range statuses {
		resolution := models.Resolution{Status: s.status}
		if s.status == models.ResolutionSaved {
			resolution = saved
		}
		d := models.ReviewDecision{PurchaseID: s.id, Decision: "reject", AnalystID: "rita", Blocks: models.ReviewBlocks{Card: true}}
		calls = append(calls, store.EXPECT().Resolve(gomock.Any(), gomock.Cond(func(got models.ReviewDecision) bool {
			return got.PurchaseID == d.PurchaseID && got.Decision == d.Decision && got.AnalystID == d.AnalystID && got.Blocks == d.Blocks
		})).Return(resolution, s.err))
	}
	gomock.InOrder(calls...)

	results, err := svc.DecideMany(context.Background(), review.BatchDecision{
		PurchaseIDs: []int64{1, 2, 3, 4, 5, 6, 7}, Decision: "reject", AnalystID: "rita", Blocks: models.ReviewBlocks{Card: true},
	})

	if err != nil {
		t.Fatal(err)
	}
	want := []review.DecisionResult{
		{PurchaseID: 1, Status: review.BatchSaved, Resolution: saved},
		{PurchaseID: 2, Status: review.BatchAlreadyDecided},
		{PurchaseID: 3, Status: review.BatchNotClaimed},
		{PurchaseID: 4, Status: review.BatchNotFound},
		{PurchaseID: 5, Status: review.BatchNotReview},
		{PurchaseID: 6, Status: review.BatchError, Err: errStoreDown},
		{PurchaseID: 7, Status: review.BatchSaved, Resolution: saved},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results =\n%+v\nwant\n%+v", results, want)
	}
}

func TestBatchesStopTryingAfterTheRequestIsCancelled(t *testing.T) {
	svc, store := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	store.EXPECT().Claim(gomock.Any(), int64(1), "rita").DoAndReturn(func(context.Context, int64, string) (models.Claim, error) {
		cancel()
		return models.Claim{Status: models.ClaimSaved, ClaimedBy: "rita"}, nil
	})

	results, err := svc.ClaimMany(ctx, []int64{1, 2, 3}, "rita")

	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != review.BatchClaimed {
		t.Fatalf("first result = %+v, want claimed", results[0])
	}
	for _, result := range results[1:] {
		if result.Status != review.BatchError || !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("result after cancel = %+v, want an error with context.Canceled", result)
		}
	}
}

func ptr(s string) *string {
	return &s
}
