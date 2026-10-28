//go:build integration

package analysis

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// Batch 4 golden fixtures, strategy-spec.md v0.6 section 9: every
// bullet in the spec's batch-4 golden-fixtures list. Range resolution
// loads swings, structure events, and the candle series, so these run
// against real Postgres (ADR-009, ADR-019).
//
// Every fixture below builds its second fib from the same base shape as
// sweep_integration_test.go's TestIntegration_SweepValidatedBOS_Bullish_
// OppositeSideDifferentLevel (bullish) or its bearish mirror: a
// protected low (or high) established by a sweep, then a sweep-
// validated break referencing the opposing reference swing. The
// resulting bullish second fib is [70, 125], equilibrium 97
// (70 + (125-70)/2); the bearish mirror is [90, 150], equilibrium 120.

func newTestCandleSource(db *sql.DB) CandleSource {
	return NewMarketdataSource(marketdata.NewStore(db, clock.Real{}))
}

func TestIntegration_StructuralLiquidity_QualifyingBullish(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// Second fib [70, 125], eq=97. A new swing low (80, discount half)
	// with a following swing high (110) giving it a shape-qualifying
	// up-leg; its down-leg (back to H1 at pivot 2) is easily long enough.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 80, 100}, // candidate swing low (80)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 110, 95, 100}, // following swing high (110)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidate, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(40)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (candidate): %v", err)
	}
	if candidate.Price != 80 {
		t.Fatalf("candidate price = %v, want 80", candidate.Price)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	result, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveStructuralLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveStructuralLiquidity ok = false, want true: a valid second fib exists")
	}
	if result.Status != StructuralPresent || result.Swing.ID != candidate.ID {
		t.Fatalf("result = %+v, want StructuralPresent with swing id %d (the qualifying discount-half low, 80)", result, candidate.ID)
	}
}

func TestIntegration_StructuralLiquidity_QualifyingBearishMirror(t *testing.T) {
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
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 105, 95, 100},
		{19, 108, 96, 102},
		{20, 150, 100, 115}, // sweep + protected high (150)
		{21, 107, 98, 103},
		{22, 106, 97, 104},
		{25, 80, 65, 70}, // sweep-validated BOS below L1 (90)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// Second fib [90, 150], eq=120. A new swing high (130, premium half)
	// with a following swing low (80) giving it a shape-qualifying leg.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 100, 95, 98},
		{39, 100, 95, 98},
		{40, 130, 95, 100}, // candidate swing high (130)
		{41, 100, 95, 98},
		{42, 100, 95, 98},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 100, 80, 95}, // following swing low (80)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidate, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingHigh, clock.NewEventTime(et(40)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (candidate): %v", err)
	}
	if candidate.Price != 130 {
		t.Fatalf("candidate price = %v, want 130", candidate.Price)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	result, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveStructuralLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveStructuralLiquidity ok = false, want true")
	}
	if result.Status != StructuralPresent || result.Swing.ID != candidate.ID {
		t.Fatalf("result = %+v, want StructuralPresent with swing id %d (the qualifying premium-half high, 130)", result, candidate.ID)
	}
}

// TestIntegration_StructuralLiquidity_DisqualifiedByPremiumHalf proves a
// candidate above equilibrium (the wrong half for a bullish structure)
// is skipped, and the scan correctly falls back to an older swing that
// IS in the qualifying half.
func TestIntegration_StructuralLiquidity_DisqualifiedByPremiumHalf(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70) -- the fallback candidate
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// Second fib [70, 125], eq=97. A new swing high (135, well clear of
	// H1) gives the protected low a following leg. A more recent swing
	// low at 110 (premium half: 110 > 97) must be skipped -- and is
	// given its OWN complete, shape-qualifying legs (preceding 30,
	// following 48) so this fixture actually exercises the premium-half
	// filter, not merely a missing up-leg: were that filter removed,
	// this candidate would otherwise wrongly qualify.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{28, 100, 90, 98},
		{29, 100, 90, 98},
		{30, 135, 90, 98}, // following swing high for the protected low
		{31, 100, 90, 98},
		{32, 100, 90, 98},
		{36, 125, 115, 118},
		{37, 125, 115, 118},
		{38, 125, 115, 118},
		{39, 125, 115, 118},
		{40, 125, 110, 118}, // candidate swing low (110) -- premium half, disqualified
		{41, 125, 115, 118},
		{42, 125, 115, 118},
		{46, 125, 115, 118},
		{47, 125, 115, 118},
		{48, 140, 115, 118}, // following swing high for the candidate's own up-leg
		{49, 125, 115, 118},
		{50, 125, 115, 118},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(28), et(51)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidate, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(40)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (candidate): %v", err)
	}
	if candidate.Price != 110 {
		t.Fatalf("candidate price = %v, want 110", candidate.Price)
	}
	protectedLow, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	result, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveStructuralLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveStructuralLiquidity ok = false, want true")
	}
	if result.Status != StructuralPresent || result.Swing.ID != protectedLow.ID {
		t.Fatalf("result = %+v, want StructuralPresent with swing id %d (the protected low, 70) -- NOT the premium-half candidate %d (110)", result, protectedLow.ID, candidate.ID)
	}
}

