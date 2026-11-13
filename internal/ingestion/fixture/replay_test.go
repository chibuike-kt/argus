package fixture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
)

func TestScriptedProvider_EmitsInScriptOrder(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []ScriptedEvent{
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base, Close: "1"}},
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base.Add(time.Minute), Close: "2"}},
		// Out of order: an earlier OpenTime arriving after a later one.
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base.Add(-time.Minute), Close: "3"}},
	}
	p := NewScriptedProvider([]Script{{Symbol: "X", Events: events}}, clock.Real{}, 0, func(context.Context, time.Duration) {})

	var got []string
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := p.StreamCandles(ctx, "X", func(_ context.Context, c ingestion.ProviderCandle) error {
		got = append(got, c.Close)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	want := []string{"1", "2", "3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q (script order, not OpenTime order)", i, got[i], want[i])
		}
	}
}

func TestScriptedProvider_DisconnectStopsReplayAndReturnsUnwrapped(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []ScriptedEvent{
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base, Close: "1"}},
		{Disconnect: true},
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base.Add(time.Minute), Close: "never reached"}},
	}
	p := NewScriptedProvider([]Script{{Symbol: "X", Events: events}}, clock.Real{}, 0, func(context.Context, time.Duration) {})

	var got []string
	err := p.StreamCandles(context.Background(), "X", func(_ context.Context, c ingestion.ProviderCandle) error {
		got = append(got, c.Close)
		return nil
	})
	if !errors.Is(err, ErrScriptedDisconnect) {
		t.Fatalf("err = %v, want errors.Is ErrScriptedDisconnect", err)
	}
	if len(got) != 1 || got[0] != "1" {
		t.Fatalf("got %v, want exactly [\"1\"]: nothing after the disconnect", got)
	}
}

func TestScriptedProvider_HandleErrorStopsReplayUnwrapped(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []ScriptedEvent{
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base, Close: "1"}},
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base.Add(time.Minute), Close: "2"}},
	}
	p := NewScriptedProvider([]Script{{Symbol: "X", Events: events}}, clock.Real{}, 0, func(context.Context, time.Duration) {})

	wantErr := errors.New("handler boom")
	calls := 0
	err := p.StreamCandles(context.Background(), "X", func(context.Context, ingestion.ProviderCandle) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want errors.Is wantErr (never wrapped, per ingestion.Provider's own contract)", err)
	}
	if calls != 1 {
		t.Fatalf("handle called %d times, want exactly 1 (stop at the first error)", calls)
	}
}

func TestScriptedProvider_SpeedGovernsPacingNotContent(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []ScriptedEvent{
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base, Close: "1"}},
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base.Add(time.Minute), Close: "2"}},
	}

	var slept []time.Duration
	p := NewScriptedProvider(
		[]Script{{Symbol: "X", Events: events}},
		clock.Real{}, 2, // speed=2: half the real gap
		func(_ context.Context, d time.Duration) { slept = append(slept, d) },
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.StreamCandles(ctx, "X", func(context.Context, ingestion.ProviderCandle) error { return nil }); err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	if len(slept) != 1 || slept[0] != 30*time.Second {
		t.Fatalf("slept = %v, want exactly one 30s sleep (1 minute gap / speed 2)", slept)
	}
}

func TestScriptedProvider_ZeroSpeedNeverSleeps(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []ScriptedEvent{
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base, Close: "1"}},
		{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base.Add(time.Minute), Close: "2"}},
	}
	slept := false
	p := NewScriptedProvider([]Script{{Symbol: "X", Events: events}}, clock.Real{}, 0, func(context.Context, time.Duration) { slept = true })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.StreamCandles(ctx, "X", func(context.Context, ingestion.ProviderCandle) error { return nil }); err != nil {
		t.Fatalf("StreamCandles: %v", err)
	}
	if slept {
		t.Fatal("speed <= 0 slept anyway, want instant replay")
	}
}

func TestScriptedProvider_Determinism(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newEvents := func() []ScriptedEvent {
		return []ScriptedEvent{
			{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base, Close: "1"}},
			{Candle: ingestion.ProviderCandle{Symbol: "X", OpenTime: base.Add(time.Minute), Close: "2"}},
		}
	}

	run := func() []string {
		p := NewScriptedProvider([]Script{{Symbol: "X", Events: newEvents()}}, clock.Real{}, 0, func(context.Context, time.Duration) {})
		var got []string
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.StreamCandles(ctx, "X", func(_ context.Context, c ingestion.ProviderCandle) error {
			got = append(got, c.Close)
			return nil
		})
		return got
	}

	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("first=%v second=%v: different lengths", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("first=%v second=%v: differ at index %d", first, second, i)
		}
	}
}
