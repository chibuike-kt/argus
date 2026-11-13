// Package config loads and validates runtime configuration from the
// environment. Configuration is parsed and validated once at startup; invalid
// required configuration fails fast (see writing_go_like_a_senior_engineer.md 12).
package config

import (
	"errors"
	"fmt"
	"strings"
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

	// PublicBaseURL is the frontend origin internal/identity/service
	// builds the verification-email link against ({base}/verify-email?
	// token=...) -- an implementation setting (where the web app is
	// served), not an ADR-015 addendum policy value, so it lives here
	// rather than in AuthPolicy. Defaults to the Vite dev server's own
	// origin; validated like ARGUS_AUTH_ALLOWED_ORIGINS (https:// only
	// in production).
	PublicBaseURL string `env:"ARGUS_PUBLIC_BASE_URL" envDefault:"http://localhost:5173"`

	// Auth is ADR-015's own tunables (internal/config/auth.go) --
	// caarlos0/env recurses into a nested struct's own `env` tags
	// automatically, so no prefix/special handling is needed here.
	Auth AuthPolicy

	// Live is the live-chart-pipeline brief's own tunables
	// (internal/config/live_feed.go) -- every one of them [U], per that
	// brief's own rule 2.
	Live LiveFeed

	// Hyperliquid is the Hyperliquid adapter's own tunables
	// (internal/config/hyperliquid.go, brief-hyperliquid-adapter.md).
	Hyperliquid Hyperliquid

	// IngestProvider selects which ingestion.Provider cmd/argus-worker
	// runs its live/backfill loop against. "fixture" (the default) is
	// the dev/test replay provider; "hyperliquid" switches to the real
	// Hyperliquid adapter (private use, ADR-007 still open). Any other
	// value is a startup error (Validate) -- never silently falls back
	// to fixture for a typo'd value, the same reasoning IsProduction's
	// own doc comment gives for not defaulting unknown values to the
	// safe case.
	IngestProvider string `env:"ARGUS_INGEST_PROVIDER" envDefault:"fixture"`

	// DevFixtureIngest turns on cmd/argus-worker's fixture-provider
	// ingestion path: it replays the checked-in dev/test dataset
	// (internal/ingestion/fixture/testdata) into whatever instruments
	// db/seed/dev_fixture_instruments.sql seeded, so the worker's own
	// processing loop has real candles to walk a setup through its
	// lifecycle in a local or test environment. It must never be true
	// in a real deployment -- this is a separate flag rather than
	// branching on Env so that a future non-local Env value is never
	// accidentally interpreted as "safe to enable this too". Defaulted
	// false; opt in explicitly.
	DevFixtureIngest bool `env:"ARGUS_DEV_FIXTURE_INGEST" envDefault:"false"`

	// DevFixtureLiveReplay turns on cmd/argus-worker's own live
	// candle generator (internal/ingestion/fixture.LiveGenerator,
	// live-chart-pipeline brief Part 2G) -- new candles forming and
	// closing forward from each seeded instrument's own last
	// historical close, published through the hub exactly like a
	// real provider's candles would be, so a human can watch ticks
	// move on screen (this brief's own report: "what I will use to
	// watch ticks move on screen"). Separate from DevFixtureIngest
	// (the one-time historical backfill) because IngestHistorical
	// replaying the SAME recorded dataset again would only redeliver
	// identical, already-closed candles (devfixture.go's own doc
	// comment) -- it cannot show a forming candle moving or a new one
	// appearing at a boundary. Same safety posture: never true in a
	// real deployment, opt in explicitly, defaulted false.
	DevFixtureLiveReplay bool `env:"ARGUS_DEV_FIXTURE_LIVE_REPLAY" envDefault:"false"`

	// DevFixtureLiveReplaySpeed scales simulated time against real
	// time for DevFixtureLiveReplay (fixture.LiveGeneratorConfig's own
	// Speed) -- [U], a demo/legibility choice, not a measured value.
	// Placeholder 60: one simulated 1-minute candle every real second,
	// so a human watching the chart sees a new candle about once a
	// second instead of waiting a full minute for each one.
	DevFixtureLiveReplaySpeed float64 `env:"ARGUS_DEV_FIXTURE_LIVE_REPLAY_SPEED" envDefault:"60"` // [U]
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
	if c.IngestProvider != "fixture" && c.IngestProvider != "hyperliquid" {
		return fmt.Errorf("ARGUS_INGEST_PROVIDER must be %q or %q, got %q", "fixture", "hyperliquid", c.IngestProvider)
	}
	// Defense in depth, not a real branch: AuthPolicy.SecureCookies
	// already always returns true when IsProduction does (ADR-015:
	// "production must set them"). This assertion exists so a future
	// change to that derivation that breaks the invariant fails
	// startup immediately, in production, rather than quietly serving
	// an insecure cookie.
	if c.IsProduction() && !c.Auth.SecureCookies(true) {
		return errors.New("ARGUS_ENV=production requires secure cookies, but AuthPolicy.SecureCookies(true) is false")
	}
	// Architect review item 8: a plain-http ARGUS_AUTH_ALLOWED_ORIGINS
	// entry in production would let CSRF's own Origin check accept a
	// non-TLS origin, undermining Secure cookies' own point. Local/test
	// are exempt since the default origins are the Vite dev server's
	// own http:// ones.
	if c.IsProduction() {
		for _, origin := range c.Auth.AllowedOrigins {
			if !strings.HasPrefix(origin, "https://") {
				return fmt.Errorf("ARGUS_ENV=%s requires every ARGUS_AUTH_ALLOWED_ORIGINS entry to start with https://, got %q", c.Env, origin)
			}
		}
		if !strings.HasPrefix(c.PublicBaseURL, "https://") {
			return fmt.Errorf("ARGUS_ENV=%s requires ARGUS_PUBLIC_BASE_URL to start with https://, got %q", c.Env, c.PublicBaseURL)
		}
	}
	// The signup code's HMAC key is what keeps a database breach from
	// being brute-forced offline (ADR-015 "Signup and onboarding");
	// the local-dev placeholder every deployment starts from must
	// never reach production unreplaced.
	if c.IsProduction() && (c.Auth.SignupCodeHMACKey == "" || c.Auth.SignupCodeHMACKey == insecureSignupCodeHMACKey) {
		return errors.New("ARGUS_AUTH_SIGNUP_CODE_HMAC_KEY must be set outside local/test; the local-dev default must not run in production")
	}
	return nil
}
