package fixture

import (
	"context"
	"hash/fnv"
	"math/rand"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/quant"
)

// LiveGenerator implements ingestion.Provider by generating new candles
// forward from a starting price, never replaying recorded history (that
// is Provider's own job, via the embedded-in-devfixture.go one-time
// IngestHistorical call) -- this is Part 2G's own answer to
// devfixture.go's own doc comment ("a later brief can add a live stream
// if an ongoing feed is genuinely needed to demonstrate something
// [IngestHistorical] does not"): watching a forming candle visibly move
// and a new one appear at the boundary needs candles that keep arriving
// after startup, which replaying a fixed, already-ingested dataset
// cannot do no matter how many times it is repeated (idempotent
// redelivery of identical values, not new data).
//
// DEV/TEST ONLY, same as the rest of this package (ADR-007 is open; no
// code path here or in cmd/argus-worker enables an instrument). Prices
// are a random walk in integer tick_size steps (CLAUDE.md section 13:
// never float64 for a price) -- plausible movement for watching candles
// change, not a market simulation with any claim to realism.
type LiveGeneratorConfig struct {
	// StartPrices seeds each symbol's own walk at its own last
	// historical close, read by the caller from the same dataset
	// devfixture.go already ingested -- continuity, so the live feed
	// picks up visually where the ingested history's own last candle
	// left off, never a jump to an arbitrary unrelated price.
	StartPrices map[string]quant.Price
	// StartTimes seeds each symbol's own first generated candle at
	// exactly one minute after its own last historical OpenTime (the
	// same source as StartPrices). A symbol missing from this map
	// falls back to clk.Now() truncated to the minute -- but every
	// real caller (startDevFixtureLiveReplay) always sets both maps
	// together from the same dataset, so this only matters for a
	// symbol with StartPrices but no recorded candles at all, which
	// cannot happen given StartPrices is itself read from that
	// symbol's own last candle. Without this, the generator would
	// start at real "now" regardless of where history actually ends,
	// which collides (two different, conflicting writers superseding
	// the same open_time) whenever the dev dataset's own recorded
	// history already extends close to "now" -- confirmed against the
	// real worker, not a hypothetical: devfixture.go's own
	// IngestHistorical and this generator both tried to write
	// instrument 1492's own 14:17-14:19 candles with different values,
	// logged as repeated "superseding closed candle" warnings.
	StartTimes map[string]time.Time
	// PriceScale/TickStep are shared across every symbol this
	// generator serves -- true for this dev fixture's own two
	// instruments (price_scale 0, tick_size 1 both); a per-symbol
	// override is not needed for the one dataset this exists for.
	PriceScale quant.Scale
	TickStep   quant.Price
	// TicksPerCandle is how many forming updates happen before a
	// minute closes -- [U], a demo/legibility choice (watching 4
	// visible moves per candle on screen), not a measured value.
	// <= 0 defaults to 4.
	TicksPerCandle int
	// Speed scales simulated time against real time, the same meaning
	// as fixture.ScriptedProvider's own Speed: <= 0 behaves as 1 (one
	// real minute per simulated candle); 60 makes one simulated candle
	// take one real second, so a human watching the chart sees a new
	// candle roughly once a second instead of once a minute.
	Speed float64
	// Seed makes each symbol's own walk reproducible across a run
	// (ADR-019 in spirit, though this generator's own job is a visible
	// demo, not point-in-time replay) -- each symbol gets its own
	// independent stream derived from Seed plus its own name, so two
	// symbols never walk in lockstep.
	Seed int64
}

// LiveGenerator is dev/test tooling, never a real ingestion.Provider --
// see this file's own package doc comment.
type LiveGenerator struct {
	cfg   LiveGeneratorConfig
	clk   clock.Clock
	sleep func(ctx context.Context, d time.Duration)
}

