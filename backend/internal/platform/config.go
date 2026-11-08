package platform

import (
	"fmt"
	"os"
)

const defaultPort = "8080"

// Config holds the settings read from environment variables.
type Config struct {
	Port        string
	DatabaseURL string
	// ModelPath is the fraud model file. When it is empty, scoring uses rules only.
	ModelPath string
	// APIKey authenticates requests to the score endpoint.
	APIKey string
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
	return cfg, nil
}
