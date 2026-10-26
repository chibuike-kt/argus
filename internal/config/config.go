// Package config loads runtime configuration from the environment.
package config

import (
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

// Load reads a local .env file when present, then parses the environment.
// A missing .env file is not an error; real environments set variables directly.
func Load() (Config, error) {
	_ = godotenv.Load()
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return c, nil
}
