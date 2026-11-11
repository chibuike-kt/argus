//go:build integration

package ingestion_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// live-chart-pipeline brief, Phase 2: fixture.ScriptedProvider is the
// test harness for everything after it. This file exercises every
// required scenario against the real ingestion.Service + a real
// Postgres store: forming updates, a close, a duplicate, an out of
// order message, a late message after close, a corrected closed
// candle, a gap, and (its own test below) a disconnect.

func TestIntegration_ScriptedReplay_AllScenariosProduceExpectedStoreState(t *testing.T) {
	db := testDB(t)
	instrumentID, symbol := testIngestionInstrument(t, db)
	store := marketdata.NewStore(db, clock.Real{})

	start := et(2026, time.October, 27, 15, 0)
	m0, m1, m2 := start, start.Add(time.Minute), start.Add(2*time.Minute)

	events := []fixture.ScriptedEvent{
		// Minute 0: two forming updates, then a close.
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m0, Open: "100.00", High: "101.00", Low: "99.00", Close: "100.50", Volume: "1.0", IsFinal: false}},
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m0, Open: "100.00", High: "103.00", Low: "99.00", Close: "102.00", Volume: "2.0", IsFinal: false}},
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m0, Open: "100.00", High: "105.00", Low: "99.00", Close: "104.00", Volume: "3.0", IsFinal: true}},
		// Minute 1 deliberately absent here: a gap, filled back in out of
		// order further down.
		// Minute 2: closes directly, single message.
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m2, Open: "110.00", High: "112.00", Low: "109.00", Close: "111.00", Volume: "4.0", IsFinal: true}},
		// Late message after close for minute 0: identical values --
		// must stay a no-op, not a correction (ingestion.Service's own
		// applyCandle, candleValuesEqual branch).
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m0, Open: "100.00", High: "105.00", Low: "99.00", Close: "104.00", Volume: "3.0", IsFinal: true}},
		// Duplicate for minute 2: exact same final message resent.
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m2, Open: "110.00", High: "112.00", Low: "109.00", Close: "111.00", Volume: "4.0", IsFinal: true}},
		// Corrected closed candle for minute 2: different final values.
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m2, Open: "110.00", High: "120.00", Low: "108.00", Close: "118.00", Volume: "4.5", IsFinal: true}},
		// Out of order: minute 1 finally arrives, after minute 2's own
		// correction -- the store keys by open_time, not arrival order,
		// so this must land correctly regardless of when it shows up.
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: m1, Open: "104.00", High: "106.00", Low: "103.00", Close: "105.00", Volume: "1.5", IsFinal: true}},
	}

	provider := fixture.NewScriptedProvider(
		[]fixture.Script{{Symbol: symbol, Events: events}},
		clock.Real{}, 0, func(context.Context, time.Duration) {}, // no delay: deterministic, instant
	)
	svc := testIngestionService(db, provider, marketdata.ContinuousCalendar{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.WatchCandles(ctx, symbol); err != nil {
		t.Fatalf("WatchCandles: %v", err)
	}

	// Minute 0: closed, unaffected by the later late-but-unchanged
	// message -- still revision 1.
	c0, err := store.GetCurrent(context.Background(), instrumentID, clock.NewEventTime(m0))
	if err != nil {
		t.Fatalf("GetCurrent minute 0: %v", err)
	}
	if !c0.IsClosed || c0.Revision != 1 || c0.Close != 10400 {
		t.Fatalf("minute 0 = %+v, want closed, revision 1, Close=10400 (late-unchanged message must not bump the revision)", c0)
	}

	// Minute 1: filled in out of order, closed correctly.
	c1, err := store.GetCurrent(context.Background(), instrumentID, clock.NewEventTime(m1))
	if err != nil {
		t.Fatalf("GetCurrent minute 1: %v", err)
	}
	if !c1.IsClosed || c1.Close != 10500 {
		t.Fatalf("minute 1 = %+v, want closed with Close=10500 (arrived out of order, after minute 2)", c1)
	}

	// Minute 2: duplicate kept it at revision 1, the later correction
	// bumped it to revision 2 with the corrected values.
	c2, err := store.GetCurrent(context.Background(), instrumentID, clock.NewEventTime(m2))
	if err != nil {
		t.Fatalf("GetCurrent minute 2: %v", err)
	}
	if !c2.IsClosed || c2.Revision != 2 || c2.Close != 11800 || c2.High != 12000 {
		t.Fatalf("minute 2 = %+v, want closed, revision 2, Close=11800 High=12000 (the duplicate must not bump revision, the correction must)", c2)
	}
}

func TestIntegration_ScriptedReplay_DisconnectPropagatesUnwrapped(t *testing.T) {
	db := testDB(t)
	_, symbol := testIngestionInstrument(t, db)

	start := et(2026, time.October, 27, 16, 0)
	events := []fixture.ScriptedEvent{
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: start, Open: "1.00", High: "1.00", Low: "1.00", Close: "1.00", Volume: "1.0", IsFinal: true}},
		{Disconnect: true},
		// Never reached: StreamCandles must stop at the disconnect.
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: start.Add(time.Minute), Open: "2.00", High: "2.00", Low: "2.00", Close: "2.00", Volume: "1.0", IsFinal: true}},
	}
	provider := fixture.NewScriptedProvider(
		[]fixture.Script{{Symbol: symbol, Events: events}},
		clock.Real{}, 0, func(context.Context, time.Duration) {},
	)
	svc := testIngestionService(db, provider, marketdata.ContinuousCalendar{})

	err := svc.WatchCandles(context.Background(), symbol)
	if !errors.Is(err, fixture.ErrScriptedDisconnect) {
		t.Fatalf("WatchCandles error = %v, want errors.Is ErrScriptedDisconnect (the provider contract: returned unwrapped)", err)
	}
}

