package config

import "time"

// Hyperliquid holds the Hyperliquid adapter's own tunables (private
// use only, ADR-007 still open -- see
// docs/briefs/brief-hyperliquid-adapter.md). Unlike LiveFeed's own
// fields, Symbols and BackfillDays are not [U] standard-practice
// placeholders: both defaults are the owner's own explicit choice
// (approved alongside the rest of the brief), not invented here.
// ReconnectBackoffBase/Cap are deliberately NOT duplicated here --
// the brief's own instruction is to reuse LiveFeed's existing values,
// not add a second, parallel pair.
type Hyperliquid struct {
	// Symbols is which Hyperliquid coins to ingest (its own coin
	// names, from the meta response -- e.g. "BTC", not "BTC-PERP" or
	// "BTCUSDT"). A symbol outside this list is never ingested, by
	// cmd/argus-instruments' sync-hyperliquid command or by the
	// worker's own live/backfill wiring. Owner's own default:
	// BTC,ETH,SOL.
	Symbols []string `env:"ARGUS_HYPERLIQUID_SYMBOLS" envSeparator:"," envDefault:"BTC,ETH,SOL"`

	// BackfillDays bounds how far back historical backfill reaches.
	// Owner's own default: 3 -- Hyperliquid's own candleSnapshot limit
	// is "only the most recent 5000 1-minute candles are available"
	// (~3.47 days), so this package's own HistoricalCandles call
	// never claims more than that ceiling actually holds regardless
	// of what this value requests.
	BackfillDays int `env:"ARGUS_HYPERLIQUID_BACKFILL_DAYS" envDefault:"3"`

	// MaxConnectionAge bounds how long one StreamCandles connection is
	// kept open before this package voluntarily reconnects (a planned
	// reconnect, never losing a candle across the swap -- the gap
	// backfill after every disconnect, planned or not, is
	// unconditional). [U]: Hyperliquid documents no connection-
	// lifetime limit at all, unlike Binance's own documented 24h --
	// this is pure precaution, a standard-practice default, not a
	// measured or exchange-asserted value. 12 hours.
	MaxConnectionAge time.Duration `env:"ARGUS_HYPERLIQUID_MAX_CONNECTION_AGE" envDefault:"12h"` // [U]

	// BaseURLOverride/WSURLOverride point the adapter at a different
	// REST/WebSocket host than Hyperliquid's own real one -- empty
	// (the default) means use the real hosts. Exists for the exact
	// same reason hyperliquid.WithBaseURL/WithWSURL already exist for
	// tests: to point this adapter at something other than the real
	// exchange, here specifically so a real-disconnect verification
	// run can point it at a genuinely unreachable address (a real
	// network-level failure, not a mocked one) without modifying this
	// machine's own system/network configuration -- never used for
	// anything else, and empty in every normal deployment.
	BaseURLOverride string `env:"ARGUS_HYPERLIQUID_BASE_URL_OVERRIDE" envDefault:""`
	WSURLOverride   string `env:"ARGUS_HYPERLIQUID_WS_URL_OVERRIDE" envDefault:""`

	// PeriodicBackfillInterval/Window close the real-run gap this
	// brief's own verification found: StreamCandles' in-memory
	// tracked-minute starts empty on every fresh connection, so if
	// the first live message already belongs to a minute later than
	// the one the connection's own startup/reconnect backfill left
	// dangling (last item in that batch, open by design), that
	// minute is never closed by the stream itself -- only the NEXT
	// disconnect's own gap backfill would catch it, which on an
	// otherwise-healthy connection could be MaxConnectionAge away.
	// runHyperliquidSymbol now also runs IngestHistorical on this
	// fixed interval WHILE streaming, over a short trailing window,
	// so a dangling minute like that is closed within one interval
	// instead of waiting for the next real disconnect. [U]: both are
	// a precaution, not a measured or exchange-asserted value -- 60s
	// period, 5-minute window (comfortably covers a single missed
	// minute plus the fetch's own latency).
	PeriodicBackfillInterval time.Duration `env:"ARGUS_HYPERLIQUID_PERIODIC_BACKFILL_INTERVAL" envDefault:"60s"` // [U]
	PeriodicBackfillWindow   time.Duration `env:"ARGUS_HYPERLIQUID_PERIODIC_BACKFILL_WINDOW" envDefault:"5m"`    // [U]

	// PingInterval/StallTimeout are the WebSocket heartbeat and stall
	// watchdog (hyperliquid.WithHeartbeat). Hyperliquid's docs
	// (websocket/timeouts-and-heartbeats) say the server closes a
	// connection it has sent nothing on for 60 seconds and answers
	// {"method":"ping"} with {"channel":"pong"}; they give no ping
	// interval. [U]: ping every 20s; a connection that delivers
	// nothing at all (not even a pong) for 60s is treated as dead,
	// closed, backfilled and reconnected. 0 disables either.
	PingInterval time.Duration `env:"ARGUS_HYPERLIQUID_PING_INTERVAL" envDefault:"20s"` // [U]
	StallTimeout time.Duration `env:"ARGUS_HYPERLIQUID_STALL_TIMEOUT" envDefault:"60s"` // [U]

	// IntegrityScanInterval is how often, while streaming, the worker scans
	// the stored series (the last BackfillDays) for holes and backfills
	// them; it also runs after the startup and every reconnect backfill.
	// [U] 10 minutes (proposed): holes come from stream drops, which the
	// reconnect path already scans after, so this is the safety net for a
	// hole nothing else noticed. 0 disables the periodic run.
	IntegrityScanInterval time.Duration `env:"ARGUS_HYPERLIQUID_INTEGRITY_SCAN_INTERVAL" envDefault:"10m"` // [U]

	// MaxTrackedSymbols is the system cap of distinct Hyperliquid symbols
	// ARGUS ingests and analyses at once (D15.3, 8): the configured floor
	// plus every enabled instrument on at least one watchlist. Today's
	// adapter opens one WebSocket connection per symbol and Hyperliquid
	// allows 10 per IP, so 8 leaves room for reconnects. An add that would
	// track a new symbol past it is refused; the worker never tracks more.
	MaxTrackedSymbols int `env:"ARGUS_HYPERLIQUID_MAX_TRACKED_SYMBOLS" envDefault:"8"`
	// RemovalGrace is how long a symbol keeps ingesting after it leaves
	// every watchlist (D15.4, 10 minutes), so a remove then an undo does
	// not restart its pipeline.
	RemovalGrace time.Duration `env:"ARGUS_HYPERLIQUID_REMOVAL_GRACE" envDefault:"10m"`
	// BackfillConcurrency is how many new symbols backfill their history
	// at once (D15.4, 2); the rest wait their turn.
	BackfillConcurrency int `env:"ARGUS_HYPERLIQUID_BACKFILL_CONCURRENCY" envDefault:"2"`
	// NewSymbolBackfillRetryCap caps the retry delay of a tracked symbol's
	// history backfill (architect review of 4.1a: about 5 minutes), so a
	// symbol the provider keeps rejecting retries rarely. That path only:
	// the shared live feed cap (ARGUS_LIVE_RECONNECT_BACKOFF_CAP) is
	// unchanged everywhere else.
	NewSymbolBackfillRetryCap time.Duration `env:"ARGUS_HYPERLIQUID_NEW_SYMBOL_BACKFILL_RETRY_CAP" envDefault:"5m"`
	// SupervisorInterval is how often the worker re-reads the tracked set
	// and starts or stops symbol pipelines; the catalog refresh runs at
	// CatalogRefreshInterval. Implementation values. [U]
	SupervisorInterval     time.Duration `env:"ARGUS_HYPERLIQUID_SUPERVISOR_INTERVAL" envDefault:"15s"`      // [U]
	CatalogRefreshInterval time.Duration `env:"ARGUS_HYPERLIQUID_CATALOG_REFRESH_INTERVAL" envDefault:"24h"` // [U]

	// NativeVolumeNoiseDivisor is L1.1 decision 1's float-noise threshold
	// for NATIVE higher timeframe candles only: a volume with more decimals
	// than the instrument's quantity scale is rounded (half to even) when
	// it differs from the rounded value by less than one unit of the scale
	// divided by this number (1000: 1/1000 of a unit); anything larger is
	// rejected. Default ingestion.DefaultNativeVolumeNoiseDivisor, which a
	// value <= 0 also keeps. 1 minute ingestion never rounds.
	NativeVolumeNoiseDivisor int64 `env:"ARGUS_HYPERLIQUID_NATIVE_VOLUME_NOISE_DIVISOR" envDefault:"1000"`
	// NativeRereadInterval is how often each symbol re-reads its two most
	// recent closed native candles per interval (15m, 1h, 4h) and applies
	// the 1 minute revision rule to them: L1.1 decision 6, "at the existing
	// 5 minute periodic backfill". The existing periodic 1 minute backfill
	// actually runs every PeriodicBackfillInterval (60s) over a 5 minute
	// window, so this has its own setting at the 5 minutes the decision
	// names. Cost with 8 symbols: 3 requests of weight 21 per symbol, about
	// 101 weight per minute at 5 minutes. <= 0 turns the re-read off.
	NativeRereadInterval time.Duration `env:"ARGUS_HYPERLIQUID_NATIVE_REREAD_INTERVAL" envDefault:"5m"`
}
