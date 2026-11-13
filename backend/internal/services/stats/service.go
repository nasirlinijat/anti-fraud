package stats

import (
	"context"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

//go:generate mockgen -destination=mocks/mock_store.go -package=mocks . Store

var periods = map[string]struct{ length, bucketWidth time.Duration }{
	"15m": {15 * time.Minute, time.Minute},
	"1h":  {time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, 6 * time.Hour},
	"all": {},
}

const (
	DefaultPeriod = "24h"
	maxRules      = 10
	// maxRecentDecisions bounds a recent pipeline count, so a console that was
	// away skips ahead instead of making a large query.
	maxRecentDecisions = 5000
)

type Store interface {
	Summary(ctx context.Context, window models.StatsWindow, maxRules int) (models.StatsSummary, error)
	RecentPipeline(ctx context.Context, after int64, limit int) (models.RecentPipeline, error)
	LatestDecisionID(ctx context.Context) (int64, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// Window returns the decision window of a dashboard period ending at now. The
// "all" period starts at the Unix epoch and lets the store choose the bucket width.
func (s *Service) Window(period string, now time.Time) (models.StatsWindow, error) {
	settings, ok := periods[period]
	if !ok {
		return models.StatsWindow{}, ErrUnknownPeriod
	}
	since := time.Unix(0, 0).UTC()
	if settings.length > 0 {
		since = now.Add(-settings.length)
	}
	return models.StatsWindow{Since: since, Until: now, BucketWidth: settings.bucketWidth}, nil
}

func (s *Service) Summary(ctx context.Context, window models.StatsWindow) (models.StatsSummary, error) {
	return s.store.Summary(ctx, window, maxRules)
}

// Recent counts the decisions after a purchase ID. Without one it counts
// nothing and returns the latest decision ID, so the caller starts from now.
func (s *Service) Recent(ctx context.Context, after *int64) (models.RecentPipeline, error) {
	if after == nil {
		latest, err := s.store.LatestDecisionID(ctx)
		return models.RecentPipeline{NextAfter: latest}, err
	}
	recent, err := s.store.RecentPipeline(ctx, *after, maxRecentDecisions)
	recent.After = after
	return recent, err
}
