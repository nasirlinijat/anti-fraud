package main

import (
	"log/slog"

	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
)

// useIPIntel gives the score service the configured IP intelligence provider.
func useIPIntel(cfg platform.IPIntelConfig, service *scoresvc.Service) error {
	mock, err := newIPIntel(cfg)
	if err != nil || mock == nil {
		return err
	}
	service.UseIPIntel(mock)
	return nil
}

// newIPIntel returns the configured mock, or nil when IP intelligence is off.
// A mock ranges file replaces the default ranges.
func newIPIntel(cfg platform.IPIntelConfig) (*ipintel.Mock, error) {
	if cfg.Provider == "" {
		slog.Info("IP_INTEL_PROVIDER is off, so purchases are scored without IP intelligence")
		return nil, nil
	}
	ranges := ipintel.DefaultRanges
	if cfg.MockRangesPath != "" {
		var err error
		if ranges, err = ipintel.LoadRanges(cfg.MockRangesPath); err != nil {
			return nil, err
		}
	}
	slog.Info("IP intelligence enabled", "provider", ipintel.MockName, "ranges", len(ranges))
	return ipintel.NewMock(ranges), nil
}
