//go:build integration

package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// Batch 5 golden fixtures, strategy-spec.md v0.9.0 section 13: QMR and
// POI selection. Every fixture builds the same bullish/bearish base
// shape as order_block_integration_test.go (a protected anchor
// established by a sweep, then a sweep-validated break, with an order
// block identified in the impulse window via feedOHLCCandles), plus a
// qualifying structural-liquidity swing (section 9, mirroring
// structural_liquidity_integration_test.go's own shape) so the order
// block's structural-present and discount preconditions are satisfied.
//
// Bullish base (unless a fixture says otherwise): second fib [70,125],
// eq=97; order block at minute 23, zone [100,110]; structural liquidity
// is a swing low at 75 (discount half, <=97); the later-confirmed swing
// high at 160 pushes the premium/discount range (section 8) to
// [70,160], eq=115, which keeps the order block's zone (high=110) in
// discount. The bearish mirror negates all of this symmetrically.

func TestIntegration_POI_QMR_Bullish(t *testing.T) {
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
		{23, 108, 110, 100, 102}, // bearish -- the order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B: sweep-validated BOS, close 130 > H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	// Fail trigger: a finalized candle after the order block's own
	// candle sweeps its low (section 6 semantics: low strictly below
	// 100, close strictly back above it).
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 103, 95, 102}, // low(95) < OB.Low(100), close(102) > OB.Low(100)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100}, // structural-liquidity candidate low (75, discount half of [70,125])
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100}, // following swing high (160) -- pushes section 8's eq to 115
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	failCandleID := mustCandleIDAt(t, db, instrumentID, 30)
	obCandleID := mustCandleIDAt(t, db, instrumentID, 23)
	structSwing, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clockEventTime(40))
	if err != nil {
		t.Fatalf("GetCurrentSwing (structural liquidity): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	poi, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI: %v", err)
	}
	if poi.Type != POIQMR {
		t.Fatalf("poi.Type = %v, want POIQMR", poi.Type)
	}
	if poi.Direction != DirectionBullish {
		t.Fatalf("poi.Direction = %v, want Bullish", poi.Direction)
	}
	if poi.Low != 100 || poi.High != 110 {
		t.Fatalf("poi zone = [%d,%d], want [100,110] (the order block's own zone)", poi.Low, poi.High)
	}
	if poi.OrderBlockCandleID != obCandleID {
		t.Fatalf("poi.OrderBlockCandleID = %d, want %d", poi.OrderBlockCandleID, obCandleID)
	}
	if poi.QMRFailCandleID != failCandleID {
		t.Fatalf("poi.QMRFailCandleID = %d, want %d", poi.QMRFailCandleID, failCandleID)
	}
	if poi.StructuralSwingID != structSwing.ID {
		t.Fatalf("poi.StructuralSwingID = %d, want %d", poi.StructuralSwingID, structSwing.ID)
	}
	if !poi.InDiscount {
		t.Fatal("poi.InDiscount = false, want true")
	}
}

func TestIntegration_POI_QMR_BearishMirror(t *testing.T) {
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
		{20, 115, 150, 100, 115}, // sweep + protected high (150)
		{21, 103, 107, 98, 103},
		{22, 104, 106, 97, 104},
		{23, 95, 100, 90, 98}, // bullish -- the order block, zone [90,100]
		{24, 98, 99, 80, 82},
		{25, 70, 80, 65, 70}, // B: sweep-validated BOS, close 70 < L1 (90)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 110, 90, 95}, // fail: high(110) > OB.High(100), close(95) < OB.High(100)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 100, 95, 98},
		{39, 100, 95, 98},
		{40, 135, 95, 100}, // structural-liquidity candidate high (135, premium half of [90,150])
		{41, 100, 95, 98},
		{42, 100, 95, 98},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 100, 20, 98}, // following swing low (20) -- pulls section 8's eq to 85
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	failCandleID := mustCandleIDAt(t, db, instrumentID, 30)
	obCandleID := mustCandleIDAt(t, db, instrumentID, 23)
	structSwing, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clockEventTime(40))
	if err != nil {
		t.Fatalf("GetCurrentSwing (structural liquidity): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	poi, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI: %v", err)
	}
	if poi.Type != POIQMR || poi.Direction != DirectionBearish {
		t.Fatalf("poi = %+v, want POIQMR, Bearish", poi)
	}
	if poi.Low != 90 || poi.High != 100 {
		t.Fatalf("poi zone = [%d,%d], want [90,100]", poi.Low, poi.High)
	}
	if poi.OrderBlockCandleID != obCandleID || poi.QMRFailCandleID != failCandleID {
		t.Fatalf("poi = %+v, want order-block candle %d and fail candle %d", poi, obCandleID, failCandleID)
	}
	if poi.StructuralSwingID != structSwing.ID {
		t.Fatalf("poi.StructuralSwingID = %d, want %d", poi.StructuralSwingID, structSwing.ID)
	}
}

