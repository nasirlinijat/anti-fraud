package stats_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats/mocks"
	"go.uber.org/mock/gomock"
)

func TestWindow(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		period    string
		wantSince time.Time
		wantWidth time.Duration
	}{
		{"15m", now.Add(-15 * time.Minute), time.Minute},
		{"1h", now.Add(-time.Hour), 5 * time.Minute},
		{"24h", now.Add(-24 * time.Hour), time.Hour},
		{"7d", now.Add(-7 * 24 * time.Hour), 6 * time.Hour},
		{"all", time.Unix(0, 0).UTC(), 0},
	}
	svc := stats.NewService(nil)
	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			got, err := svc.Window(tt.period, now)
			want := models.StatsWindow{Since: tt.wantSince, Until: now, BucketWidth: tt.wantWidth}
			if err != nil || got != want {
				t.Fatalf("Window = %+v, %v; want %+v", got, err, want)
			}
		})
	}
	if _, err := svc.Window("2d", now); !errors.Is(err, stats.ErrUnknownPeriod) {
		t.Fatalf("err = %v, want ErrUnknownPeriod", err)
	}
}

func TestSummaryAsksForTheTopTenRules(t *testing.T) {
	store := mocks.NewMockStore(gomock.NewController(t))
	window := models.StatsWindow{Since: time.Unix(0, 0).UTC(), Until: time.Now().UTC()}
	store.EXPECT().Summary(gomock.Any(), window, 10).Return(models.StatsSummary{ReviewQueue: 4}, nil)

	got, err := stats.NewService(store).Summary(context.Background(), window)
	if err != nil || got.ReviewQueue != 4 {
		t.Fatalf("Summary = %+v, %v", got, err)
	}
}
