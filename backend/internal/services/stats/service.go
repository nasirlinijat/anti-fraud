package stats

import (
	"context"
	"errors"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

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
)

var ErrUnknownPeriod = errors.New("must be one of 15m, 1h, 24h, 7d, all")

type Store interface {
	Summary(ctx context.Context, window models.StatsWindow, maxRules int) (models.StatsSummary, error)
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