// TestIntegration_POI_QMR_Spent_FallsBackToOrderBlock proves that once
// the QMR is spent (price left the zone, then returned), it is no
// longer offered, and POI selection falls back to the order block.
func TestIntegration_POI_QMR_Spent_FallsBackToOrderBlock(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 103, 95, 102},  // fail: low(95) < 100, close(102) > 100
		{31, 125, 115, 120}, // left: low(115) > OB.High(110), entirely above the zone
		{32, 105, 100, 103}, // returned: overlaps [100,110] again -- spent
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(33)); err != nil {
		t.Fatalf("EvaluateRange (fail, leave, return): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100},
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	obCandleID := mustCandleIDAt(t, db, instrumentID, 23)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	poi, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI: %v", err)
	}
	if poi.Type != POIOrderBlock {
		t.Fatalf("poi.Type = %v, want POIOrderBlock: the QMR is spent (left and returned), so it must fall back", poi.Type)
	}
	if poi.OrderBlockCandleID != obCandleID {
		t.Fatalf("poi.OrderBlockCandleID = %d, want %d", poi.OrderBlockCandleID, obCandleID)
	}
	if poi.QMRFailCandleID != 0 {
		t.Fatalf("poi.QMRFailCandleID = %d, want 0 (unused on an order-block selection)", poi.QMRFailCandleID)
	}
}

// TestIntegration_POI_QMR_BelowStructuralLiquidity_OrderBlockSelected
// proves that a failed, unspent QMR positioned below the structural
// liquidity (its low not strictly above the structural-liquidity low)
// is skipped; the order block is still selected because structural
// liquidity backs it and it is in discount. The fail candle (minute
// 30) is placed before the structural-liquidity candidate even pivots
// (minute 40), so its low does not count against that swing's own
// intactness scan (which only starts at the swing's confirmation,
// minute 43) -- keeping the two outcomes independently constructible.
func TestIntegration_POI_QMR_BelowStructuralLiquidity_OrderBlockSelected(t *testing.T) {
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
		{23, 78, 80, 72, 73}, // bearish -- the order block, zone [72,80] (close to the floor)
		{24, 76, 95, 74, 90},
		{25, 100, 135, 95, 130}, // B: close 130 > H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 75, 65, 74}, // fail: low(65) < OB.Low(72), close(74) > 72
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}
	// Structural liquidity at 85: ABOVE the order block's low (72), so
	// the QMR's "low strictly above the structural liquidity" check
	// fails, even though the fail trigger above is otherwise valid.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 85, 100}, // structural-liquidity candidate low (85, still <=97, the second fib's eq)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	obCandleID := mustCandleIDAt(t, db, instrumentID, 23)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	poi, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI: %v", err)
	}
	if poi.Type != POIOrderBlock {
		t.Fatalf("poi.Type = %v, want POIOrderBlock: the QMR exists but sits at or below the structural liquidity (72 vs 85) and must be skipped", poi.Type)
	}
	if poi.OrderBlockCandleID != obCandleID || poi.Low != 72 || poi.High != 80 {
		t.Fatalf("poi = %+v, want order-block candle %d, zone [72,80]", poi, obCandleID)
	}
}

// TestIntegration_POI_OrderBlockAlone proves the order block alone is
// the POI when no QMR exists (the order block was never swept) but
// structural liquidity backs it and it is in discount.
func TestIntegration_POI_OrderBlockAlone(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block, zone [100,110] -- never swept
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100},
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	obCandleID := mustCandleIDAt(t, db, instrumentID, 23)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	poi, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI: %v", err)
	}
	if poi.Type != POIOrderBlock {
		t.Fatalf("poi.Type = %v, want POIOrderBlock: no QMR ever formed", poi.Type)
	}
	if poi.OrderBlockCandleID != obCandleID || poi.Low != 100 || poi.High != 110 {
		t.Fatalf("poi = %+v, want order-block candle %d, zone [100,110]", poi, obCandleID)
	}
	if !poi.InDiscount {
		t.Fatal("poi.InDiscount = false, want true")
	}
}

