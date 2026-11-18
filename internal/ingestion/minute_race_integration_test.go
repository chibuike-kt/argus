//go:build integration

package ingestion_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// The minute-boundary race seen on the dev stack (2026-11-17T00:34:59Z,
// periodic gap backfill): "candle 45434 is closed: OHLCV and is_closed are
// frozen; insert a new revision to correct it". A REST candle for a minute
// that was still forming when fetched (the newest candle of a backfill,
// IsFinal=false) was applied after the live stream had already closed that
// minute.
//
// Correct behaviour: a FORMING value never overwrites a stored CLOSED
// candle -- it is older information than the close. A FINAL value for an
// already-closed minute still goes through the existing equality /
// correction path.

func candleAt(symbol string, t time.Time, closePx string, final bool) ingestion.ProviderCandle {
	return ingestion.ProviderCandle{Symbol: symbol, OpenTime: t, Open: "100.00", High: "200.00", Low: "50.00", Close: closePx, Volume: "1.0", IsFinal: final}
}

func svcWith(t *testing.T, db *sql.DB, symbol string, cs ...ingestion.ProviderCandle) *ingestion.Service {
	t.Helper()
	return testIngestionService(db, fixture.New(fixture.Data{Candles: map[string][]ingestion.ProviderCandle{symbol: cs}}, 0), marketdata.ContinuousCalendar{})
}

// (A) At the store level: a forming upsert onto a closed row is refused
// with a sentinel the caller can act on, not the trigger's raw error.
func TestIntegration_UpsertFormingOnClosedRow_IsRefusedWithSentinel(t *testing.T) {
	db := testDB(t)
	id, _ := testIngestionInstrument(t, db)
	store := marketdata.NewStore(db, clock.Real{})
	ctx := context.Background()
	at := clock.NewEventTime(et(2026, time.November, 17, 0, 34))
	tick := marketdata.Tick{InstrumentID: id, OpenTime: at, Open: 10000, High: 20000, Low: 5000, Close: 15000, Volume: 1, Provider: "fixture", IngestedAt: clock.NewIngestionTime(time.Now())}
	if _, err := store.UpsertForming(ctx, tick); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(ctx, id, at); err != nil {
		t.Fatal(err)
	}
	tick.Close = 16000
	_, err := store.UpsertForming(ctx, tick)
	if !errors.Is(err, marketdata.ErrCandleAlreadyClosed) {
		t.Fatalf("err = %v, want ErrCandleAlreadyClosed", err)
	}
	c, _ := store.GetCurrent(ctx, id, at)
	if c.Close != 15000 || c.Revision != 1 || !c.IsClosed {
		t.Fatalf("closed candle changed: %+v", c)
	}
}