// NewLiveGenerator applies TicksPerCandle/Speed defaults and returns a
// generator ready to stream any symbol in cfg.StartPrices. sleep is the
// same injectable pacing seam fixture.NewScriptedProvider already uses
// (nil means real time.After) -- a test replaces it to run an entire
// multi-candle scenario without waiting in real time, the only way to
// unit-test this generator's own OHLC/IsFinal shape quickly.
func NewLiveGenerator(cfg LiveGeneratorConfig, clk clock.Clock, sleep func(ctx context.Context, d time.Duration)) *LiveGenerator {
	if cfg.TicksPerCandle <= 0 {
		cfg.TicksPerCandle = 4
	}
	if cfg.Speed <= 0 {
		cfg.Speed = 1
	}
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}
	}
	return &LiveGenerator{cfg: cfg, clk: clk, sleep: sleep}
}

func symbolSeed(base int64, symbol string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(symbol))
	return base ^ int64(h.Sum64())
}

// Instruments/HistoricalCandles/StreamQuotes are never called by
// cmd/argus-worker's own live-replay wiring (WatchCandles only reaches
// StreamCandles) -- implemented as empty/blocking stubs purely to
// satisfy ingestion.Provider, the same composition-over-reimplementation
// choice ScriptedProvider makes by embedding a real Provider for these;
// this type has no recorded dataset to delegate to, so a stub is
// simpler and equally correct for a type that is never asked to do more
// than stream live candles.
func (g *LiveGenerator) Instruments(_ context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}

func (g *LiveGenerator) HistoricalCandles(_ context.Context, _ string, _, _ time.Time) ([]ingestion.ProviderCandle, error) {
	return nil, nil
}

func (g *LiveGenerator) StreamQuotes(ctx context.Context, _ string, _ func(ctx context.Context, q ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

// StreamCandles generates symbol's own forward-moving candles,
// starting at its own configured StartPrice, until ctx is cancelled.
// A symbol with no configured start price streams nothing (matches
// every other Provider in this package: "no data for this symbol" is
// never itself an error, only a reason to block on ctx.Done()).
func (g *LiveGenerator) StreamCandles(ctx context.Context, symbol string, handle func(ctx context.Context, c ingestion.ProviderCandle) error) error {
	start, ok := g.cfg.StartPrices[symbol]
	if !ok {
		<-ctx.Done()
		return nil
	}

	rng := rand.New(rand.NewSource(symbolSeed(g.cfg.Seed, symbol))) //nolint:gosec // dev-only demo data, never security-sensitive
	tickDuration := time.Duration(float64(time.Minute) / float64(g.cfg.TicksPerCandle) / g.cfg.Speed)
	last := start
	openTime, ok := g.cfg.StartTimes[symbol]
	if !ok {
		openTime = g.clk.Now().UTC().Truncate(time.Minute)
	}

	for {
		open := last
		high, low, cl := open, open, open

		for tick := 0; tick < g.cfg.TicksPerCandle; tick++ {
			g.sleep(ctx, tickDuration)
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			cl = step(rng, cl, g.cfg.TickStep)
			if cl > high {
				high = cl
			}
			if cl < low {
				low = cl
			}

			c := ingestion.ProviderCandle{
				Symbol:   symbol,
				OpenTime: openTime,
				Open:     open.String(g.cfg.PriceScale),
				High:     high.String(g.cfg.PriceScale),
				Low:      low.String(g.cfg.PriceScale),
				Close:    cl.String(g.cfg.PriceScale),
				Volume:   "1",
				IsFinal:  tick == g.cfg.TicksPerCandle-1,
			}
			if err := handle(ctx, c); err != nil {
				return err
			}
		}

		last = cl
		openTime = openTime.Add(time.Minute)
	}
}

// step moves p by exactly one tickStep, up or down with equal
// probability -- integer arithmetic throughout (quant.Price is int64),
// never a float64 price. A step that would make the price non-positive
// is skipped (retried as "up" instead) rather than ever emitting a
// zero or negative price.
func step(rng *rand.Rand, p, tickStep quant.Price) quant.Price {
	if rng.Intn(2) == 0 {
		if np, err := p.Add(tickStep); err == nil {
			return np
		}
	}
	if np, err := p.Sub(tickStep); err == nil && np > 0 {
		return np
	}
	if np, err := p.Add(tickStep); err == nil {
		return np
	}
	return p
}

var _ ingestion.Provider = (*LiveGenerator)(nil)
