package fixture

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/quant"
)

// noSleep makes StreamCandles run instantly for a test -- same pattern
// fixture.NewScriptedProvider's own tests use for the same reason
// (pacing is the one thing a unit test must not actually wait for).
func noSleep(_ context.Context, _ time.Duration) {}

func TestLiveGenerator_EmitsFormingThenOneFinalPerCandle(t *testing.T) {
	gen := NewLiveGenerator(LiveGeneratorConfig{
		StartPrices:    map[string]quant.Price{"BTC-PERP": 1000},
		PriceScale:     0,
		TickStep:       1,
		TicksPerCandle: 3,
		Seed:           1,
	}, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), noSleep)

	ctx, cancel := context.WithCancel(context.Background())
	var got []ingestion.ProviderCandle
	err := gen.StreamCandles(ctx, "BTC-PERP", func(_ context.Context, c ingestion.ProviderCandle) error {
		got = append(got, c)
		if len(got) == 6 { // two full candles at 3 ticks each
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d candles, want 6 (cancel fires on the 6th)", len(got))
	}

	for i, c := range got {
		wantFinal := i%3 == 2 // every third tick (index 2, 5, ...) closes the candle
		if c.IsFinal != wantFinal {
			t.Errorf("candle[%d].IsFinal = %v, want %v", i, c.IsFinal, wantFinal)
		}
	}
	if got[0].OpenTime != got[2].OpenTime {
		t.Errorf("the first candle's own 3 ticks have different OpenTime: %v vs %v", got[0].OpenTime, got[2].OpenTime)
	}
	if !got[3].OpenTime.After(got[2].OpenTime) {
		t.Errorf("the second candle's OpenTime (%v) is not after the first's own (%v)", got[3].OpenTime, got[2].OpenTime)
	}
	if got[3].OpenTime.Sub(got[2].OpenTime) != time.Minute {
		t.Errorf("OpenTime advanced by %v between candles, want exactly 1 minute", got[3].OpenTime.Sub(got[2].OpenTime))
	}
}

// TestLiveGenerator_StartTimesOverridesClockNow is the fix for a real
// bug found running this against the actual worker: without
// StartTimes, every symbol started at clk.Now() regardless of where
// the dev dataset's own recorded history actually ended, which
// collided with devfixture.go's own still-in-flight IngestHistorical
// writes to that same range whenever the dataset's history already
// extended close to "now" (observed as repeated "superseding closed
// candle with different values" warnings against the real worker).
// StartTimes is the fix: when set, it wins over clk.Now() entirely.
func TestLiveGenerator_StartTimesOverridesClockNow(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // far from "now" on purpose
	gen := NewLiveGenerator(LiveGeneratorConfig{
		StartPrices:    map[string]quant.Price{"BTC-PERP": 1000},
		StartTimes:     map[string]time.Time{"BTC-PERP": start},
		PriceScale:     0,
		TickStep:       1,
		TicksPerCandle: 1,
	}, clock.NewFixed(now), noSleep)

	ctx, cancel := context.WithCancel(context.Background())
	var first *ingestion.ProviderCandle
	err := gen.StreamCandles(ctx, "BTC-PERP", func(_ context.Context, c ingestion.ProviderCandle) error {
		if first == nil {
			cp := c
			first = &cp
		}
		cancel()
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	if first == nil {
		t.Fatal("handle was never called")
	}
	if !first.OpenTime.Equal(start) {
		t.Errorf("OpenTime = %v, want StartTimes' own %v (not clk.Now() = %v)", first.OpenTime, start, now)
	}
}

// TestLiveGenerator_NoStartTimeFallsBackToClockNow confirms the other
// side of the same branch still works for a symbol with no StartTimes
// entry -- every existing test in this file relies on exactly this
// fallback already; this one asserts it directly.
func TestLiveGenerator_NoStartTimeFallsBackToClockNow(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 3, 30, 0, time.UTC) // not minute-aligned on purpose
	gen := NewLiveGenerator(LiveGeneratorConfig{
		StartPrices:    map[string]quant.Price{"BTC-PERP": 1000},
		PriceScale:     0,
		TickStep:       1,
		TicksPerCandle: 1,
	}, clock.NewFixed(now), noSleep)

	ctx, cancel := context.WithCancel(context.Background())
	var first *ingestion.ProviderCandle
	err := gen.StreamCandles(ctx, "BTC-PERP", func(_ context.Context, c ingestion.ProviderCandle) error {
		if first == nil {
			cp := c
			first = &cp
		}
		cancel()
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	if first == nil {
		t.Fatal("handle was never called")
	}
	want := now.Truncate(time.Minute)
	if !first.OpenTime.Equal(want) {
		t.Errorf("OpenTime = %v, want clk.Now() truncated to the minute (%v)", first.OpenTime, want)
	}
}

func TestLiveGenerator_PriceWalksByExactlyOneTickStepPerTick(t *testing.T) {
	gen := NewLiveGenerator(LiveGeneratorConfig{
		StartPrices:    map[string]quant.Price{"BTC-PERP": 1000},
		PriceScale:     0,
		TickStep:       5,
		TicksPerCandle: 1,
		Seed:           7,
	}, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), noSleep)

	ctx, cancel := context.WithCancel(context.Background())
	var closes []quant.Price
	err := gen.StreamCandles(ctx, "BTC-PERP", func(_ context.Context, c ingestion.ProviderCandle) error {
		p, perr := quant.ParsePrice(c.Close, 0)
		if perr != nil {
			t.Fatalf("ParsePrice(%q): %v", c.Close, perr)
		}
		closes = append(closes, p)
		if len(closes) == 20 {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}

	prev := quant.Price(1000)
	for i, p := range closes {
		diff := int64(p - prev)
		if diff != 5 && diff != -5 {
			t.Fatalf("closes[%d] = %d, prev = %d: step was %d, want exactly +-5", i, p, prev, diff)
		}
		if p <= 0 {
			t.Fatalf("closes[%d] = %d, want a positive price (never zero or negative)", i, p)
		}
		prev = p
	}
}

func TestLiveGenerator_TwoSymbolsWalkIndependently(t *testing.T) {
	gen := NewLiveGenerator(LiveGeneratorConfig{
		StartPrices:    map[string]quant.Price{"BTC-PERP": 1000, "ETH-PERP": 1000},
		PriceScale:     0,
		TickStep:       1,
		TicksPerCandle: 1,
		Seed:           42,
	}, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), noSleep)

	collect := func(symbol string, n int) []string {
		ctx, cancel := context.WithCancel(context.Background())
		var closes []string
		_ = gen.StreamCandles(ctx, symbol, func(_ context.Context, c ingestion.ProviderCandle) error {
			closes = append(closes, c.Close)
			if len(closes) == n {
				cancel()
			}
			return nil
		})
		return closes
	}

	btc := collect("BTC-PERP", 15)
	eth := collect("ETH-PERP", 15)

	identical := true
	for i := range btc {
		if btc[i] != eth[i] {
			identical = false
			break
		}
	}
	if identical {
		t.Fatalf("BTC-PERP and ETH-PERP produced identical walks (%v) -- symbolSeed is not actually varying the RNG per symbol", btc)
	}
}

func TestLiveGenerator_UnknownSymbolBlocksUntilCancelled(t *testing.T) {
	gen := NewLiveGenerator(LiveGeneratorConfig{
		StartPrices: map[string]quant.Price{"BTC-PERP": 1000},
		PriceScale:  0,
		TickStep:    1,
	}, clock.Real{}, noSleep)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	err := gen.StreamCandles(ctx, "UNKNOWN-SYMBOL", func(_ context.Context, _ ingestion.ProviderCandle) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	if called {
		t.Fatalf("handle was called for a symbol with no configured start price")
	}
}

func TestLiveGenerator_ContextCancelDuringSleepStopsPromptly(t *testing.T) {
	gen := NewLiveGenerator(LiveGeneratorConfig{
		StartPrices:    map[string]quant.Price{"BTC-PERP": 1000},
		PriceScale:     0,
		TickStep:       1,
		TicksPerCandle: 1,
	}, clock.Real{}, func(ctx context.Context, _ time.Duration) {
		<-ctx.Done() // simulates a long real sleep that ctx cancellation cuts short
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- gen.StreamCandles(ctx, "BTC-PERP", func(_ context.Context, _ ingestion.ProviderCandle) error {
			return nil
		})
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamCandles: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StreamCandles did not return promptly after ctx was cancelled")
	}
}