// (B) A forming REST value arriving AFTER the stream closed the minute is
// dropped: no error, no new revision, the closed values stand.
func TestIntegration_FormingRESTValue_NeverOverwritesAClosedCandle(t *testing.T) {
	db := testDB(t)
	id, symbol := testIngestionInstrument(t, db)
	ctx := context.Background()
	minute := et(2026, time.November, 17, 0, 34)

	// The stream closed 00:34 at 150.00.
	closed := svcWith(t, db, symbol, candleAt(symbol, minute, "150.00", true))
	if err := closed.IngestHistorical(ctx, symbol, minute, minute.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Then the periodic backfill applies its newest candle -- 00:34 as it was
	// while still forming (149.00, IsFinal=false).
	late := svcWith(t, db, symbol, candleAt(symbol, minute, "149.00", false))
	if err := late.IngestHistorical(ctx, symbol, minute, minute.Add(time.Minute)); err != nil {
		t.Fatalf("applying a stale forming value: %v", err)
	}
	c, err := marketdata.NewStore(db, clock.Real{}).GetCurrent(ctx, id, clock.NewEventTime(minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.Close != 15000 || c.Revision != 1 || !c.IsClosed {
		t.Fatalf("closed candle overwritten by a forming value: close=%d revision=%d closed=%v", c.Close, c.Revision, c.IsClosed)
	}
}

// (B2) A FINAL value that differs still corrects the closed candle (the
// existing correction path is unchanged).
func TestIntegration_FinalRESTValue_StillCorrectsAClosedCandle(t *testing.T) {
	db := testDB(t)
	id, symbol := testIngestionInstrument(t, db)
	ctx := context.Background()
	minute := et(2026, time.November, 17, 0, 36)
	if err := svcWith(t, db, symbol, candleAt(symbol, minute, "150.00", true)).IngestHistorical(ctx, symbol, minute, minute.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svcWith(t, db, symbol, candleAt(symbol, minute, "151.00", true)).IngestHistorical(ctx, symbol, minute, minute.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	c, _ := marketdata.NewStore(db, clock.Real{}).GetCurrent(ctx, id, clock.NewEventTime(minute))
	if c.Close != 15100 || c.Revision != 2 {
		t.Fatalf("final correction not applied: close=%d revision=%d", c.Close, c.Revision)
	}
}

// (C) The race itself: for each of many minutes, the stream (forming then
// final) and a REST backfill (forming) apply concurrently. Whatever the
// interleaving, nothing errors and every minute ends closed at the
// stream's value, revision 1.
func TestIntegration_StreamCloseVersusFormingBackfill_Concurrent(t *testing.T) {
	db := testDB(t)
	id, symbol := testIngestionInstrument(t, db)
	ctx := context.Background()
	base := et(2026, time.November, 17, 1, 0)
	const minutes = 150

	var mu sync.Mutex
	var errs []string
	var wg sync.WaitGroup
	// At most 16 minutes in flight (32 goroutines): the race is per minute
	// (stream vs REST on the SAME minute, both started together); the cap
	// only keeps the test under Postgres's own connection limit.
	slots := make(chan struct{}, 16)
	for i := 0; i < minutes; i++ {
		slots <- struct{}{}
		m := base.Add(time.Duration(i) * time.Minute)
		stream := svcWith(t, db, symbol, candleAt(symbol, m, "150.00", false), candleAt(symbol, m, "150.00", true))
		rest := svcWith(t, db, symbol, candleAt(symbol, m, "149.00", false))
		var pair sync.WaitGroup
		pair.Add(2)
		wg.Add(2)
		go func() {
			defer pair.Done()
			defer wg.Done()
			if err := stream.IngestHistorical(ctx, symbol, m, m.Add(time.Minute)); err != nil {
				mu.Lock()
				errs = append(errs, "stream: "+err.Error())
				mu.Unlock()
			}
		}()
		go func() {
			defer pair.Done()
			defer wg.Done()
			if err := rest.IngestHistorical(ctx, symbol, m, m.Add(time.Minute)); err != nil {
				mu.Lock()
				errs = append(errs, "rest: "+err.Error())
				mu.Unlock()
			}
		}()
		go func() {
			pair.Wait()
			<-slots
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		frozen := 0
		for _, e := range errs {
			if strings.Contains(e, "OHLCV and is_closed are frozen") {
				frozen++
			}
		}
		kinds := map[string]int{}
		for _, e := range errs {
			i := strings.LastIndex(e, ": ")
			kinds[e[:min(len(e), 6)]+" .. "+e[i+2:]]++
		}
		t.Fatalf("%d of %d applies failed (%d with the production error): %v", len(errs), 2*minutes, frozen, kinds)
	}
	var wrong int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM candles WHERE instrument_id = $1 AND superseded_at IS NULL
		  AND open_time >= $2 AND open_time < $3 AND (NOT is_closed OR close <> 15000 OR revision <> 1)`,
		id, base, base.Add(minutes*time.Minute)).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong > 0 {
		t.Fatalf("%d minutes not closed at the stream's value at revision 1", wrong)
	}
}
