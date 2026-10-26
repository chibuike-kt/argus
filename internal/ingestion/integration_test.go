//go:build integration

// Package ingestion_test is deliberately an external (black-box) test
// package, not "package ingestion": it needs to import both ingestion
// and ingestion/fixture, and fixture itself imports ingestion to use its
// boundary types -- importing fixture from an internal ingestion test
// file would be an import cycle. The other _test.go files in this
// directory stay "package ingestion" (white-box) because they exercise
// unexported internals that have no reason to be exported just for
// tests; Go links both kinds of test file into one test binary per
// directory.
package ingestion_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// These tests exercise the real ingestion-to-store path: a fixture
// Provider -> Normalizer -> Service -> a real marketdata.Store backed by
// Postgres. Run with:
//
//	go test -tags=integration -race ./internal/ingestion/...
//
// against a Postgres with all migrations applied (make migrate-up).

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

// testIngestionInstrument registers a fresh instrument with a unique
// symbol per call, so concurrent and repeated test runs do not collide
// on the (provider, native_symbol) uniqueness constraint.
func testIngestionInstrument(t *testing.T, db *sql.DB) (id int64, symbol string) {
	t.Helper()
	priceScale := quant.Scale(2)
	qtyScale := quant.Scale(6)
	symbol = fmt.Sprintf("ING-%s-%d", t.Name(), time.Now().UnixNano())

	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider:     "fixture",
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
	return inst.ID, symbol
}

