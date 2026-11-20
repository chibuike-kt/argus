//go:build integration

package analysis

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Phase 6.1 (D6): the evidence chain recorded with a plan. The same
// hand-built bullish QMR structure as TestIntegration_TradePlan_Bullish_FromQMR
// (sweep and protected low at minute 20, order block at 23, break at 25,
// QMR fail at 30, structural low 75 at 40, later high 160 at 48), so every
// value below is known by construction.
func bullishQMRScenario(t *testing.T) (*Store, CandleSource, StateSnapshot) {
	t.Helper()
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
		{25, 130, 135, 120, 130},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(18), et(26)); err != nil {
		t.Fatal(err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{{30, 103, 95, 102}})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(30), et(31)); err != nil {
		t.Fatal(err)
	}
	feedCloseCandles(t, db, instrumentID, []closeBar{
		{38, 105, 95, 100}, {39, 105, 95, 100}, {40, 105, 75, 100}, {41, 105, 95, 100}, {42, 105, 95, 100},
		{46, 100, 95, 98}, {47, 100, 95, 98}, {48, 160, 95, 100}, {49, 100, 95, 98}, {50, 100, 95, 98},
	})
	if err := e.EvaluateRange(context.Background(), instrumentID, "1m", et(38), et(51)); err != nil {
		t.Fatal(err)
	}
	state, err := store.GetState(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatal(err)
	}
	return store, candles, state
}

func stepByKind(t *testing.T, ev *PlanEvidence, kind, role string) EvidenceStep {
	t.Helper()
	for _, s := range ev.Steps {
		if s.Kind == kind && s.Role == role {
			return s
		}
	}
	t.Fatalf("no %s/%s step in %+v", kind, role, ev.Steps)
	return EvidenceStep{}
}

func level(t *testing.T, s EvidenceStep, name string) EvidenceLevel {
	t.Helper()
	for _, l := range s.Levels {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("step %s has no level %q: %+v", s.Kind, name, s.Levels)
	return EvidenceLevel{}
}

func TestIntegration_Evidence_RecordsWhatTheDecisionUsed(t *testing.T) {
	store, candles, state := bullishQMRScenario(t)
	ctx := context.Background()
	plan, ok, err := ResolveTradePlan(ctx, store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
	if err != nil || !ok {
		t.Fatalf("ResolveTradePlan: ok=%v err=%v", ok, err)
	}
	ev := plan.Evidence
	if ev == nil {
		t.Fatal("plan.Evidence is nil")
	}

	// The story, in order.
	var kinds []string
	for _, s := range ev.Steps {
		kinds = append(kinds, s.Kind+"/"+s.Role)
	}
	want := []string{"swing/swept", "sweep/", "swing/protected", "break/", "premium_discount/", "structural_liquidity/", "qmr/", "poi/", "plan/"}
	if len(kinds) != len(want) {
		t.Fatalf("steps = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("steps = %v, want %v", kinds, want)
		}
	}

	// The break is exactly the one the order block was anchored to.
	ob, obOK, err := ResolveOrderBlock(ctx, store, candles, state, et(55))
	if err != nil || !obOK {
		t.Fatalf("ResolveOrderBlock: ok=%v err=%v", obOK, err)
	}
	brk := stepByKind(t, ev, StepBreak, "")
	if brk.Attrs["structure_event"] != itoa(ob.BreakEventID) {
		t.Fatalf("break step names event %s, want the order block's own break %d", brk.Attrs["structure_event"], ob.BreakEventID)
	}
	if !brk.Candles[0].OpenTime.Equal(et(25)) {
		t.Fatalf("break candle = %v, want minute 25", brk.Candles[0].OpenTime)
	}

	// Sweep at minute 20 (wick 70), protected low 70, zone [100,110].
	sweep := stepByKind(t, ev, StepSweep, "")
	if !sweep.Candles[0].OpenTime.Equal(et(20)) || level(t, sweep, "penetration_extreme").Price != 70 {
		t.Fatalf("sweep = %+v, want minute 20 with extreme 70", sweep)
	}
	if level(t, stepByKind(t, ev, StepSwing, "protected"), "swing").Price != 70 {
		t.Fatal("protected swing is not the 70 low")
	}
	poi := stepByKind(t, ev, StepPOI, "")
	if poi.Attrs["poi_type"] != "qmr" || level(t, poi, "zone_low").Price != 100 || level(t, poi, "zone_high").Price != 110 || !poi.Candles[0].OpenTime.Equal(et(23)) {
		t.Fatalf("poi = %+v, want the minute-23 QMR zone [100,110]", poi)
	}

	// Bullish: discount, met; structural low 75; QMR met.
	pd := stepByKind(t, ev, StepPremiumDiscount, "")
	if pd.Attrs["zone"] != "discount" || pd.Result != EvidenceMet {
		t.Fatalf("premium_discount = %+v, want discount met", pd)
	}
	if level(t, stepByKind(t, ev, StepStructuralLiquidity, ""), "structural_swing").Price != 75 {
		t.Fatal("structural liquidity is not the 75 low")
	}
	if q := stepByKind(t, ev, StepQMR, ""); q.Result != EvidenceMet {
		t.Fatalf("qmr = %+v, want met", q)
	}

	// The plan: entry 110, stop 70, targets 125 (3/8) and 160 (5/4).
	pl := stepByKind(t, ev, StepPlan, "")
	if level(t, pl, "entry").Price != 110 || level(t, pl, "stop").Price != 70 ||
		level(t, pl, "target_1").Price != 125 || level(t, pl, "target_2").Price != 160 {
		t.Fatalf("plan levels = %+v", pl.Levels)
	}
	if pl.Attrs["target_1_rr"] != "3/8" || pl.Attrs["target_2_rr"] != "5/4" {
		t.Fatalf("plan R:R = %v", pl.Attrs)
	}
	if pl.Attrs["target_1_source"] == "" || level(t, pl, "target_1").SwingID == 0 {
		t.Fatalf("target 1 has no source: %+v / %v", level(t, pl, "target_1"), pl.Attrs)
	}
}

// ADR-019: the same candles give the same evidence, byte for byte.
func TestIntegration_Evidence_Deterministic(t *testing.T) {
	store, candles, state := bullishQMRScenario(t)
	ctx := context.Background()
	var out [2][]byte
	for i := range out {
		plan, ok, err := ResolveTradePlan(ctx, store, candles, state, et(55), time.Minute, DefaultMinLegSpan, 1)
		if err != nil || !ok {
			t.Fatal(err)
		}
		b, err := json.Marshal(plan.Evidence)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = b
	}
	if string(out[0]) != string(out[1]) {
		t.Fatalf("evidence differs between two resolutions:\n%s\n%s", out[0], out[1])
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