// TestIntegration_POI_None_StructuralAbsent proves there is no POI when
// the structure is structural-absent (the inducement case), even
// though an order block is present: no structural liquidity swing is
// ever seeded, so section 9 classifies structural-absent.
func TestIntegration_POI_None_StructuralAbsent(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block present
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
	poi, err := ResolvePOI(context.Background(), store, candles, state, et(30), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI: %v", err)
	}
	if poi.Type != POINone {
		t.Fatalf("poi.Type = %v, want POINone: no structural liquidity swing exists, so the structure is structural-absent (inducement)", poi.Type)
	}
}

// TestIntegration_POI_PointInTimeSafe proves a QMR whose fail candle
// has not yet closed is not offered -- the order block is offered
// instead -- and becomes available once that candle closes.
func TestIntegration_POI_PointInTimeSafe(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102},
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100},
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}
	// Fail candle at minute 60, closing at minute 61.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{60, 103, 95, 102},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(60), et(61)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}

	obCandleID := mustCandleIDAt(t, db, instrumentID, 23)
	failCandleID := mustCandleIDAt(t, db, instrumentID, 60)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	before, err := ResolvePOI(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI (asOf=60): %v", err)
	}
	if before.Type != POIOrderBlock || before.OrderBlockCandleID != obCandleID {
		t.Fatalf("asOf=60: poi = %+v, want POIOrderBlock with candle %d: the fail candle closes at minute 61, strictly after asOf -- not yet known", before, obCandleID)
	}

	after, err := ResolvePOI(context.Background(), store, candles, state, et(61), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI (asOf=61): %v", err)
	}
	if after.Type != POIQMR || after.QMRFailCandleID != failCandleID {
		t.Fatalf("asOf=61: poi = %+v, want POIQMR with fail candle %d: the fail candle has now closed", after, failCandleID)
	}
}

// TestIntegration_POI_RecomputesUnderCorrection proves POI selection
// recomputes deterministically under a candle correction: correcting
// the fail candle's low so it no longer sweeps the order block's
// boundary flips the result from QMR back to the order block, with no
// caching involved -- ResolvePOI re-reads live candle data (via
// ResolveOrderBlock and resolveQMR) on every call.
func TestIntegration_POI_RecomputesUnderCorrection(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})
	candles := newTestCandleSource(db)

	// A real EventDirectionEstablished row, not seedBullishEstablished's
	// raw SetState, is required: HandleCandleCorrection below replays
	// from affectedFrom = 30 - RightBars*interval = 28, and
	// reconstructStateAsOf needs a real event strictly before that point
	// to recover direction from. The genuine BOS at minute 25 (trigger
	// candle at 25, well before 28) already satisfies this on its own --
	// unlike order_block.go's own correction fixture, which corrects a
	// candle inside the impulse window itself, before that BOS exists.
	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95},
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // B
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{30, 103, 95, 102}, // fail: low(95) < 100, close(102) > 100
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatalf("EvaluateRange (fail candle): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 75, 100},
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 160, 95, 100},
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange (structural liquidity): %v", err)
	}

	obCandleID := mustCandleIDAt(t, db, instrumentID, 23)

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	before, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI (before): %v", err)
	}
	if before.Type != POIQMR {
		t.Fatalf("before.Type = %v, want POIQMR", before.Type)
	}

	// Correct minute 30's low from 95 to 101: it no longer sweeps the
	// order block's low (100), so the fail trigger no longer fires.
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(30), marketdata.Correction{
		Open: 103, High: 103, Low: 101, Close: 102, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(30)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	after, err := ResolvePOI(context.Background(), store, candles, state, et(55), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolvePOI (after): %v", err)
	}
	if after.Type != POIOrderBlock || after.OrderBlockCandleID != obCandleID {
		t.Fatalf("after = %+v, want POIOrderBlock with candle %d: minute 30 no longer sweeps the order block, so no QMR exists", after, obCandleID)
	}
}
