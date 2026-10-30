//go:build integration

package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// Batch 5 golden fixtures, strategy-spec.md v0.8.0 section 12: order
// block. Every fixture builds the same bullish/bearish second-fib base
// shape as structural_liquidity_integration_test.go and
// transactional_liquidity_integration_test.go: a protected anchor
// established by a sweep, then a sweep-validated break referencing the
// opposing reference swing. The impulse window is [protected-anchor
// pivot_time, B's open_time); candle colour (open vs close) within it
// is controlled via feedOHLCCandles, since feedCloseCandles/feedCandles
// always produce dojis.

func TestIntegration_OrderBlock_Bullish(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70), window floor
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // bearish: close(102) < open(108) -- the order block
		{24, 103, 120, 102, 118}, // bullish: impulse continuation
		{25, 130, 135, 120, 130}, // B: sweep-validated BOS, close 130 > H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidate, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clockEventTime(20))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}
	breakEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", et(25))
	if err != nil {
		t.Fatalf("GetCurrentEvent (break): %v", err)
	}
	sourceCandleID := mustCandleIDAt(t, db, instrumentID, 23)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	ob, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(30))
	if err != nil {
		t.Fatalf("ResolveOrderBlock: %v", err)
	}
	if !ok {
		t.Fatal("ResolveOrderBlock ok = false, want true")
	}
	if ob.Status != OrderBlockPresent {
		t.Fatalf("ob.Status = %v, want OrderBlockPresent", ob.Status)
	}
	if ob.Direction != DirectionBullish {
		t.Fatalf("ob.Direction = %v, want Bullish", ob.Direction)
	}
	if ob.SourceCandleID != sourceCandleID {
		t.Fatalf("ob.SourceCandleID = %d, want %d (minute 23, the most recent bearish candle)", ob.SourceCandleID, sourceCandleID)
	}
	if ob.SourceRevision != 1 {
		t.Fatalf("ob.SourceRevision = %d, want 1", ob.SourceRevision)
	}
	if ob.BreakEventID != breakEvent.ID {
		t.Fatalf("ob.BreakEventID = %d, want %d", ob.BreakEventID, breakEvent.ID)
	}
	if ob.ProtectedSwingID != candidate.ID {
		t.Fatalf("ob.ProtectedSwingID = %d, want %d (the protected low)", ob.ProtectedSwingID, candidate.ID)
	}
	if ob.Low != 100 || ob.High != 110 {
		t.Fatalf("zone = [%d,%d], want [100,110] (minute 23's full candle range)", ob.Low, ob.High)
	}
}

func TestIntegration_OrderBlock_BearishMirror(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	h1 := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	l1 := seedSwing(t, store, instrumentID, SwingLow, 7, 90, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBearish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed live state: %v", err)
	}
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 105, 95, 100},
		{19, 102, 108, 96, 102},
		{20, 115, 150, 100, 115}, // sweep + protected high (150), window floor
		{21, 103, 107, 98, 103},
		{22, 104, 106, 97, 104},
		{23, 95, 100, 90, 98}, // bullish: close(98) > open(95) -- the order block
		{24, 98, 99, 80, 82},  // bearish: impulse continuation
		{25, 70, 80, 65, 70},  // B: sweep-validated BOS, close 70 < L1 (90)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	protectedHigh, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(20))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected high): %v", err)
	}
	breakEvent, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", et(25))
	if err != nil {
		t.Fatalf("GetCurrentEvent (break): %v", err)
	}
	sourceCandleID := mustCandleIDAt(t, db, instrumentID, 23)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	ob, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(30))
	if err != nil {
		t.Fatalf("ResolveOrderBlock: %v", err)
	}
	if !ok {
		t.Fatal("ResolveOrderBlock ok = false, want true")
	}
	if ob.Status != OrderBlockPresent || ob.Direction != DirectionBearish {
		t.Fatalf("ob = %+v, want OrderBlockPresent, Bearish", ob)
	}
	if ob.SourceCandleID != sourceCandleID {
		t.Fatalf("ob.SourceCandleID = %d, want %d (minute 23, the most recent bullish candle)", ob.SourceCandleID, sourceCandleID)
	}
	if ob.BreakEventID != breakEvent.ID {
		t.Fatalf("ob.BreakEventID = %d, want %d", ob.BreakEventID, breakEvent.ID)
	}
	if ob.ProtectedSwingID != protectedHigh.ID {
		t.Fatalf("ob.ProtectedSwingID = %d, want %d (the protected high)", ob.ProtectedSwingID, protectedHigh.ID)
	}
	if ob.Low != 90 || ob.High != 100 {
		t.Fatalf("zone = [%d,%d], want [90,100] (minute 23's full candle range)", ob.Low, ob.High)
	}
}

