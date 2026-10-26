package fixture

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/ingestion"
)

func sampleData() Data {
	return Data{
		Instruments: []ingestion.ProviderInstrument{{Symbol: "BTCUSDT", AssetClass: "crypto_perp"}},
		Candles: map[string][]ingestion.ProviderCandle{
			"BTCUSDT": {
				// Deliberately out of order: New must sort by OpenTime.
				{Symbol: "BTCUSDT", OpenTime: time.Date(2026, time.October, 27, 12, 1, 0, 0, time.UTC), Open: "2", High: "2", Low: "2", Close: "2", Volume: "1", IsFinal: true},
				{Symbol: "BTCUSDT", OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC), Open: "1", High: "1", Low: "1", Close: "1", Volume: "1", IsFinal: true},
				// 12:02 is deliberately absent -- a gap for ingestion
				// service tests to detect.
				{Symbol: "BTCUSDT", OpenTime: time.Date(2026, time.October, 27, 12, 3, 0, 0, time.UTC), Open: "4", High: "4", Low: "4", Close: "4", Volume: "1", IsFinal: true},
			},
		},
		Quotes: map[string][]ingestion.ProviderQuote{
			"BTCUSDT": {
				{Symbol: "BTCUSDT", Bid: "1", Ask: "2", QuoteTime: time.Date(2026, time.October, 27, 12, 0, 5, 0, time.UTC)},
			},
		},
	}
}

func TestProvider_HistoricalCandles_FiltersRangeAndSorts(t *testing.T) {
	p := New(sampleData(), 0)
	start := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.October, 27, 12, 4, 0, 0, time.UTC)

	got, err := p.HistoricalCandles(context.Background(), "BTCUSDT", start, end)
	if err != nil {
		t.Fatalf("HistoricalCandles: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candles, want 3 (12:02 is deliberately absent from the fixture)", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].OpenTime.Before(got[i-1].OpenTime) {
			t.Fatalf("HistoricalCandles result not sorted ascending: %v then %v", got[i-1].OpenTime, got[i].OpenTime)
		}
	}
	if got[0].Open != "1" {
		t.Errorf("first candle Open = %q, want %q (the out-of-order input must be sorted)", got[0].Open, "1")
	}
}

func TestProvider_HistoricalCandles_RangeExcludesEnd(t *testing.T) {
	p := New(sampleData(), 0)
	start := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.October, 27, 12, 1, 0, 0, time.UTC) // excludes 12:01 itself

	got, err := p.HistoricalCandles(context.Background(), "BTCUSDT", start, end)
	if err != nil {
		t.Fatalf("HistoricalCandles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candles, want exactly 1 (end is exclusive)", len(got))
	}
}

func TestProvider_StreamCandles_ReplaysInOrderThenBlocksUntilCanceled(t *testing.T) {
	p := New(sampleData(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var got []string
	err := p.StreamCandles(ctx, "BTCUSDT", func(_ context.Context, c ingestion.ProviderCandle) error {
		got = append(got, c.Open)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	want := []string{"1", "2", "4"}
	if len(got) != len(want) {
		t.Fatalf("replayed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replayed %v, want %v (order matters)", got, want)
		}
	}
}

func TestProvider_StreamCandles_HandlerErrorStopsStream(t *testing.T) {
	p := New(sampleData(), 0)
	sentinel := context.Canceled // reuse an existing error value as a stand-in sentinel
	calls := 0

	err := p.StreamCandles(context.Background(), "BTCUSDT", func(_ context.Context, _ ingestion.ProviderCandle) error {
		calls++
		if calls == 1 {
			return sentinel
		}
		t.Fatal("handle called again after returning an error; the stream should have stopped")
		return nil
	})
	if err != sentinel {
		t.Fatalf("StreamCandles returned %v, want the handler's error unchanged", err)
	}
	if calls != 1 {
		t.Fatalf("handle called %d times, want exactly 1", calls)
	}
}

func TestProvider_StreamCandles_CtxCanceledBeforeAnyEvent(t *testing.T) {
	p := New(sampleData(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	called := false
	err := p.StreamCandles(ctx, "BTCUSDT", func(_ context.Context, _ ingestion.ProviderCandle) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles with an already-canceled ctx: err = %v, want nil", err)
	}
	if called {
		t.Error("handle was called despite ctx already being canceled")
	}
}

func TestProvider_StreamQuotes_Replays(t *testing.T) {
	p := New(sampleData(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var got int
	err := p.StreamQuotes(ctx, "BTCUSDT", func(_ context.Context, _ ingestion.ProviderQuote) error {
		got++
		return nil
	})
	if err != nil {
		t.Fatalf("StreamQuotes: %v", err)
	}
	if got != 1 {
		t.Fatalf("replayed %d quotes, want 1", got)
	}
}

func TestProvider_Instruments(t *testing.T) {
	p := New(sampleData(), 0)
	got, err := p.Instruments(context.Background())
	if err != nil {
		t.Fatalf("Instruments: %v", err)
	}
	if len(got) != 1 || got[0].Symbol != "BTCUSDT" {
		t.Fatalf("Instruments = %+v, want the one fixture instrument", got)
	}
}