// TestIntegration_StructuralLiquidity_DisqualifiedByNotIntact proves a
// candidate that is otherwise qualifying is excluded once a later
// finalized candle trades through it, and that this check is applied
// to every candidate in the scan, not just the most recent one -- a
// breach deep enough to invalidate all current swing lows correctly
// yields structural-absent, not a wrong fallback.
func TestIntegration_StructuralLiquidity_DisqualifiedByNotIntact(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// A new swing low (80) with a following swing high (110) -- shape-
	// qualifying -- then a later finalized candle at minute 55 trades
	// down to 65, below every current swing low (65 < 80, 70, and 90).
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 80, 100}, // candidate swing low (80)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 110, 95, 100}, // following swing high (110)
		{49, 100, 95, 98},
		{50, 100, 95, 98},
		{55, 100, 65, 85}, // breaches every current swing low
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(56)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	result, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveStructuralLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveStructuralLiquidity ok = false, want true: the second fib itself is still valid")
	}
	if result.Status != StructuralAbsent {
		t.Fatalf("result = %+v, want StructuralAbsent: the breach at minute 55 (low 65) traded through every current swing low", result)
	}
}

// TestIntegration_StructuralLiquidity_ShortLegDisqualified proves the
// shape test's arithmetic itself rejects a candidate whose down-leg
// spans fewer than minLegSpan candles, even though both its legs exist
// (a missing leg and a too-short leg must be distinguished, and both
// must disqualify) -- and falls back to an older, properly-shaped swing.
func TestIntegration_StructuralLiquidity_ShortLegDisqualified(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70) -- the fallback candidate
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// A new swing high at pivot 38 sits only 2 candles before a new
	// swing low at pivot 40 -- a down-leg span of 2, short of the
	// default minLegSpan (3) -- even though a valid following high (48)
	// exists. The protected low's own shape is still fine: its
	// following leg is this same new high at 38.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{36, 100, 95, 98},
		{37, 100, 95, 98},
		{38, 115, 95, 98}, // new swing high, only 2 candles before the candidate low
		{39, 100, 95, 98},
		{40, 100, 80, 98}, // candidate swing low (80) -- short down-leg
		{41, 100, 95, 98},
		{42, 100, 95, 98},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 105, 95, 98}, // following swing high for the candidate's up-leg
		{49, 100, 95, 98},
		{50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(36), et(51)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidate, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(40)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (candidate): %v", err)
	}
	if candidate.Price != 80 {
		t.Fatalf("candidate price = %v, want 80", candidate.Price)
	}
	protectedLow, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	result, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveStructuralLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveStructuralLiquidity ok = false, want true")
	}
	if result.Status != StructuralPresent || result.Swing.ID != protectedLow.ID {
		t.Fatalf("result = %+v, want StructuralPresent with swing id %d (the protected low, 70) -- NOT the short-down-leg candidate %d (80)", result, protectedLow.ID, candidate.ID)
	}
}

// TestIntegration_StructuralLiquidity_MoreRecentSupersedesEarlier proves
// that when two confirmed swing lows both qualify, the more recent one
// by pivot time is the structural liquidity, not merely the first one
// to have ever qualified.
func TestIntegration_StructuralLiquidity_MoreRecentSupersedesEarlier(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}

	// Candidate A (80) qualifies, shape-complete via the swing high at
	// 48. Candidate B (85), confirmed later, also qualifies, shape-
	// complete via the swing high at 68 (and the same high at 48 as its
	// own down-leg). B must be the one returned.
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 80, 100}, // candidate A (80)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 90, 98},
		{47, 100, 90, 98},
		{48, 110, 90, 98}, // swing high: A's up-leg, B's down-leg
		{49, 100, 90, 98},
		{50, 100, 90, 98},
		{58, 100, 90, 98},
		{59, 100, 90, 98},
		{60, 100, 85, 98}, // candidate B (85), more recent than A
		{61, 100, 90, 98},
		{62, 100, 90, 98},
		{66, 100, 90, 98},
		{67, 100, 90, 98},
		{68, 105, 90, 98}, // swing high: B's up-leg
		{69, 100, 90, 98},
		{70, 100, 90, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(71)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidateB, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(60)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (candidate B): %v", err)
	}
	if candidateB.Price != 85 {
		t.Fatalf("candidate B price = %v, want 85", candidateB.Price)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	result, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(90), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveStructuralLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveStructuralLiquidity ok = false, want true")
	}
	if result.Status != StructuralPresent || result.Swing.ID != candidateB.ID {
		t.Fatalf("result = %+v, want StructuralPresent with swing id %d (candidate B, 85, the more recent qualifying swing)", result, candidateB.ID)
	}
}

