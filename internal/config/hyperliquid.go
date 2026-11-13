package config

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
}