// TestIntegration_OrderBlock_DojiSkipped proves a doji nearer B does
// not get selected over an older, true opposite-colour candle.
func TestIntegration_OrderBlock_DojiSkipped(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // bearish: close(102) < open(108) -- the TRUE order block
		{24, 105, 106, 104, 105}, // doji: close(105) == open(105), nearer B -- must be skipped
		{25, 130, 135, 120, 130}, // B: sweep-validated BOS
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	trueCandidateID := mustCandleIDAt(t, db, instrumentID, 23)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	ob, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(30))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock: ok=%v err=%v", ok, err)
	}
	if ob.Status != OrderBlockPresent || ob.SourceCandleID != trueCandidateID {
		t.Fatalf("ob = %+v, want OrderBlockPresent with source candle %d (minute 23) -- NOT the doji at minute 24", ob, trueCandidateID)
	}
}

// TestIntegration_OrderBlock_Absent proves no qualifying opposite-colour
// candle in the window yields order-block-absent, a defined result.
func TestIntegration_OrderBlock_Absent(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 100, 110, 99, 105},  // bullish: same colour as the impulse
		{24, 105, 120, 104, 118}, // bullish
		{25, 130, 135, 120, 130}, // B: sweep-validated BOS
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidate, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clockEventTime(20))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	ob, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(30))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock: ok=%v err=%v", ok, err)
	}
	if ob.Status != OrderBlockAbsent {
		t.Fatalf("ob.Status = %v, want OrderBlockAbsent: no bearish candle anywhere in the window", ob.Status)
	}
	if ob.ProtectedSwingID != candidate.ID {
		t.Fatalf("ob.ProtectedSwingID = %d, want %d (still carried on the absent result)", ob.ProtectedSwingID, candidate.ID)
	}
}

// TestIntegration_OrderBlock_ProtectedAnchorFloor proves a qualifying
// opposite-colour candle positioned strictly before the protected
// anchor's pivot_time is excluded from the window -- it is outside the
// structure's origin, not the order block.
func TestIntegration_OrderBlock_ProtectedAnchorFloor(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 105, 106, 90, 95}, // bearish, BEFORE the floor (20) -- must be excluded
		{20, 95, 100, 70, 95},  // sweep + protected low (70), window floor
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 100, 110, 99, 105},  // bullish: same colour as the impulse, in-window
		{24, 105, 120, 104, 118}, // bullish, in-window
		{25, 130, 135, 120, 130}, // B: sweep-validated BOS
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	ob, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(30))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock: ok=%v err=%v", ok, err)
	}
	if ob.Status != OrderBlockAbsent {
		t.Fatalf("ob = %+v, want OrderBlockAbsent: the only bearish candle (minute 19) is before the protected-low floor (minute 20) and must be excluded from the window", ob)
	}
}

// TestIntegration_OrderBlock_TracksMostRecentBreak mirrors transactional
// liquidity's "110-then-118" fixture: after a second, later
// sweep-validated break in the same trend, the order block moves to the
// new leg's impulse window, which now extends all the way to the new B.
func TestIntegration_OrderBlock_TracksMostRecentBreak(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70), window floor throughout
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // bearish -- the FIRST order block, superseded below
		{24, 103, 120, 102, 118}, // bullish
		{25, 130, 135, 120, 130}, // B#1: establishing break, close 130 > H1 (125)
		{28, 128, 132, 125, 128},
		{29, 129, 134, 126, 129},
		{30, 131, 140, 127, 131}, // a brand-new, higher swing high (140)
		{31, 130, 133, 128, 130},
		{32, 130, 131, 129, 130},
		{33, 135, 136, 125, 128}, // bearish, closer to B#2 -- the NEW order block
		{35, 145, 148, 138, 145}, // B#2: later validated break, close 145 > the new high (140)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(36)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	newBreak, err := store.GetCurrentEvent(context.Background(), instrumentID, "1m", et(35))
	if err != nil {
		t.Fatalf("GetCurrentEvent (B#2): %v", err)
	}
	newCandidateID := mustCandleIDAt(t, db, instrumentID, 33)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	ob, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(40))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock: ok=%v err=%v", ok, err)
	}
	if ob.Status != OrderBlockPresent || ob.SourceCandleID != newCandidateID {
		t.Fatalf("ob = %+v, want OrderBlockPresent with source candle %d (minute 33, the new leg's impulse) -- NOT minute 23's candle from the establishing break's window", ob, newCandidateID)
	}
	if ob.BreakEventID != newBreak.ID {
		t.Fatalf("ob.BreakEventID = %d, want %d (B#2, the later validated break)", ob.BreakEventID, newBreak.ID)
	}
}

