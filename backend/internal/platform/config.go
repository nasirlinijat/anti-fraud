package platform

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultPort           = "8080"
	defaultScoreRateLimit = 100
	defaultScoreRateBurst = 200
	defaultScoreTimeout   = 2 * time.Second
)

type ScoreLimits struct {
	RatePerSecond float64
	Burst         int
	Timeout       time.Duration
}

// Config holds the settings read from environment variables.
type Config struct {
	SimulationEnabled bool
	LabelsDatabaseURL string
	Port              string
	DatabaseURL       string
	// ModelPath is the fraud model file. When it is empty, scoring uses rules only.
	ModelPath string
	// APIKey authenticates requests to the score endpoint.
	APIKey       string
	AnalystKeys  AnalystKeys
	OperatorKeys OperatorKeys
	ScoreLimits  ScoreLimits
}

// LoadConfig reads the configuration from environment variables. It returns an
// error if a required variable is missing or empty.
func LoadConfig() (Config, error) {
	cfg := Config{
		Port:              os.Getenv("PORT"),
		LabelsDatabaseURL: os.Getenv("LABELS_DATABASE_URL"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		ModelPath:         os.Getenv("MODEL_PATH"),
		APIKey:            os.Getenv("API_KEY"),
	}
	if cfg.Port == "" {
		cfg.Port = defaultPort
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("required environment variable DATABASE_URL is not set")
	}
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("required environment variable API_KEY is not set")
	}
	if strings.IndexFunc(cfg.APIKey, isSpaceOrControl) >= 0 {
		return Config{}, fmt.Errorf("API_KEY must not contain whitespace or control characters")
	}
	if raw := os.Getenv("SIMULATION_ENABLED"); raw != "" {
		var err error
		cfg.SimulationEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SIMULATION_ENABLED must be a boolean")
		}
	}
	if cfg.SimulationEnabled && cfg.LabelsDatabaseURL == "" {
		return Config{}, fmt.Errorf("SIMULATION_ENABLED requires LABELS_DATABASE_URL")
	}
	keys, err := ParseAnalystKeys(os.Getenv("REVIEW_ANALYST_KEYS"), cfg.APIKey)
	if err != nil {
		return Config{}, err
	}
	cfg.AnalystKeys = keys
	cfg.OperatorKeys, err = ParseOperatorKeys(os.Getenv("OPERATOR_API_KEYS"), cfg.APIKey, keys)
	if err != nil {
		return Config{}, err
	}
	cfg.ScoreLimits, err = loadScoreLimits()
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadScoreLimits() (ScoreLimits, error) {
	limits := ScoreLimits{RatePerSecond: defaultScoreRateLimit, Burst: defaultScoreRateBurst, Timeout: defaultScoreTimeout}
	if raw := os.Getenv("SCORE_RATE_LIMIT"); raw != "" {
		rate, err := strconv.ParseFloat(raw, 64)
		if err != nil || rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return ScoreLimits{}, fmt.Errorf("SCORE_RATE_LIMIT must be a number of requests per second, 0 or more")
		}
		limits.RatePerSecond = rate
	}
	if raw := os.Getenv("SCORE_RATE_BURST"); raw != "" {
		burst, err := strconv.Atoi(raw)
		if err != nil || burst < 1 {
			return ScoreLimits{}, fmt.Errorf("SCORE_RATE_BURST must be a whole number of requests, 1 or more")
		}
		limits.Burst = burst
	}
	if raw := os.Getenv("SCORE_TIMEOUT"); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return ScoreLimits{}, fmt.Errorf("SCORE_TIMEOUT must be a positive duration such as 2s or 500ms")
		}
		limits.Timeout = timeout
	}
	return limits, nil
}

func isSpaceOrControl(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}
