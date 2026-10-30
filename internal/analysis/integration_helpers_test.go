//go:build integration

package analysis

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// These tests exercise the real schema from db/migrations -- the two
// supersede-handshake triggers (confirmed_swings, structure_events) and
// the full Engine against real Postgres -- because a handwritten fake
// cannot verify a database trigger or a partial unique index (guide 10).
// Run with: go test -tags=integration -race ./internal/analysis/...
// against a Postgres with all migrations applied (make migrate-up).

const testAlgorithmVersion = "swing-structure-v1-test"

func testConfig() Config {
	return Config{AlgorithmVersion: testAlgorithmVersion, LeftBars: 2, RightBars: 2, Interval: time.Minute}
}

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
	priceScale := quant.Scale(0) // integer prices, to keep fixture arithmetic readable
	qtyScale := quant.Scale(0)
	symbol := fmt.Sprintf("ANL-%s-%d", t.Name(), time.Now().UnixNano())

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

func newTestEngine(t *testing.T, db *sql.DB) *Engine {
	t.Helper()
	mdStore := marketdata.NewStore(db, clock.Real{})
	source := NewMarketdataSource(mdStore)
	analysisStore := NewStore(db)
	e, err := NewEngine(db, source, analysisStore, clock.Real{}, testConfig())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func et(minute int) time.Time {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(minute) * time.Minute)
}

func clockEventTime(minute int) clock.EventTime { return clock.NewEventTime(et(minute)) }

// bar is one candle's (high, low) for feedCandles; open and close are
// derived as the midpoint, which is irrelevant to every fixture that
// uses feedCandles for pivot/establishment setup (only relevant to the
// break fixtures, which set Close explicitly via feedCandlesWithClose).
type bar struct {
	minute    int
	high, low int64
}

// feedCandles drives the real candle store (UpsertForming then Close,
// exactly as the ingestion engine would) for each bar, in order, using
// open=close=midpoint(high,low). Returns the inserted candle ids by
// minute offset.
func feedCandles(t *testing.T, db *sql.DB, instrumentID int64, bars []bar) map[int]int64 {
	t.Helper()
	ids := make(map[int]int64, len(bars))
	store := marketdata.NewStore(db, clock.Real{})
	for _, b := range bars {
		mid := (b.high + b.low) / 2
		openTime := clock.NewEventTime(et(b.minute))
		tick := marketdata.Tick{
			InstrumentID: instrumentID, OpenTime: openTime,
			Open: quant.Price(mid), High: quant.Price(b.high), Low: quant.Price(b.low), Close: quant.Price(mid),
			Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
		}
		c, err := store.UpsertForming(context.Background(), tick)
		if err != nil {
			t.Fatalf("UpsertForming minute=%d: %v", b.minute, err)
		}
		closed, err := store.Close(context.Background(), instrumentID, openTime)
		if err != nil {
			t.Fatalf("Close minute=%d: %v", b.minute, err)
		}
		ids[b.minute] = closed.ID
		_ = c
	}
	return ids
}

// closeBar is feedCandles' counterpart for the break fixtures, where the
// candle's close value (not just its high/low) determines the outcome.
type closeBar struct {
	minute         int
	high, low, cls int64
}

func feedCloseCandles(t *testing.T, db *sql.DB, instrumentID int64, bars []closeBar) map[int]int64 {
	t.Helper()
	ids := make(map[int]int64, len(bars))
	store := marketdata.NewStore(db, clock.Real{})
	for _, b := range bars {
		openTime := clock.NewEventTime(et(b.minute))
		tick := marketdata.Tick{
			InstrumentID: instrumentID, OpenTime: openTime,
			Open: quant.Price(b.cls), High: quant.Price(b.high), Low: quant.Price(b.low), Close: quant.Price(b.cls),
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

// ohlcBar is feedCloseCandles' counterpart for order-block fixtures,
// where the candle's colour (open versus close, not just high/low/close)
// determines the outcome -- feedCandles and feedCloseCandles both set
// open == close by construction, which makes every candle they produce
// a doji, unusable for testing section 12's colour test.
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

// seedSwing inserts a confirmed swing directly via the Store API,
// bypassing fractal detection -- used by fixtures that only care about
// the break/establishment decision given an already-confirmed swing,
// not about how that swing came to be confirmed (the cold-start
// fixtures exercise that path organically).
func seedSwing(t *testing.T, store *Store, instrumentID int64, swingType SwingType, minute int, price int64, sourceIDs []int64) Swing {
	t.Helper()
	now := time.Now().UTC()
	s, err := store.InsertSwing(context.Background(), NewSwing{
		InstrumentID: instrumentID, Timeframe: "1m", Type: swingType,
		PivotTime: clock.NewEventTime(et(minute)), Price: quant.Price(price),
		ConfirmedAt: clock.NewEventTime(et(minute + 2)), EvaluationTime: clock.NewEvaluationTime(now),
		AlgorithmVersion: testAlgorithmVersion, SourceCandleIDs: sourceIDs,
	})
	if err != nil {
		t.Fatalf("seedSwing: %v", err)
	}
	return s
}