// TestIntegration_ScriptedReplay_DeterministicAcrossTwoRuns proves "same
// script, same output" by replaying the identical script into two
// independently seeded instruments (never the same row twice: replaying
// a forming-then-close sequence a second time against a row the first
// run already closed would make the forming message look like a
// correction against the closed value, a real property of
// ingestion.Service's own existing applyCandle, not a determinism bug --
// this test isolates the replay process itself from that, the same way
// a from-scratch run always looks regardless of what ran before it).
func TestIntegration_ScriptedReplay_DeterministicAcrossTwoRuns(t *testing.T) {
	db := testDB(t)
	store := marketdata.NewStore(db, clock.Real{})

	start := et(2026, time.October, 27, 17, 0)
	scriptEvents := func(symbol string) []fixture.ScriptedEvent {
		return []fixture.ScriptedEvent{
			{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: start, Open: "50.00", High: "51.00", Low: "49.00", Close: "50.50", Volume: "1.0", IsFinal: false}},
			{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: start, Open: "50.00", High: "52.00", Low: "49.00", Close: "51.50", Volume: "2.0", IsFinal: true}},
		}
	}

	run := func() marketdata.Candle {
		instrumentID, symbol := testIngestionInstrument(t, db)
		provider := fixture.NewScriptedProvider(
			[]fixture.Script{{Symbol: symbol, Events: scriptEvents(symbol)}},
			clock.Real{}, 0, func(context.Context, time.Duration) {},
		)
		svc := testIngestionService(db, provider, marketdata.ContinuousCalendar{})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := svc.WatchCandles(ctx, symbol); err != nil {
			t.Fatalf("WatchCandles: %v", err)
		}
		c, err := store.GetCurrent(context.Background(), instrumentID, clock.NewEventTime(start))
		if err != nil {
			t.Fatalf("GetCurrent: %v", err)
		}
		return c
	}

	first := run()
	second := run()
	if first.Open != second.Open || first.Close != second.Close || first.High != second.High || first.Low != second.Low ||
		first.Volume != second.Volume || first.Revision != second.Revision || first.IsClosed != second.IsClosed {
		t.Fatalf("replay is not deterministic: first=%+v second=%+v", first, second)
	}
}
