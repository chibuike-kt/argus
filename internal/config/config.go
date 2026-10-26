// Package config loads and validates runtime configuration from the
// environment. Configuration is parsed and validated once at startup; invalid
// required configuration fails fast (see writing_go_like_a_senior_engineer.md 12).
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config holds all runtime configuration. Values come from the environment.
// See .env.example for the full list and defaults.
type Config struct {
	Env             string        `env:"ARGUS_ENV" envDefault:"local"`
	LogLevel        string        `env:"ARGUS_LOG_LEVEL" envDefault:"info"`
	HTTPPort        string        `env:"ARGUS_HTTP_PORT" envDefault:"8080"`
	HTTPReadTimeout time.Duration `env:"ARGUS_HTTP_READ_TIMEOUT" envDefault:"15s"`
	ShutdownTimeout time.Duration `env:"ARGUS_SHUTDOWN_TIMEOUT" envDefault:"15s"`

	DatabaseURL string `env:"ARGUS_DATABASE_URL" envDefault:"postgres://argus:argus@localhost:5432/argus?sslmode=disable"`
	RedisURL    string `env:"ARGUS_REDIS_URL" envDefault:"redis://localhost:6379/0"`
}

// Load reads a local .env file when present, parses the environment and
// validates the result. A missing .env file is not an error; real environments
// set variables directly.
func Load() (Config, error) {
	_ = godotenv.Load()
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return c, nil
}

// Validate checks required fields. It is called by Load and can be called
// directly in tests.
func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("ARGUS_DATABASE_URL is required")
	}
	if c.RedisURL == "" {
		return errors.New("ARGUS_REDIS_URL is required")
	}
	if c.HTTPPort == "" {
		return errors.New("ARGUS_HTTP_PORT is required")
	}
	if c.ShutdownTimeout <= 0 {
		return errors.New("ARGUS_SHUTDOWN_TIMEOUT must be positive")
	}
	return nil
}
