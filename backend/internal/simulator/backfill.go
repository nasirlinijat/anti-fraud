package simulator

import (
	"context"
	"fmt"
	"time"
)

// GenerateUntil generates a backfill from cfg.Start up to, but excluding, end.
func GenerateUntil(ctx context.Context, cfg Config, end time.Time) ([]Purchase, error) {
	if !end.After(cfg.Start) {
		return nil, fmt.Errorf("backfill end must be after the start")
	}
	g, err := newGenerator(ctx, cfg, false)
	if err != nil {
		return nil, err
	}
	var purchases []Purchase
	for p := range g.purchases(ctx) {
		if !p.Request.EventTime.Before(end) {
			break
		}
		purchases = append(purchases, p)
	}
	return purchases, ctx.Err()
}
