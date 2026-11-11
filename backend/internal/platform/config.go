package platform

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

const defaultPort = "8080"

// Config holds the settings read from environment variables.
type Config struct {
	SimulationEnabled bool
	Port              string
	DatabaseURL       string
	// ModelPath is the fraud model file. When it is empty, scoring uses rules only.
	ModelPath string
	// APIKey authenticates requests to the score endpoint.
	APIKey       string
	AnalystKeys  AnalystKeys
	OperatorKeys OperatorKeys
}

// LoadConfig reads the configuration from environment variables. It returns an
// error if a required variable is missing or empty.
func LoadConfig() (Config, error) {
	cfg := Config{
		Port:        os.Getenv("PORT"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		ModelPath:   os.Getenv("MODEL_PATH"),
		APIKey:      os.Getenv("API_KEY"),
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
	keys, err := ParseAnalystKeys(os.Getenv("REVIEW_ANALYST_KEYS"), cfg.APIKey)
	if err != nil {
		return Config{}, err
	}
	cfg.AnalystKeys = keys
	cfg.OperatorKeys, err = ParseOperatorKeys(os.Getenv("OPERATOR_API_KEYS"), cfg.APIKey, keys)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func isSpaceOrControl(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}
