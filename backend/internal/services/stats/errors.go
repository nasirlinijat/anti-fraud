package stats

import "errors"

var ErrUnknownPeriod = errors.New("must be one of 15m, 1h, 24h, 7d, all")
