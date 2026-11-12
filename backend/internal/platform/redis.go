package platform

import (
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisDialTimeout  = 200 * time.Millisecond
	redisReadTimeout  = 100 * time.Millisecond
	redisWriteTimeout = 100 * time.Millisecond
)

// NewRedis returns a client configured for blocklist cache lookups.
// It sets short timeouts so failures quickly fall back to PostgreSQL.
func NewRedis(rawURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid redis url")
	}
	opts.DialTimeout = redisDialTimeout
	opts.ReadTimeout = redisReadTimeout
	opts.WriteTimeout = redisWriteTimeout
	opts.MaxRetries = -1
	opts.DialerRetries = 1
	return redis.NewClient(opts), nil
}
