//go:build integration

package setups

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// These tests exercise the real schema from db/migrations -- the
// setup_events immutability trigger and the unique identity index
// cannot be verified by a handwritten fake (guide 10) -- and, for
// TestIntegration_Reconcile_DetectedThenActionableThenTriggered, the
// real analysis.Engine and ResolveTradePlan chain, proving the actual
// wiring cmd/argus-worker/processing.go uses. Run with:
// go test -tags=integration ./internal/setups/...
// against a Postgres with all migrations applied (make migrate-up).

const testVersion = "swing-structure-v1-test"

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

func testInstrument(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)
	symbol := fmt.Sprintf("SETUPS-%s-%d", t.Name(), time.Now().UnixNano())

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

// ohlcBar is feedOHLCCandles' bar shape -- open and close matter (the
// order block's colour test), not just high/low.
type ohlcBar struct {
	minute               int
	open, high, low, cls int64
}

func feedOHLCCandles(t *testing.T, db *sql.DB, instrumentID int64, bars []ohlcBar) map[int]int64 {
	t.Helper()
	ids := make(map[int]int64, len(bars))
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
		closed, err := store.Close(context.Background(), instrumentID, openTime)
		if err != nil {
			t.Fatalf("Close minute=%d: %v", b.minute, err)
		}
		ids[b.minute] = closed.ID
	}
	return ids
}

// closeBar is feedOHLCCandles' simpler sibling for fixtures that only
// care about high/low/close (open == close, a doji), matching
// analysis's own closeBar/feedCloseCandles convention.
type closeBar struct {
	minute         int
	high, low, cls int64
}

func feedCloseCandles(t *testing.T, db *sql.DB, instrumentID int64, bars []closeBar) map[int]int64 {
	t.Helper()
	full := make([]ohlcBar, len(bars))
	for i, b := range bars {
		full[i] = ohlcBar{minute: b.minute, open: b.cls, high: b.high, low: b.low, cls: b.cls}
	}
	return feedOHLCCandles(t, db, instrumentID, full)
}

// seedSwing inserts a confirmed swing directly, bypassing fractal
// detection -- the same shortcut analysis's own integration tests use
// to seed an established direction without reproducing a full cold-
// start sequence (this package only needs a stable base to resolve a
// trade plan against, not to prove swing detection itself).
func seedSwing(t *testing.T, store *analysis.Store, instrumentID int64, swingType analysis.SwingType, minute int, price int64, sourceIDs []int64) analysis.Swing {
	t.Helper()
	now := time.Now().UTC()
	s, err := store.InsertSwing(context.Background(), analysis.NewSwing{
		InstrumentID: instrumentID, Timeframe: "1m", Type: swingType,
		PivotTime: clock.NewEventTime(et(minute)), Price: quant.Price(price),
		ConfirmedAt: clock.NewEventTime(et(minute + 2)), EvaluationTime: clock.NewEvaluationTime(now),
		AlgorithmVersion: testVersion, SourceCandleIDs: sourceIDs,
	})
	if err != nil {
		t.Fatalf("seedSwing: %v", err)
	}
	return s
}

func seedBullishEstablished(t *testing.T, store *analysis.Store, instrumentID int64) (highID, lowID int64) {
	t.Helper()
	high := seedSwing(t, store, instrumentID, analysis.SwingHigh, 2, 125, []int64{1, 2, 3, 4, 5})
	low := seedSwing(t, store, instrumentID, analysis.SwingLow, 7, 90, []int64{6, 7, 8, 9, 10})
	if err := store.SetState(context.Background(), analysis.StateSnapshot{
		InstrumentID: instrumentID, Timeframe: "1m", Direction: analysis.DirectionBullish,
		ReferenceSwingHighID: &high.ID, ReferenceSwingLowID: &low.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	return high.ID, low.ID
}

func newTestEngine(t *testing.T, db *sql.DB) *analysis.Engine {
	t.Helper()
	mdStore := marketdata.NewStore(db, clock.Real{})
	source := analysis.NewMarketdataSource(mdStore)
	analysisStore := analysis.NewStore(db)
	e, err := analysis.NewEngine(db, source, analysisStore, clock.Real{}, analysis.Config{
		AlgorithmVersion: testVersion, LeftBars: 2, RightBars: 2, Interval: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func newTestCandleSource(db *sql.DB) analysis.CandleSource {
	return analysis.NewMarketdataSource(marketdata.NewStore(db, clock.Real{}))
}

// oneCandle inserts a single closed candle and returns its id -- the
// minimum fixture a hand-built analysis.POI needs to satisfy
// setups.poi_source_candle_id's real foreign key, for fixtures that
// test Reconcile's own identity/amendment/terminal logic directly
// rather than the full organic engine pipeline (see this file's
// TestIntegration_Reconcile_* tests for which each kind uses).
func oneCandle(t *testing.T, db *sql.DB, instrumentID int64, minute int, price int64) int64 {
	t.Helper()
	ids := feedCloseCandles(t, db, instrumentID, []closeBar{{minute: minute, high: price, low: price, cls: price}})
	return ids[minute]
}
