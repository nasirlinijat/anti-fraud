package review_test

import (
	"context"
	"errors"
	"strings"
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
		name     string
		decision string
		blocks   models.ReviewBlocks
		want     error
	}{
		{"unknown decision", "maybe", models.ReviewBlocks{}, review.ErrInvalidDecision},
		{"empty decision", "", models.ReviewBlocks{}, review.ErrInvalidDecision},
		{"block device on approve", "approve", models.ReviewBlocks{Device: true}, review.ErrBlockNeedsReject},
		{"block card on approve", "approve", models.ReviewBlocks{Card: true}, review.ErrBlockNeedsReject},
		{"block player on approve", "approve", models.ReviewBlocks{Player: true}, review.ErrBlockNeedsReject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Resolve(gomock.Any(), gomock.Any()).Times(0)

			_, err := svc.Decide(context.Background(), 1, tt.decision, "analyst-1", tt.blocks)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDecideRejectsABlockWithApprove(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Resolve(gomock.Any(), gomock.Any()).Times(0)

	_, err := svc.Decide(context.Background(), 1, "approve", "rita", models.ReviewBlocks{Card: true, Player: true})

	if !errors.Is(err, review.ErrBlockNeedsReject) {
		t.Fatalf("err = %v, want ErrBlockNeedsReject", err)
	}
}

func TestDecidePassesTheBlocksToTheStore(t *testing.T) {
	blocks := models.ReviewBlocks{Device: true, Card: true, Player: true}

	t.Run("reviewer session", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Resolve(gomock.Any(), models.ReviewDecision{
			PurchaseID: 7, Decision: "reject", AnalystID: "key-analyst", Blocks: blocks,
		}).Return(models.Resolution{Status: models.ResolutionSaved, Blocked: models.ReviewBlocks{Card: true, Player: true}}, nil)

		got, err := svc.Decide(context.Background(), 7, "reject", "key-analyst", blocks)

		if err != nil || got.Blocked != (models.ReviewBlocks{Card: true, Player: true}) {
			t.Fatalf("Decide = %+v, %v; want card and player blocked", got, err)
		}
	})

	t.Run("session", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Resolve(gomock.Any(), models.ReviewDecision{
			PurchaseID: 7, Decision: "reject", AnalystID: "rita", Blocks: blocks,
		}).Return(models.Resolution{Status: models.ResolutionSaved, Blocked: blocks}, nil)

		got, err := svc.Decide(context.Background(), 7, "reject", "rita", blocks)

		if err != nil || got.Blocked != blocks {
			t.Fatalf("Decide = %+v, %v; want every value blocked", got, err)
		}
	})
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
			blocks := models.ReviewBlocks{Device: true}
			store.EXPECT().Resolve(gomock.Any(), models.ReviewDecision{PurchaseID: 1, Decision: "reject", AnalystID: "analyst-1", Blocks: blocks}).
				Return(models.Resolution{Status: tt.status, Blocked: blocks}, nil)

			got, err := svc.Decide(context.Background(), 1, "reject", "analyst-1", blocks)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.want == nil && !got.Blocked.Device {
				t.Fatal("saved resolution lost Blocked.Device")
			}
		})
	}

	t.Run("unknown status", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Resolve(gomock.Any(), models.ReviewDecision{PurchaseID: 1, Decision: "approve", AnalystID: "analyst-1"}).Return(models.Resolution{Status: "unexpected"}, nil)
		if _, err := svc.Decide(context.Background(), 1, "approve", "analyst-1", models.ReviewBlocks{}); err == nil {
			t.Fatal("an unknown store status was accepted")
		}
	})

	t.Run("store error", func(t *testing.T) {
		svc, store := newService(t)
		storeErr := errors.New("database unavailable")
		store.EXPECT().Resolve(gomock.Any(), models.ReviewDecision{PurchaseID: 1, Decision: "approve", AnalystID: "analyst-1"}).Return(models.Resolution{}, storeErr)
		if _, err := svc.Decide(context.Background(), 1, "approve", "analyst-1", models.ReviewBlocks{}); !errors.Is(err, storeErr) {
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
		store.EXPECT().Queue(gomock.Any(), review.PageSize+1, models.QueueByRuleScore, after, nil).Return(queueItems(review.PageSize+1, at), nil)

		page, err := svc.Queue(context.Background(), models.QueueByRuleScore, after, nil)
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
		store.EXPECT().Queue(gomock.Any(), review.PageSize+1, models.QueueByModelScore, nil, nil).Return(queueItems(3, at), nil)

		page, err := svc.Queue(context.Background(), models.QueueByModelScore, nil, nil)
		if err != nil || len(page.Items) != 3 || page.Next != nil {
			t.Fatalf("last page = %d items, next %v, err %v", len(page.Items), page.Next, err)
		}
	})

	t.Run("store error", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Queue(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("database unavailable"))
		if _, err := svc.Queue(context.Background(), models.QueueByRuleScore, nil, nil); err == nil {
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

func TestNetworkPurchasesChecksTheAnchorAndValueBeforeReading(t *testing.T) {
	tests := []struct {
		name   string
		anchor models.BlocklistType
		value  string
		want   error
	}{
		{"empty anchor", "", "player-2", review.ErrInvalidNetworkAnchor},
		{"ip anchor", models.BlocklistIP, "player-2", review.ErrInvalidNetworkAnchor},
		{"unknown anchor", "email", "player-2", review.ErrInvalidNetworkAnchor},
		{"empty value", models.BlocklistDevice, "", review.ErrInvalidLinkedValue},
		{"value over 256 bytes", models.BlocklistCard, strings.Repeat("a", 257), review.ErrInvalidLinkedValue},
		{"control character", models.BlocklistPlayer, "card\n2", review.ErrInvalidLinkedValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().NetworkPurchases(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			_, err := svc.NetworkPurchases(context.Background(), 7, tt.anchor, tt.value)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNetworkPurchasesReturnsTheStoreResult(t *testing.T) {
	longest := strings.Repeat("a", 256)
	want := models.LinkedPurchases{Anchor: models.BlocklistCard, Value: longest, WindowDays: 30, Total: 1,
		Purchases: []models.LinkedPurchase{{PurchaseID: 3, TransactionID: "tx-3"}}}

	t.Run("linked purchases", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().NetworkPurchases(gomock.Any(), int64(7), models.BlocklistCard, longest).Return(want, nil)

		got, err := svc.NetworkPurchases(context.Background(), 7, models.BlocklistCard, longest)

		if err != nil || got.Total != 1 || len(got.Purchases) != 1 || got.Purchases[0].TransactionID != "tx-3" {
			t.Fatalf("NetworkPurchases = %+v, %v; want %+v", got, err, want)
		}
	})

	t.Run("store error", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().NetworkPurchases(gomock.Any(), int64(7), models.BlocklistDevice, "player-2").Return(models.LinkedPurchases{}, models.ErrReviewNotFound)

		_, err := svc.NetworkPurchases(context.Background(), 7, models.BlocklistDevice, "player-2")

		if !errors.Is(err, models.ErrReviewNotFound) {
			t.Fatalf("err = %v, want ErrReviewNotFound", err)
		}
	})
}

func TestCursorForModelOrderPutsMissingScoresLast(t *testing.T) {
	score := 0.8
	with := review.CursorFor(models.QueueByModelScore, models.QueueItem{ModelScore: &score})
	without := review.CursorFor(models.QueueByModelScore, models.QueueItem{})
	if with.ScoreKey != -0.8 || without.ScoreKey != 1 {
		t.Fatalf("score keys = %v and %v, want -0.8 and 1", with.ScoreKey, without.ScoreKey)
	}
}

func TestDecideRequiresTheUsernameToHoldTheClaim(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Resolve(gomock.Any(), models.ReviewDecision{
		PurchaseID: 7, Decision: "approve", AnalystID: "rita",
	}).Return(models.Resolution{Status: models.ResolutionSaved}, nil)

	if _, err := svc.Decide(context.Background(), 7, "approve", "rita", models.ReviewBlocks{}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
}

func TestDecideReportsAMissingClaim(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Resolve(gomock.Any(), gomock.Any()).Return(models.Resolution{Status: models.ResolutionNotClaimed}, nil)

	_, err := svc.Decide(context.Background(), 7, "reject", "rita", models.ReviewBlocks{})

	if !errors.Is(err, review.ErrNotClaimed) {
		t.Fatalf("err = %v, want ErrNotClaimed", err)
	}
}

func TestClaimMapsTheStoreStatus(t *testing.T) {
	tests := []struct {
		status models.ClaimStatus
		want   error
	}{
		{models.ClaimSaved, nil},
		{models.ClaimAlreadyYours, nil},
		{models.ClaimNotFound, review.ErrPurchaseNotFound},
		{models.ClaimNotReview, review.ErrNotUnderReview},
		{models.ClaimAlreadyResolved, review.ErrAlreadyDecided},
		{models.ClaimHeldByOther, review.ErrClaimedByOther},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Claim(gomock.Any(), int64(7), "rita").Return(models.Claim{Status: tt.status, ClaimedBy: "rita"}, nil)

			got, err := svc.Claim(context.Background(), 7, "rita")

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.want == nil && got.ClaimedBy != "rita" {
				t.Fatalf("claim = %+v, want rita", got)
			}
		})
	}

	t.Run("unknown status", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().Claim(gomock.Any(), int64(7), "rita").Return(models.Claim{Status: "unexpected"}, nil)
		if _, err := svc.Claim(context.Background(), 7, "rita"); err == nil {
			t.Fatal("an unknown store status was accepted")
		}
	})
}

func TestQueuePassesTheClaimFilter(t *testing.T) {
	username := "rita"
	svc, store := newService(t)
	store.EXPECT().Queue(gomock.Any(), review.PageSize+1, models.QueueByRuleScore, nil, &username).Return(nil, nil)

	if _, err := svc.Queue(context.Background(), models.QueueByRuleScore, nil, &username); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseClaimMapsTheStoreStatus(t *testing.T) {
	tests := []struct {
		status models.ReleaseStatus
		want   error
	}{
		{models.ReleaseSaved, nil},
		{models.ReleaseNoOpenClaim, review.ErrNoOpenClaim},
		{models.ReleaseAlreadyResolved, review.ErrAlreadyDecided},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().ReleaseClaim(gomock.Any(), int64(7)).Return(tt.status, nil)

			err := svc.ReleaseClaim(context.Background(), 7)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}

	t.Run("unknown status", func(t *testing.T) {
		svc, store := newService(t)
		store.EXPECT().ReleaseClaim(gomock.Any(), int64(7)).Return(models.ReleaseStatus("unexpected"), nil)
		if err := svc.ReleaseClaim(context.Background(), 7); err == nil {
			t.Fatal("an unknown store status was accepted")
		}
	})
}