// TestIntegration_OrderBlock_PointInTimeSafe reuses the tracking
// fixture's exact shape: before B#2's trigger candle has closed, the
// order block is still anchored to B#1's (smaller) window; once it has
// closed, the order block moves to the new leg.
func TestIntegration_OrderBlock_PointInTimeSafe(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // bearish -- B#1's order block
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B#1
		{28, 128, 132, 125, 128},
		{29, 129, 134, 126, 129},
		{30, 131, 140, 127, 131}, // new swing high (140)
		{31, 130, 133, 128, 130},
		{32, 130, 131, 129, 130},
		{33, 135, 136, 125, 128}, // bearish -- B#2's order block
		{35, 145, 148, 138, 145}, // B#2, trigger candle closes at minute 36
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(36)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidateB1 := mustCandleIDAt(t, db, instrumentID, 23)
	candidateB2 := mustCandleIDAt(t, db, instrumentID, 33)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	before, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(35))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock (asOf=35): ok=%v err=%v", ok, err)
	}
	if before.Status != OrderBlockPresent || before.SourceCandleID != candidateB1 {
		t.Fatalf("asOf=35: ob = %+v, want OrderBlockPresent with source %d (minute 23): B#2's trigger candle closes at minute 36, strictly after asOf -- not yet known", before, candidateB1)
	}

	after, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(36))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock (asOf=36): ok=%v err=%v", ok, err)
	}
	if after.Status != OrderBlockPresent || after.SourceCandleID != candidateB2 {
		t.Fatalf("asOf=36: ob = %+v, want OrderBlockPresent with source %d (minute 33): B#2 has now closed", after, candidateB2)
	}
}

// TestIntegration_OrderBlock_RecomputesUnderCorrection proves the
// classification recomputes deterministically from corrected candle
// data: correcting the order-block candle's own open (without touching
// its high/low/close, so no swing or structure event is affected)
// flips its colour and changes the result, with no caching involved --
// ResolveOrderBlock re-reads live candle data on every call.
func TestIntegration_OrderBlock_RecomputesUnderCorrection(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})
	candles := newTestCandleSource(db)

	// A real EventDirectionEstablished row, not seedBullishEstablished's
	// raw SetState, is required here: the correction below replays from
	// affectedFrom=21, and reconstructStateAsOf needs an actual event
	// strictly before that point to recover direction from -- otherwise
	// it falls back to cold-start Undetermined (the same reason
	// sweep_integration_test.go's correction fixtures seed one).
	h1 := seedSwing(t, store, instrumentID, SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	l1 := seedSwing(t, store, instrumentID, SwingLow, 7, 90, []int64{6, 7, 8, 9, 10})
	establishTriggerID := feedCloseCandles(t, db, instrumentID, []closeBar{{1, 105, 95, 100}})[1]
	establishEvent, err := store.InsertEvent(context.Background(), NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: "1m", Type: EventDirectionEstablished,
		DirectionBefore: DirectionUndetermined, DirectionAfter: DirectionBullish,
		TriggerOpenTime: et(1), TriggerCandleID: establishTriggerID, ReferenceSwingIDs: []int64{h1.ID, h1.ID, l1.ID, l1.ID},
		EvaluationTime: clockEvalTime(1), AlgorithmVersion: testAlgorithmVersion,
	})
	if err != nil {
		t.Fatalf("seed establishment event: %v", err)
	}
	if err := store.SetState(context.Background(), StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: DirectionBullish,
		ReferenceSwingHighID: &h1.ID, ReferenceSwingLowID: &l1.ID, LastEventID: &establishEvent.ID,
	}, nowUTC()); err != nil {
		t.Fatalf("seed live state: %v", err)
	}

	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // bearish -- the only candidate before correction
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	before, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(30))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock (before): ok=%v err=%v", ok, err)
	}
	if before.Status != OrderBlockPresent {
		t.Fatalf("before = %+v, want OrderBlockPresent", before)
	}

	// Correct minute 23's open from 108 to 101: high/low/close are
	// unchanged, so no pivot, sweep, or break recomputes -- only the
	// candle's own colour flips, from bearish (102 < 108) to bullish
	// (102 > 101).
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(23), marketdata.Correction{
		Open: 101, High: 110, Low: 100, Close: 102, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(23)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	after, ok, err := ResolveOrderBlock(context.Background(), store, candles, state, et(30))
	if err != nil || !ok {
		t.Fatalf("ResolveOrderBlock (after): ok=%v err=%v", ok, err)
	}
	if after.Status != OrderBlockAbsent {
		t.Fatalf("after = %+v, want OrderBlockAbsent: minute 23 is now bullish, and no other bearish candle exists in the window", after)
	}
}
