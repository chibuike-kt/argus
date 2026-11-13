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

	// HeartbeatInterval governs both the `heartbeat` message cadence and
	// the server's own session re-check tick
	// (adr-004-ws-contract-review.md review item 2: re-validate on a
	// timer, not only on writes). [U] -- not named by the design doc;
	// standard practice. Placeholder 15s.
	HeartbeatInterval time.Duration `env:"ARGUS_LIVE_HEARTBEAT_INTERVAL" envDefault:"15s"` // [U]

	// MaxMessageSize bounds one WebSocket message's own size, in bytes
	// (adr-004-ws-contract-draft.md's own "Subscription limits and
	// message size" section). [U]. Placeholder 4096 (every real message
	// this contract defines -- subscribe/unsubscribe, a candle payload --
	// is a small, fixed-shape JSON object; this is headroom, not a
	// measured value).
	MaxMessageSize int64 `env:"ARGUS_LIVE_MAX_MESSAGE_SIZE" envDefault:"4096"` // [U]

	// MaxConnectionsPerUser/PerIP (review item 8: "a maximum number of
	// concurrent connections per user and per IP"). [U]. Placeholders 5
	// and 20: one user with several open tabs/devices should comfortably
	// fit under 5; 20 per IP allows a shared office/NAT without being
	// effectively unbounded.
	MaxConnectionsPerUser int `env:"ARGUS_LIVE_MAX_CONNECTIONS_PER_USER" envDefault:"5"` // [U]
	MaxConnectionsPerIP   int `env:"ARGUS_LIVE_MAX_CONNECTIONS_PER_IP" envDefault:"20"`  // [U]

	// MaxSubscribeMessagesPerSecond (review item 8: "a limit on
	// subscribe and unsubscribe messages per second per connection"
	// -- a flood closes the connection). [U]. Placeholder 10: generous
	// for a legitimate client (changing instrument/interval unsubscribes
	// the old stream first, per the brief's own Phase 4 rule -- at most
	// a handful of these messages per genuine user action), tight enough
	// to catch a flood quickly.
	MaxSubscribeMessagesPerSecond float64 `env:"ARGUS_LIVE_MAX_SUBSCRIBE_MESSAGES_PER_SECOND" envDefault:"10"` // [U]
}