func et(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func testIngestionService(db *sql.DB, provider ingestion.Provider, cal marketdata.Calendar) *ingestion.Service {
	cache := ingestion.NewInstrumentCache(instruments.NewStore(db))
	normalizer := ingestion.NewNormalizer(cache, clock.Real{})
	store := marketdata.NewStore(db, clock.Real{})
	return ingestion.NewService("fixture", provider, normalizer, cache, store, cal, nil)
}

func TestIntegration_IngestHistorical_AppliesCandlesAndRecordsGap(t *testing.T) {
	db := testDB(t)
	instrumentID, symbol := testIngestionInstrument(t, db)

	start := et(2026, time.October, 27, 12, 0)
	data := fixture.Data{
		Candles: map[string][]ingestion.ProviderCandle{
			symbol: {
				{Symbol: symbol, OpenTime: start, Open: "100.00", High: "105.00", Low: "95.00", Close: "102.00", Volume: "1.5", IsFinal: true},
				{Symbol: symbol, OpenTime: start.Add(time.Minute), Open: "102.00", High: "108.00", Low: "100.00", Close: "106.00", Volume: "2.0", IsFinal: true},
				// start+2m deliberately absent: a gap.
				{Symbol: symbol, OpenTime: start.Add(3 * time.Minute), Open: "106.00", High: "107.00", Low: "104.00", Close: "105.00", Volume: "1.0", IsFinal: true},
			},
		},
	}
	provider := fixture.New(data, 0)
	svc := testIngestionService(db, provider, marketdata.ContinuousCalendar{})

	end := start.Add(4 * time.Minute)
	if err := svc.IngestHistorical(context.Background(), symbol, start, end); err != nil {
		t.Fatalf("IngestHistorical: %v", err)
	}

	store := marketdata.NewStore(db, clock.Real{})
	c0, err := store.GetCurrent(context.Background(), instrumentID, clock.NewEventTime(start))
	if err != nil {
		t.Fatalf("GetCurrent minute 0: %v", err)
	}
	if !c0.IsClosed || c0.Close != 10200 { // "102.00" at scale 2
		t.Fatalf("minute 0 = %+v, want closed with Close=10200", c0)
	}

	gaps, err := store.ListOpenGaps(context.Background(), instrumentID)
	if err != nil {
		t.Fatalf("ListOpenGaps: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("got %d open gaps, want exactly 1 for the missing minute: %+v", len(gaps), gaps)
	}
	wantGapStart := start.Add(2 * time.Minute)
	wantGapEnd := start.Add(3 * time.Minute)
	if !gaps[0].Start.Time().Equal(wantGapStart) || !gaps[0].End.Time().Equal(wantGapEnd) {
		t.Fatalf("gap = [%s,%s), want [%s,%s)", gaps[0].Start, gaps[0].End, wantGapStart, wantGapEnd)
	}
}

func TestIntegration_IngestHistorical_ScheduledClosureIsNotRecordedAsGap(t *testing.T) {
	db := testDB(t)
	instrumentID, symbol := testIngestionInstrument(t, db)

	start := et(2026, time.October, 27, 12, 0)
	end := start.Add(2 * time.Minute)
	// No candles at all for this range, and the calendar reports every
	// instant in it as a scheduled closure.
	provider := fixture.New(fixture.Data{}, 0)
	svc := testIngestionService(db, provider, alwaysClosedCalendar{})

	if err := svc.IngestHistorical(context.Background(), symbol, start, end); err != nil {
		t.Fatalf("IngestHistorical: %v", err)
	}

	store := marketdata.NewStore(db, clock.Real{})
	gaps, err := store.ListOpenGaps(context.Background(), instrumentID)
	if err != nil {
		t.Fatalf("ListOpenGaps: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("got %d gaps, want 0: a scheduled closure must never be recorded as a gap (ADR-006): %+v", len(gaps), gaps)
	}
}

type alwaysClosedCalendar struct{}

func (alwaysClosedCalendar) Status(_ context.Context, _ string, _ clock.EventTime) (marketdata.MarketStatus, error) {
	return marketdata.MarketStatusScheduledClosure, nil
}

func TestIntegration_WatchCandles_LiveFormingThenCloseThenCorrection(t *testing.T) {
	db := testDB(t)
	instrumentID, symbol := testIngestionInstrument(t, db)
	store := marketdata.NewStore(db, clock.Real{})

	openTime := et(2026, time.October, 27, 13, 0)
	data := fixture.Data{
		Candles: map[string][]ingestion.ProviderCandle{
			symbol: {
				// Two forming ticks for the same minute, then a final
				// close, then -- for the same minute, after it is
				// already closed in the store -- a correction with
				// different values.
				{Symbol: symbol, OpenTime: openTime, Open: "100.00", High: "101.00", Low: "99.00", Close: "100.50", Volume: "1.0", IsFinal: false},
				{Symbol: symbol, OpenTime: openTime, Open: "100.00", High: "103.00", Low: "99.00", Close: "102.00", Volume: "2.0", IsFinal: false},
				{Symbol: symbol, OpenTime: openTime, Open: "100.00", High: "105.00", Low: "99.00", Close: "104.00", Volume: "3.0", IsFinal: true},
				{Symbol: symbol, OpenTime: openTime, Open: "100.00", High: "110.00", Low: "98.00", Close: "108.00", Volume: "3.5", IsFinal: true},
			},
		},
	}
	provider := fixture.New(data, 0)
	svc := testIngestionService(db, provider, marketdata.ContinuousCalendar{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.WatchCandles(ctx, symbol); err != nil {
		t.Fatalf("WatchCandles: %v", err)
	}

	got, err := store.GetCurrent(context.Background(), instrumentID, clock.NewEventTime(openTime))
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if !got.IsClosed {
		t.Fatal("final candle is not closed")
	}
	if got.Revision != 2 {
		t.Fatalf("Revision = %d, want 2: the fourth message had different final values than the third, so it must be a correction, not a duplicate", got.Revision)
	}
	if got.Close != 10800 { // "108.00" at scale 2
		t.Fatalf("Close = %d, want 10800 (the corrected value)", got.Close)
	}
	if got.High != 11000 { // "110.00"
		t.Fatalf("High = %d, want 11000 (the corrected value)", got.High)
	}
}

func TestIntegration_WatchQuotes_LiveQuoteReachesStore(t *testing.T) {
	db := testDB(t)
	instrumentID, symbol := testIngestionInstrument(t, db)
	store := marketdata.NewStore(db, clock.Real{})

	data := fixture.Data{
		Quotes: map[string][]ingestion.ProviderQuote{
			symbol: {
				{Symbol: symbol, Bid: "100.00", Ask: "100.50", QuoteTime: et(2026, time.October, 27, 14, 0)},
			},
		},
	}
	provider := fixture.New(data, 0)
	svc := testIngestionService(db, provider, marketdata.ContinuousCalendar{})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := svc.WatchQuotes(ctx, symbol); err != nil {
		t.Fatalf("WatchQuotes: %v", err)
	}

	got, err := store.GetQuote(context.Background(), instrumentID)
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if got.Bid != 10000 || got.Ask != 10050 {
		t.Fatalf("got Bid=%d Ask=%d, want Bid=10000 Ask=10050", got.Bid, got.Ask)
	}
}

func TestIntegration_RejectedCandleIsDroppedNotStored(t *testing.T) {
	db := testDB(t)
	instrumentID, symbol := testIngestionInstrument(t, db)
	store := marketdata.NewStore(db, clock.Real{})

	openTime := et(2026, time.October, 27, 15, 0)
	data := fixture.Data{
		Candles: map[string][]ingestion.ProviderCandle{
			symbol: {
				// price_scale is 2: three fractional digits is a scale
				// mismatch and must be rejected, not rounded.
				{Symbol: symbol, OpenTime: openTime, Open: "100.005", High: "105.00", Low: "95.00", Close: "102.00", Volume: "1.0", IsFinal: true},
			},
		},
	}
	provider := fixture.New(data, 0)
	svc := testIngestionService(db, provider, marketdata.ContinuousCalendar{})

	start := openTime
	end := openTime.Add(time.Minute)
	if err := svc.IngestHistorical(context.Background(), symbol, start, end); err != nil {
		t.Fatalf("IngestHistorical: %v", err)
	}

	if _, err := store.GetCurrent(context.Background(), instrumentID, clock.NewEventTime(openTime)); err == nil {
		t.Fatal("a rejected candle was written to the store, want it dropped")
	}

	// The minute is still correctly recorded as a gap: a rejected candle
	// is not "present" for gap-detection purposes.
	gaps, err := store.ListOpenGaps(context.Background(), instrumentID)
	if err != nil {
		t.Fatalf("ListOpenGaps: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("got %d gaps, want 1: a rejected candle's minute must still be reported missing", len(gaps))
	}
}
