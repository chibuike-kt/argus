//go:build integration

package backtest

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/outcomes"
	"github.com/chibuike-kt/argus/internal/quant"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// This test exercises Run against the real schema and the real analysis
// engine, exactly as cmd/argus-worker drives it -- a handwritten fake
// cannot stand in for the engine's own persisted state (confirmed_swings,
// structure_events, structure_state) or the setups package's own
// immutability trigger and unique identity index (guide 10). Run with:
// go test -tags=integration ./internal/backtest/...
// against a Postgres with all migrations applied (make migrate-up), and
// ARGUS_TEST_DATABASE_URL pointing at it -- never the dev argus database
// (CLAUDE.md rule 7, ADR-019).

const testAlgorithmVersion = "swing-structure-v1-test"

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration test database unreachable at %s: %v (start it with: docker compose up -d postgres, then apply migrations)", dsn, err)
	}
	return db
}

func testInstrument(t *testing.T, db *sql.DB, label string) int64 {
	t.Helper()
	priceScale := quant.Scale(0) // integer prices, to keep fixture arithmetic readable
	qtyScale := quant.Scale(0)
	symbol := fmt.Sprintf("BT-%s-%s-%d", t.Name(), label, time.Now().UnixNano())

	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider:     "test-provider",
		NativeSymbol: symbol,
		AssetClass:   "crypto_perp",
		PriceScale:   &priceScale,
		QtyScale:     &qtyScale,
		TickSize:     1,
		QtyStepSize:  1,
	})
	if err != nil {
		t.Fatalf("create test instrument: %v", err)
	}
	return inst.ID
}

func et(minute int) time.Time {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(minute) * time.Minute)
}

type ohlcBar struct {
	minute               int
	open, high, low, cls int64
}

func feedOHLCCandles(t *testing.T, db *sql.DB, instrumentID int64, bars []ohlcBar) {
	t.Helper()
	store := marketdata.NewStore(db, clock.Real{})
	for _, b := range bars {
		openTime := clock.NewEventTime(et(b.minute))
		tick := marketdata.Tick{
			InstrumentID: instrumentID, OpenTime: openTime,
			Open: quant.Price(b.open), High: quant.Price(b.high), Low: quant.Price(b.low), Close: quant.Price(b.cls),
			Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
		}
		if _, err := store.UpsertForming(context.Background(), tick); err != nil {
			t.Fatalf("UpsertForming minute=%d: %v", b.minute, err)
		}
		if _, err := store.Close(context.Background(), instrumentID, openTime); err != nil {
			t.Fatalf("Close minute=%d: %v", b.minute, err)
		}
	}
}

// seedBullishEstablished seeds an already-established bullish structure
// (confirmed swing high 125, confirmed swing low 90) directly via the
// Store API, bypassing fractal detection -- the same shortcut
// internal/setups's own integration tests use to get a stable base to
// resolve a trade plan against, not to re-prove swing detection itself
// (already covered by internal/analysis's own integration tests).
func seedBullishEstablished(t *testing.T, store *analysis.Store, instrumentID int64) {
	t.Helper()
	now := time.Now().UTC()
	high, err := store.InsertSwing(context.Background(), analysis.NewSwing{
		InstrumentID: instrumentID, Timeframe: "1m", Type: analysis.SwingHigh,
		PivotTime: clock.NewEventTime(et(2)), Price: quant.Price(125),
		ConfirmedAt: clock.NewEventTime(et(4)), EvaluationTime: clock.NewEvaluationTime(now),
		AlgorithmVersion: testAlgorithmVersion, SourceCandleIDs: []int64{1, 2, 3, 4, 5},
	})
	if err != nil {
		t.Fatalf("seed swing high: %v", err)
	}
	low, err := store.InsertSwing(context.Background(), analysis.NewSwing{
		InstrumentID: instrumentID, Timeframe: "1m", Type: analysis.SwingLow,
		PivotTime: clock.NewEventTime(et(7)), Price: quant.Price(90),
		ConfirmedAt: clock.NewEventTime(et(9)), EvaluationTime: clock.NewEvaluationTime(now),
		AlgorithmVersion: testAlgorithmVersion, SourceCandleIDs: []int64{6, 7, 8, 9, 10},
	})
	if err != nil {
		t.Fatalf("seed swing low: %v", err)
	}
	if err := store.SetState(context.Background(), analysis.StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: analysis.DirectionBullish,
		ReferenceSwingHighID: &high.ID, ReferenceSwingLowID: &low.ID,
	}, now); err != nil {
		t.Fatalf("SetState: %v", err)
	}
}

