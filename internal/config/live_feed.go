package config

import "time"

// LiveFeed holds every numeric tunable the live-chart-pipeline brief
// (docs/briefs/live-data-and-charts-design.md, phases 1-4) introduces
// that is not yet a decided rule. All four are [U]: the design doc's own
// section 10 says so explicitly ("values I have deliberately not set...
// I will bring candidates once there is a real feed to measure"), and
// this brief's own rule 2 says the same -- these are this package's own
// placeholder defaults, not an approved spec value, and must not be
// read as one. Nothing in internal/ingestion, the hub, or the WebSocket
// handler hardcodes a default of its own; this is the one place these
// numbers live.
type LiveFeed struct {
	// LateGraceWindow is how long after a minute's own close boundary a
	// late-arriving message is still treated as plausibly legitimate
	// data for that minute, rather than certainly a correction to an
	// already-settled candle. [U] -- design doc section 5's own "Late
	// ticks" row: "I propose the owner choose a value after seeing real
	// feed latency; I will not put a number in the spec first."
	// Placeholder 5s. Not yet consumed by ingestion.Service's own
	// applyCandle (its own doc comment already flags that it trusts a
	// provider's IsFinal claim immediately, with no grace period, as a
	// known, deliberately deferred hardening option) -- this field exists
	// so a later phase has one place to read it from, not a claim that
	// the grace-window behavior itself is implemented yet.
	LateGraceWindow time.Duration `env:"ARGUS_LIVE_LATE_GRACE_WINDOW" envDefault:"5s"` // [U]

	// MaxUpdateRatePerSecond bounds how often the hub coalesces forming-
	// candle (`candle`) messages for one subscription (adr-004-ws-
	// contract-draft.md's own "Coalescing" section); candle_closed and
	// candle_corrected are never coalesced regardless of this value.
	// [U] -- design doc section 6: "I suggest a few per second and will
	// confirm with a real feed." Placeholder 4.
	MaxUpdateRatePerSecond float64 `env:"ARGUS_LIVE_MAX_UPDATE_RATE_PER_SECOND" envDefault:"4"` // [U]

	// MaxSubscriptionsPerConnection bounds how many (instrument_id,
	// interval) subscriptions one WebSocket connection may hold at once
	// (adr-004-ws-contract-draft.md's own "Subscription limits and
	// message size" section). [U] -- not named by the design doc at
	// all; this package's own placeholder, standard practice rather
	// than a measured value. Placeholder 10.
	MaxSubscriptionsPerConnection int `env:"ARGUS_LIVE_MAX_SUBSCRIPTIONS_PER_CONNECTION" envDefault:"10"` // [U]

	// ReconnectBackoffBase/Cap are the provider-reconnect and client-
	// reconnect exponential-backoff-with-full-jitter bounds (design doc
	// section 7: "[U] exact values; standard practice, not a trading
	// parameter"). Full jitter itself (actual delay chosen uniformly in
	// [0, min(Cap, Base*2^attempt)]) is the algorithm, not a third
	// numeric value to configure. Placeholders 1s base, 30s cap.
	ReconnectBackoffBase time.Duration `env:"ARGUS_LIVE_RECONNECT_BACKOFF_BASE" envDefault:"1s"` // [U]
	ReconnectBackoffCap  time.Duration `env:"ARGUS_LIVE_RECONNECT_BACKOFF_CAP" envDefault:"30s"` // [U]
}
