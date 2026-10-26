package ingestion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// FreshnessConfig defines the maximum age a market's latest known data
// may reach before FeedHealth reports it stale. There is no envDefault
// for either field, deliberately: ADR-006 requires per-market freshness
// targets to be defined numerically before that market is enabled, and
// ADR-018 ties stale-feed alerting to the same targets. Loading this
// config without both set fails startup rather than silently assuming a
// threshold nobody has actually decided (guide 12).
//
// Only the two asset classes ADR-001 puts in scope (crypto perpetual
// futures, major forex pairs) are covered. Enabling a third asset class
// without a configured threshold for it is a startup-time gap this
// config cannot catch on its own -- MaxAgeFor fails for an unrecognized
// asset class, but only once something actually asks for it.
type FreshnessConfig struct {
	CryptoPerpMaxAge time.Duration `env:"ARGUS_FRESHNESS_CRYPTO_PERP_MAX_AGE"`
	ForexMaxAge      time.Duration `env:"ARGUS_FRESHNESS_FOREX_MAX_AGE"`
}

// LoadFreshnessConfig parses FreshnessConfig from the environment and
// validates it.
func LoadFreshnessConfig() (FreshnessConfig, error) {
	var c FreshnessConfig
	if err := env.Parse(&c); err != nil {
		return FreshnessConfig{}, fmt.Errorf("parse freshness config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return FreshnessConfig{}, fmt.Errorf("invalid freshness config: %w", err)
	}
	return c, nil
}

// Validate fails if either threshold is unset or non-positive.
func (c FreshnessConfig) Validate() error {
	if c.CryptoPerpMaxAge <= 0 {
		return errors.New("ARGUS_FRESHNESS_CRYPTO_PERP_MAX_AGE is required and must be positive")
	}
	if c.ForexMaxAge <= 0 {
		return errors.New("ARGUS_FRESHNESS_FOREX_MAX_AGE is required and must be positive")
	}
	return nil
}

// MaxAgeFor returns the configured staleness threshold for assetClass.
// An asset class this config does not cover is an error, never a
// fallback value -- an unrecognized asset class must not silently
// inherit some other market's threshold.
func (c FreshnessConfig) MaxAgeFor(assetClass string) (time.Duration, error) {
	switch assetClass {
	case "crypto_perp":
		return c.CryptoPerpMaxAge, nil
	case "forex":
		return c.ForexMaxAge, nil
	default:
		return 0, fmt.Errorf("ingestion: no freshness threshold configured for asset class %q", assetClass)
	}
}

// FeedStatus classifies how fresh an instrument's latest known data is.
type FeedStatus int

const (
	// FeedStatusUnknown is the zero value: Evaluate could not determine
	// a status (e.g. an unconfigured asset class).
	FeedStatusUnknown FeedStatus = iota
	// FeedStatusFresh means the latest known data is within the
	// configured threshold.
	FeedStatusFresh
	// FeedStatusStale means it is not.
	FeedStatusStale
	// FeedStatusScheduledClosure means the calendar affirmatively
	// reports the market closed right now -- the feed being quiet is
	// expected, not stale (ADR-006, ADR-018). This always takes
	// precedence over age: a scheduled closure is never reported stale,
	// no matter how old the latest data is.
	FeedStatusScheduledClosure
)

func (s FeedStatus) String() string {
	switch s {
	case FeedStatusFresh:
		return "fresh"
	case FeedStatusStale:
		return "stale"
	case FeedStatusScheduledClosure:
		return "scheduled_closure"
	default:
		return "unknown"
	}
}

// FeedHealth evaluates feed staleness from a configured threshold, the
// market calendar, and an injected clock (guide 7, 13). It is a pure
// evaluator: it does not query the store itself for an instrument's
// latest candle or quote time -- the caller supplies that, which keeps
// FeedHealth testable without a database and leaves "periodically check
// every watched instrument" to a future worker loop this slice does not
// build (out of scope: no scheduling, no Redis).
type FeedHealth struct {
	freshness FreshnessConfig
	calendar  marketdata.Calendar
	clk       clock.Clock
}

// NewFeedHealth wires a FeedHealth.
func NewFeedHealth(freshness FreshnessConfig, calendar marketdata.Calendar, clk clock.Clock) *FeedHealth {
	return &FeedHealth{freshness: freshness, calendar: calendar, clk: clk}
}

// Evaluate reports assetClass's feed status given the event time of its
// latest known candle or quote. A calendar outcome of
// MarketStatusScheduledClosure always wins over age. Any other calendar
// outcome -- open, unknown, or the calendar itself erroring (including
// an unimplemented one, e.g. ForexCalendar today) -- falls through to an
// ordinary age comparison: an inconclusive calendar must not be treated
// as "assume closed, do not bother checking freshness" any more than
// gap detection treats it that way.
func (h *FeedHealth) Evaluate(ctx context.Context, assetClass string, latest clock.EventTime) (FeedStatus, error) {
	now := h.clk.Now()

	status, calErr := h.calendar.Status(ctx, assetClass, clock.NewEventTime(now))
	if calErr == nil && status == marketdata.MarketStatusScheduledClosure {
		return FeedStatusScheduledClosure, nil
	}

	maxAge, err := h.freshness.MaxAgeFor(assetClass)
	if err != nil {
		return FeedStatusUnknown, err
	}

	if now.Sub(latest.Time()) > maxAge {
		return FeedStatusStale, nil
	}
	return FeedStatusFresh, nil
}