// baseOrderBlockBars is internal/analysis's own
// TestIntegration_TradePlan_Bullish_FromQMR fixture (trade_plan_integration_test.go),
// reproduced verbatim: a bullish order block at zone [100,110] with
// protected low 70 (the sweep at minute 20) via a sweep-validated break
// (close 130 > the seeded swing high 125, minute 25); a QMR fail trigger
// at minute 30 (the order block's own zone swept, converting the POI
// from order_block to QMR -- so exactly one identity is ever created,
// never two); then structural liquidity (a swing low at 75) and a later
// confirmed swing high (160) that shifts premium/discount's equilibrium
// to 115, which is what makes the order block's own zone (high=110)
// qualify as "in discount" at all (without this, as this file's own
// debug run confirmed, ResolvePOI returns POINone even with a valid,
// structurally-backed order block present). That existing test
// independently verifies this resolves to entry=110 (zone top), stop=70
// (protected low), targets [125 (R=3/8), 160 (R=5/4)] -- this file's own
// scenario tails below start at minute 52, after every one of these
// base candles.
func baseOrderBlockBars() []ohlcBar {
	return []ohlcBar{
		{18, 100, 130, 92, 100},
		{19, 101, 128, 91, 101},
		{20, 95, 100, 70, 95}, // sweep + protected low (70)
		{21, 102, 128, 93, 102},
		{22, 103, 126, 94, 103},
		{23, 108, 110, 100, 102}, // the order block, zone [100,110]
		{24, 103, 120, 102, 118},
		{25, 130, 135, 120, 130}, // sweep-validated BOS, close 130 > 125
		{30, 102, 103, 95, 102},  // QMR fail: low(95) < 100, close(102) > 100 (doji: open=close)
		{38, 100, 105, 95, 100},
		{39, 100, 105, 95, 100},
		{40, 100, 105, 75, 100}, // structural-liquidity candidate low (75)
		{41, 100, 105, 95, 100},
		{42, 100, 105, 95, 100},
		{46, 98, 100, 95, 98},
		{47, 98, 100, 95, 98},
		{48, 100, 160, 95, 100}, // later confirmed swing high (160)
		{49, 98, 100, 95, 98},
		{50, 98, 100, 95, 98},
	}
}

// setupTestInstrument seeds the base order-block fixture on a fresh
// instrument, then feeds tail (the scenario-specific candles after the
// plan is detected).
func setupTestInstrument(t *testing.T, db *sql.DB, label string, tail []ohlcBar) int64 {
	t.Helper()
	instrumentID := testInstrument(t, db, label)
	store := analysis.NewStore(db)
	seedBullishEstablished(t, store, instrumentID)
	feedOHLCCandles(t, db, instrumentID, baseOrderBlockBars())
	feedOHLCCandles(t, db, instrumentID, tail)
	return instrumentID
}

