package marketdata

import (
	"context"
	"errors"

	"github.com/chibuike-kt/argus/internal/clock"
)

// MarketStatus classifies an instant for a market. A ScheduledClosure is
// reported as closed, not as a data gap and not as a stale feed (ADR-006):
// gap detection and freshness checks must consult Calendar before treating
// a missing or late candle as a real problem.
type MarketStatus int

const (
	// MarketStatusUnknown is the zero value: no calendar has classified
	// this instant yet.
	MarketStatusUnknown MarketStatus = iota
	// MarketStatusOpen means the market is trading at this instant.
	MarketStatusOpen
	// MarketStatusScheduledClosure means the market is closed by a known,
	// scheduled rule (a session boundary or holiday) -- not a data gap and
	// not a stale feed (ADR-006).
	MarketStatusScheduledClosure
)

func (s MarketStatus) String() string {
	switch s {
	case MarketStatusOpen:
		return "open"
	case MarketStatusScheduledClosure:
		return "scheduled_closure"
	default:
		return "unknown"
	}
}

// Calendar is the per-market trading calendar port (ADR-006). The calendar
// source and holiday handling for each market must be defined before that
// market is enabled; until then, a market with no implemented calendar
// must error rather than guess.
type Calendar interface {
	Status(ctx context.Context, assetClass string, t clock.EventTime) (MarketStatus, error)
}

// ContinuousCalendar treats every instant as open. It is correct only for
// markets with no scheduled closures -- crypto perpetual futures, per
// ADR-006 ("crypto perpetual markets are treated as continuous").
type ContinuousCalendar struct{}

// Status always reports MarketStatusOpen.
func (ContinuousCalendar) Status(_ context.Context, _ string, _ clock.EventTime) (MarketStatus, error) {
	return MarketStatusOpen, nil
}

// ErrForexCalendarNotImplemented is returned by ForexCalendar.Status. Forex
// follows session hours and closes on weekends and holidays (ADR-006), and
// no session/holiday data source is configured yet. A market calendar must
// not guess: an unimplemented calendar errors rather than defaulting to
// "open" or "closed".
var ErrForexCalendarNotImplemented = errors.New("marketdata: forex market calendar is not implemented: no session/holiday data source is configured (ADR-006)")

// ForexCalendar is an intentionally unimplemented placeholder. It exists so
// callers have a named type to wire up once a calendar source is chosen,
// not because any of its logic is ready.
type ForexCalendar struct{}

// Status always returns ErrForexCalendarNotImplemented.
func (ForexCalendar) Status(_ context.Context, _ string, _ clock.EventTime) (MarketStatus, error) {
	return MarketStatusUnknown, ErrForexCalendarNotImplemented
}