// TestIntegration_StructuralLiquidity_StructuralAbsent proves the
// inducement case: a valid second fib with no confirmed swing yet
// shape-complete (no swing of the opposite type has confirmed after
// either current swing low) correctly classifies as structural-absent,
// not undefined -- the second fib itself is perfectly valid.
func TestIntegration_StructuralLiquidity_StructuralAbsent(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	result, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(30), time.Minute, DefaultMinLegSpan)
	if err != nil {
		t.Fatalf("ResolveStructuralLiquidity: %v", err)
	}
	if !ok {
		t.Fatal("ResolveStructuralLiquidity ok = false, want true: the second fib [70,125] is valid")
	}
	if result.Status != StructuralAbsent {
		t.Fatalf("result = %+v, want StructuralAbsent: no swing high has confirmed after either current swing low yet, so neither can pass the shape test", result)
	}
}

// TestIntegration_StructuralLiquidity_RecomputesUnderCorrection proves
// the classification recomputes deterministically from corrected candle
// data: before the correction, the newer candidate (80) is intact and
// wins; a correction that drives a later candle's low to 75 (through
// 80 but not through the older protected low at 70) changes the result
// without touching any swing or structure-event row.
func TestIntegration_StructuralLiquidity_RecomputesUnderCorrection(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	e := newTestEngine(t, db)
	store := NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})
	candles := newTestCandleSource(db)

	seedBullishEstablished(t, store, instrumentID)
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{18, 130, 92, 100},
		{19, 128, 91, 101},
		{20, 100, 70, 95}, // sweep + protected low (70)
		{21, 128, 93, 102},
		{22, 126, 94, 103},
		{25, 135, 120, 130}, // sweep-validated BOS above H1 (125)
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatalf("EvaluateRange (base): %v", err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100},
		{39, 105, 95, 100},
		{40, 105, 80, 100}, // candidate swing low (80)
		{41, 105, 95, 100},
		{42, 105, 95, 100},
		{46, 100, 95, 98},
		{47, 100, 95, 98},
		{48, 110, 95, 100}, // following swing high (110) -- also the protected low's
		{49, 100, 95, 98},
		{50, 100, 95, 98}, // this candle is corrected below
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatalf("EvaluateRange: %v", err)
	}

	candidate, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(40)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (candidate): %v", err)
	}
	protectedLow, err := store.GetCurrentSwing(context.Background(), instrumentID, "1m", SwingLow, clock.NewEventTime(et(20)))
	if err != nil {
		t.Fatalf("GetCurrentSwing (protected low): %v", err)
	}

	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	before, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, state, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil || !ok {
		t.Fatalf("ResolveStructuralLiquidity (before): ok=%v err=%v", ok, err)
	}
	if before.Status != StructuralPresent || before.Swing.ID != candidate.ID {
		t.Fatalf("before = %+v, want StructuralPresent with swing id %d (80)", before, candidate.ID)
	}

	// Correct minute 50's low from 95 to 75: breaches the candidate (80)
	// but not the protected low (70).
	_, err = mdStore.SupersedeCandle(context.Background(), instrumentID, clockEventTime(50), marketdata.Correction{
		Open: 98, High: 100, Low: 75, Close: 98, Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}
	if err := e.HandleCandleCorrection(context.Background(), instrumentID, "1m", et(50)); err != nil {
		t.Fatalf("HandleCandleCorrection: %v", err)
	}

	stateAfter, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("GetState (after): %v", err)
	}
	after, ok, err := ResolveStructuralLiquidity(context.Background(), store, candles, stateAfter, et(60), time.Minute, DefaultMinLegSpan)
	if err != nil || !ok {
		t.Fatalf("ResolveStructuralLiquidity (after): ok=%v err=%v", ok, err)
	}
	if after.Status != StructuralPresent || after.Swing.ID != protectedLow.ID {
		t.Fatalf("after = %+v, want StructuralPresent with swing id %d (70): the corrected candle breaches the newer candidate but not the protected low", after, protectedLow.ID)
	}
}
