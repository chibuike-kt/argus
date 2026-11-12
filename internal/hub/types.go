// Package hub implements the live candle stream's own server-side fan
// out (live-chart-pipeline brief, Phase 3; docs/briefs/adr-004-ws-contract-draft.md
// v2). The worker publishes every stored candle change (forming, closed,
// superseded) on a per-instrument Redis channel; the API process's own
// Hub subscribes and fans out to every WebSocket connection with a
// matching subscription. The worker never serves clients directly.
package hub

import (
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// EventKind distinguishes the three kinds of stored candle change the
// worker ever publishes.
type EventKind int

// The three kinds of stored candle change the worker ever publishes.
const (
	EventForming EventKind = iota
	EventClosed
	EventCorrected
)

func (k EventKind) String() string {
	switch k {
	case EventClosed:
		return "closed"
	case EventCorrected:
		return "corrected"
	default:
		return "forming"
	}
}

// CandleEvent is what the worker publishes to Redis on every stored
// 1-minute candle change -- the hub's own fan-out source, never seen
// directly by a WebSocket client (the hub derives the client-facing
// envelope, including any derived-interval recomputation, from this).
// Prices and volume stay scaled integers here (rule 3's "no float64"),
// not decimal strings: this is an internal system boundary, not the
// external wire format: the hub converts to decimal strings per
// instrument scale when it builds a client envelope.
type CandleEvent struct {
	InstrumentID int64       `json:"instrument_id"`
	Kind         EventKind   `json:"kind"`
	OpenTime     time.Time   `json:"open_time"`
	CloseTime    time.Time   `json:"close_time"`
	Open         quant.Price `json:"open"`
	High         quant.Price `json:"high"`
	Low          quant.Price `json:"low"`
	Close        quant.Price `json:"close"`
	Volume       quant.Qty   `json:"volume"`
	Revision     int32       `json:"revision"`
}

// Interval is one of the six values Phase 1's REST endpoint already
// defines (1m, 5m, 15m, 1h, 4h, 1d) -- the hub reuses the exact same set
// and duration mapping so the live stream and REST history can never
// quietly diverge on what an interval means.
type Interval string

// The six intervals Phase 1's REST endpoint defines.
const (
	Interval1m  Interval = "1m"
	Interval5m  Interval = "5m"
	Interval15m Interval = "15m"
	Interval1h  Interval = "1h"
	Interval4h  Interval = "4h"
	Interval1d  Interval = "1d"
)

// Duration maps a validated Interval to its time.Duration. Returns
// (0, false) for anything else -- callers check Valid() first, same
// convention internal/httpapi's own intervalDuration uses.
func (i Interval) Duration() (time.Duration, bool) {
	switch i {
	case Interval1m:
		return time.Minute, true
	case Interval5m:
		return 5 * time.Minute, true
	case Interval15m:
		return 15 * time.Minute, true
	case Interval1h:
		return time.Hour, true
	case Interval4h:
		return 4 * time.Hour, true
	case Interval1d:
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

// Valid reports whether i is one of the six known intervals.
func (i Interval) Valid() bool {
	_, ok := i.Duration()
	return ok
}
