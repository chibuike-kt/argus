package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

func TestFreshnessConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     FreshnessConfig
		wantErr bool
	}{
		{"both set", FreshnessConfig{CryptoPerpMaxAge: time.Minute, ForexMaxAge: 5 * time.Minute}, false},
		{"crypto unset", FreshnessConfig{ForexMaxAge: 5 * time.Minute}, true},
		{"forex unset", FreshnessConfig{CryptoPerpMaxAge: time.Minute}, true},
		{"both unset", FreshnessConfig{}, true},
		{"negative", FreshnessConfig{CryptoPerpMaxAge: -time.Minute, ForexMaxAge: time.Minute}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFreshnessConfig_MaxAgeFor(t *testing.T) {
	cfg := FreshnessConfig{CryptoPerpMaxAge: 30 * time.Second, ForexMaxAge: 2 * time.Minute}

	got, err := cfg.MaxAgeFor("crypto_perp")
	if err != nil || got != 30*time.Second {
		t.Fatalf("MaxAgeFor(crypto_perp) = %v, %v; want 30s, nil", got, err)
	}
	got, err = cfg.MaxAgeFor("forex")
	if err != nil || got != 2*time.Minute {
		t.Fatalf("MaxAgeFor(forex) = %v, %v; want 2m, nil", got, err)
	}
	if _, err := cfg.MaxAgeFor("equities"); err == nil {
		t.Fatal("MaxAgeFor(equities) = nil error, want an error for an unconfigured asset class")
	}
}

// fakeCalendar lets tests control exactly what Status returns, including
// an error, without needing marketdata.ContinuousCalendar/ForexCalendar's
// fixed behavior.
type fakeCalendar struct {
	status marketdata.MarketStatus
	err    error
}

func (f fakeCalendar) Status(_ context.Context, _ string, _ clock.EventTime) (marketdata.MarketStatus, error) {
	return f.status, f.err
}

func TestFeedHealth_Evaluate_Fresh(t *testing.T) {
	now := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	h := NewFeedHealth(
		FreshnessConfig{CryptoPerpMaxAge: time.Minute, ForexMaxAge: time.Minute},
		fakeCalendar{status: marketdata.MarketStatusOpen},
		clock.NewFixed(now),
	)

	status, err := h.Evaluate(context.Background(), "crypto_perp", clock.NewEventTime(now.Add(-30*time.Second)))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if status != FeedStatusFresh {
		t.Fatalf("status = %v, want Fresh (30s old, 1m threshold)", status)
	}
}

func TestFeedHealth_Evaluate_Stale(t *testing.T) {
	now := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	h := NewFeedHealth(
		FreshnessConfig{CryptoPerpMaxAge: time.Minute, ForexMaxAge: time.Minute},
		fakeCalendar{status: marketdata.MarketStatusOpen},
		clock.NewFixed(now),
	)

	status, err := h.Evaluate(context.Background(), "crypto_perp", clock.NewEventTime(now.Add(-90*time.Second)))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if status != FeedStatusStale {
		t.Fatalf("status = %v, want Stale (90s old, 1m threshold)", status)
	}
}

func TestFeedHealth_Evaluate_ScheduledClosureOverridesAge(t *testing.T) {
	now := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	h := NewFeedHealth(
		FreshnessConfig{CryptoPerpMaxAge: time.Minute, ForexMaxAge: time.Minute},
		fakeCalendar{status: marketdata.MarketStatusScheduledClosure},
		clock.NewFixed(now),
	)

	// Data is 10 hours old -- wildly past any threshold -- but the
	// calendar says the market is scheduled-closed right now, so this
	// must not be reported stale.
	status, err := h.Evaluate(context.Background(), "forex", clock.NewEventTime(now.Add(-10*time.Hour)))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if status != FeedStatusScheduledClosure {
		t.Fatalf("status = %v, want ScheduledClosure regardless of age", status)
	}
}

func TestFeedHealth_Evaluate_InconclusiveCalendarStillChecksAge(t *testing.T) {
	now := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		calendar fakeCalendar
	}{
		{"calendar errors (e.g. unimplemented ForexCalendar)", fakeCalendar{err: errors.New("not implemented")}},
		{"calendar reports Unknown", fakeCalendar{status: marketdata.MarketStatusUnknown}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewFeedHealth(
				FreshnessConfig{CryptoPerpMaxAge: time.Minute, ForexMaxAge: time.Minute},
				tt.calendar,
				clock.NewFixed(now),
			)
			status, err := h.Evaluate(context.Background(), "forex", clock.NewEventTime(now.Add(-10*time.Hour)))
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if status != FeedStatusStale {
				t.Fatalf("status = %v, want Stale: an inconclusive calendar must not be treated as a free pass on staleness", status)
			}
		})
	}
}

func TestFeedHealth_Evaluate_UnconfiguredAssetClass(t *testing.T) {
	now := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	h := NewFeedHealth(
		FreshnessConfig{CryptoPerpMaxAge: time.Minute, ForexMaxAge: time.Minute},
		fakeCalendar{status: marketdata.MarketStatusOpen},
		clock.NewFixed(now),
	)

	_, err := h.Evaluate(context.Background(), "equities", clock.NewEventTime(now))
	if err == nil {
		t.Fatal("Evaluate for an unconfigured asset class = nil error, want an error")
	}
}