func TestIntegration_Run_ThreeHandCheckedOutcomes(t *testing.T) {
	db := testDB(t)

	// The base fixture (baseOrderBlockBars) resolves to entry=110,
	// stop=70, targets=[125 (R=3/8), 160 (R=5/4)] -- independently
	// verified by internal/analysis's own
	// TestIntegration_TradePlan_Bullish_FromQMR -- with the setup
	// detected (and already triggered) at minute 50's own close, et(51)
	// (confirmed by this file's own debug run). Every scenario's tail
	// starts at minute 51, the first candle outcomes.Evaluate ever
	// considers for this setup.

	// Scenario A: fills at minute 51 (low 105 <= entry-tick 109), then
	// reaches the target (125) at minute 52 without ever reaching the
	// stop (70) on either candle -- target_hit, R = 3/8.
	targetHitID := setupTestInstrument(t, db, "target-hit", []ohlcBar{
		{51, 105, 108, 105, 106},
		{52, 106, 130, 104, 128},
	})

	// Scenario B: the SAME candle (minute 51) both fills (low 68 <= 109)
	// and reaches the stop (low 68 <= 70) -- stopped, at the fill candle
	// itself (spec section 16: the fill candle checks the stop only).
	stoppedID := setupTestInstrument(t, db, "stopped", []ohlcBar{
		{51, 105, 108, 68, 70},
	})

	// Scenario C: fills at minute 51, then drifts at minute 52 without
	// reaching either the stop or the target -- open, data ends there.
	openID := setupTestInstrument(t, db, "open", []ohlcBar{
		{51, 105, 108, 105, 106},
		{52, 106, 112, 103, 108},
	})

	params := Params{
		InstrumentIDs:    []int64{targetHitID, stoppedID, openID},
		Start:            et(0),
		End:              et(55),
		AlgorithmVersion: testAlgorithmVersion,
	}

	rpt1, err := Run(context.Background(), db, params)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	byInstrument := map[int64]Row{}
	for _, r := range rpt1.Rows {
		byInstrument[r.InstrumentID] = r
	}
	if len(byInstrument) != 3 {
		t.Fatalf("got %d rows, want 3 (one setup per instrument): %+v", len(byInstrument), rpt1.Rows)
	}

	// Hand-checked expectation 1: target_hit, R = 3/8.
	a := byInstrument[targetHitID]
	if a.Outcome.Status != outcomes.StatusTargetHit {
		t.Errorf("scenario A status = %v, want target_hit", a.Outcome.Status)
	}
	if a.Outcome.RAtFirstTarget == nil || a.Outcome.RAtFirstTarget.Cmp(big.NewRat(3, 8)) != 0 {
		t.Errorf("scenario A R = %v, want 3/8", a.Outcome.RAtFirstTarget)
	}
	if a.Outcome.Fill == nil || a.Outcome.FirstTarget == nil {
		t.Errorf("scenario A outcome missing Fill/FirstTarget: %+v", a.Outcome)
	}

	// Hand-checked expectation 2: stopped, at the fill candle itself.
	b := byInstrument[stoppedID]
	if b.Outcome.Status != outcomes.StatusStopped {
		t.Errorf("scenario B status = %v, want stopped", b.Outcome.Status)
	}
	if b.Outcome.Fill == nil || b.Outcome.Stop == nil || b.Outcome.Fill.ID != b.Outcome.Stop.ID {
		t.Errorf("scenario B expected Fill and Stop to be the same candle: %+v", b.Outcome)
	}

	// Hand-checked expectation 3: open -- filled, nothing else reached.
	c := byInstrument[openID]
	if c.Outcome.Status != outcomes.StatusOpen {
		t.Errorf("scenario C status = %v, want open", c.Outcome.Status)
	}
	if c.Outcome.Fill == nil {
		t.Errorf("scenario C expected a fill, got none: %+v", c.Outcome)
	}

	md1 := rpt1.RenderMarkdown()

	rpt2, err := Run(context.Background(), db, params)
	if err != nil {
		t.Fatalf("Run (second pass): %v", err)
	}
	md2 := rpt2.RenderMarkdown()

	if md1 != md2 {
		t.Fatalf("Run is not deterministic: two runs over the same data produced different reports\n--- first ---\n%s\n--- second ---\n%s", md1, md2)
	}
}
