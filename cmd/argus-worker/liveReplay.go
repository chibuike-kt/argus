package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/hub"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// startDevFixtureLiveReplay is Part 2G's own one-command demo feed: new
// candles forming and closing forward from each seeded instrument's own
// last historical close, published through the hub exactly like a real
// provider's candles would be -- devfixture.go's own IngestHistorical
// call cannot do this (replaying the same recorded dataset again only
// redelivers identical, already-closed candles; see its own doc
// comment), which is exactly the gap this fills. DEV/TEST ONLY, gated
// by cfg.DevFixtureLiveReplay in main (never true in a real deployment,
// same posture as DevFixtureIngest -- this function does not re-check
// it, the caller already did). Runs until ctx is cancelled (worker
// shutdown); each symbol streams in its own goroutine, sharing one
// Service/publisher the same way the processing loop already shares
// store/publisher across instruments.
func startDevFixtureLiveReplay(
	ctx context.Context,
	logger *slog.Logger,
	speed float64,
	data fixture.Data,
	instrumentStore *instruments.Store,
	marketdataStore *marketdata.Store,
	publisher hub.Publisher,
	clk clock.Clock,
) error {
	startPrices := make(map[string]quant.Price, len(data.Instruments))
	startTimes := make(map[string]time.Time, len(data.Instruments))
	var priceScale quant.Scale
	var tickStep quant.Price
	for _, inst := range data.Instruments {
		candles := data.Candles[inst.Symbol]
		if len(candles) == 0 {
			continue // nothing to seed this symbol's own walk from
		}
		scale := quant.Scale(inst.PriceDecimalPlaces)
		tick, err := quant.ParsePrice(inst.TickSize, scale)
		if err != nil {
			return fmt.Errorf("dev fixture live replay: parse tick size for %s: %w", inst.Symbol, err)
		}
		last := candles[len(candles)-1]
		p, err := quant.ParsePrice(last.Close, scale)
		if err != nil {
			return fmt.Errorf("dev fixture live replay: parse last close for %s: %w", inst.Symbol, err)
		}
		// Every symbol in this dev dataset shares one price_scale/
		// tick_size (db/seed/dev_fixture_instruments.sql seeds both
		// BTC-PERP and ETH-PERP with price_scale 0, tick_size 1) -- a
		// per-symbol override is not needed for the one dataset this
		// exists for (fixture.LiveGeneratorConfig's own doc comment).
		priceScale = scale
		tickStep = tick
		startPrices[inst.Symbol] = p
		// Exactly one minute after the dataset's own last recorded
		// candle (never clk.Now()): the dev dataset's own history can
		// end anywhere, including close to real "now" -- starting
		// this generator at "now" regardless collided with
		// IngestHistorical's own still-in-flight writes to that same
		// range (see LiveGeneratorConfig.StartTimes's own doc comment
		// for what that looked like against the real worker).
		startTimes[inst.Symbol] = last.OpenTime.Add(time.Minute)
	}
	if len(startPrices) == 0 {
		return fmt.Errorf("dev fixture live replay: no symbol in the dev dataset has any historical candles to seed its own walk from")
	}

	gen := fixture.NewLiveGenerator(fixture.LiveGeneratorConfig{
		StartPrices: startPrices,
		StartTimes:  startTimes,
		PriceScale:  priceScale,
		TickStep:    tickStep,
		Speed:       speed,
		// A fresh seed per process start (not a fixed constant): this
		// is a visible demo for a human, never ADR-019 point-in-time
		// replay, so a different walk on every worker restart is the
		// right default, not a bug -- real determinism, if ever
		// needed for this feed specifically, would take an explicit
		// config value, not clk.Now() reused here.
		Seed: clk.Now().UnixNano(),
	}, clk, nil)

	cache := ingestion.NewInstrumentCache(instrumentStore)
	normalizer := ingestion.NewNormalizer(cache, clk)
	svc := ingestion.NewService(devFixtureProviderName, gen, normalizer, cache, marketdataStore, marketdata.ContinuousCalendar{}, logger)
	svc.SetPublisher(publisher)

	for symbol := range startPrices {
		go func(symbol string) {
			if err := svc.WatchCandles(ctx, symbol); err != nil && ctx.Err() == nil {
				logger.Error("worker: dev fixture live replay stopped", "symbol", symbol, "error", err)
			}
		}(symbol)
	}
	logger.Warn("worker: ARGUS_DEV_FIXTURE_LIVE_REPLAY is enabled -- generating a live demo feed, never do this against a production database",
		"symbols", len(startPrices), "speed", speed)
	return nil
}
