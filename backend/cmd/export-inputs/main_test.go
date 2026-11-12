package main

import (
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/trainingdata"
)

var now = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func TestLabelFlagsDefaultToNowAndTheDefaultWindow(t *testing.T) {
	opts, err := labelFlags{maturityDays: trainingdata.DefaultMaturityDays}.options(now)

	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if !opts.AsOf.Equal(now) || opts.MaturityDays != 60 || !opts.Since.IsZero() || !opts.Until.IsZero() {
		t.Errorf("options = %+v, want as-of now, 60 days and no range", opts)
	}
}

func TestLabelFlagsParseTheTimesInUTC(t *testing.T) {
	f := labelFlags{asOf: "2026-11-01T04:00:00+04:00", since: "2026-06-01T00:00:00Z", until: "2026-09-01T00:00:00Z", maturityDays: 0}

	opts, err := f.options(now)

	if err != nil {
		t.Fatalf("options: %v", err)
	}
	want := trainingdata.LabelOptions{
		AsOf:         time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		MaturityDays: 0,
		Since:        time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Until:        time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	if opts != want {
		t.Errorf("options = %+v, want %+v", opts, want)
	}
}

func TestLabelFlagsRejectInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		flags labelFlags
		want  string
	}{
		{"as-of in the future", labelFlags{asOf: "2026-11-12T10:00:01Z", maturityDays: 60}, "in the future"},
		{"as-of not RFC 3339", labelFlags{asOf: "yesterday", maturityDays: 60}, "as-of must be RFC 3339"},
		{"negative maturity window", labelFlags{maturityDays: -1}, "must not be negative"},
		{"since not RFC 3339", labelFlags{since: "2026-06-01", maturityDays: 60}, "since must be RFC 3339"},
		{"until not after since", labelFlags{since: "2026-06-01T00:00:00Z", until: "2026-06-01T00:00:00Z", maturityDays: 60}, "until must be after since"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.flags.options(now)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}
