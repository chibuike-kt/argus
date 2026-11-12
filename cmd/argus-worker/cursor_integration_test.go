//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/cursor"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/setups"
)

// seedAndIngest seeds a fresh, uniquely-named fixture instrument and
// ingests baseCandles into it under that instrument's own symbol.
// baseCandles itself is never mutated (each candle is copied before
// its Symbol field is overwritten), so callers can pass the same
// backing slice to seedAndIngest multiple times under different
// labels without it corrupting a previous call's data.
func seedAndIngest(t *testing.T, db *sql.DB, logger *slog.Logger, baseCandles []ingestion.ProviderCandle, label string) (instrumentID int64, symbol string) {
	t.Helper()
	symbol = fmt.Sprintf("BTC-PERP-%s-%s-%d", label, t.Name(), time.Now().UnixNano())
	candles := make([]ingestion.ProviderCandle, len(baseCandles))
	for i, c := range baseCandles {
		c.Symbol = symbol
		candles[i] = c
	}
	instrumentStore := instruments.NewStore(db)
	instrumentID = seedDevFixtureInstrument(t, db, instrumentStore, symbol)
	data := fixture.Data{Candles: map[string][]ingestion.ProviderCandle{symbol: candles}}
	if err := runDevFixtureIngest(context.Background(), logger, data, instrumentStore, marketdata.NewStore(db, clock.Real{}), nil); err != nil {
		t.Fatalf("runDevFixtureIngest (%s): %v", label, err)
	}
	return instrumentID, symbol
}

// newWorkerProcessor builds a processor wired exactly like main.go's
// own (minus the dev-fixture-ingest step -- callers ingest separately
// via seedAndIngest), so the cursor and idempotency tests are not
// each re-deriving this wiring by hand.
func newWorkerProcessor(db *sql.DB, logger *slog.Logger) (*processor, *setups.Store) {
	clk := clock.Real{}
	instrumentStore := instruments.NewStore(db)
	marketdataStore := marketdata.NewStore(db, clk)
	candleSource := analysis.NewMarketdataSource(marketdataStore)
	analysisStore := analysis.NewStore(db)
	setupsStore := setups.NewStore(db)
	cursorStore := cursor.NewStore(db)
	engine, err := analysis.NewEngine(db, candleSource, analysisStore, clk, analysis.Config{
		AlgorithmVersion: algorithmVersion, LeftBars: 2, RightBars: 2, Interval: time.Minute,
	})
	if err != nil {
		panic(err) // startup wiring only; a test helper, not a code path under test
	}
	proc := newProcessor(db, logger, clk, instrumentStore, candleSource, engine, analysisStore, setupsStore, cursorStore)
	return proc, setupsStore
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setupFingerprints returns instrumentID's setups, in creation order,
// each reduced to a small fingerprint (poi_type, direction, state) --
// deliberately excluding id, instrument_id, poi_source_candle_id,
// created_at and updated_at, every one of which legitimately differs
// between two independently-seeded instruments processing identical
// candle data under different symbols/ids. Used to compare an
// uninterrupted reference run against a crashed-then-recovered run on
// a DIFFERENT instrument (TestIntegration_Cursor_...), where only the
// shape of the outcome, not its literal row identities, is meant to
// match.
func setupFingerprints(t *testing.T, store *setups.Store, instrumentID int64) []string {
	t.Helper()
	all, err := store.ListByInstrument(context.Background(), instrumentID, "1m")
	if err != nil {
		t.Fatalf("ListByInstrument: %v", err)
	}
	// ListByInstrument orders by updated_at DESC; fingerprint by
	// creation order (ascending id) instead, so two independently-run
	// instruments that reached their states via the same sequence of
	// steps compare equal regardless of exactly when each one's last
	// update happened to land.
	sortByIDAsc(all)
	out := make([]string, len(all))
	for i, s := range all {
		out[i] = fmt.Sprintf("%s|%s|%s", s.POIType, s.Direction, s.State)
	}
	return out
}

func sortByIDAsc(all []setups.Setup) {
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j-1].ID > all[j].ID; j-- {
			all[j-1], all[j] = all[j], all[j-1]
		}
	}
}

// TestIntegration_Cursor_CrashBetweenEngineAndReconcile_Recovers is
// Part B's own required proof: a crash between analysis.Engine's
// commit and setups.Reconcile's commit for one candle must not skip
// that candle, and a restart must reprocess it and reach the same
// final setups state an uninterrupted run reaches.
func TestIntegration_Cursor_CrashBetweenEngineAndReconcile_Recovers(t *testing.T) {
	db := testWorkerDB(t)
	logger := quietLogger()
	ctx := context.Background()

	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatalf("DevDataset: %v", err)
	}
	// The first 50 candles of the real dataset already reliably walk
	// a setup from Detected to Actionable (proven by
	// TestIntegration_Pipeline_... and, before that, the dev-pipeline
	// brief's own verification) -- a small, fast slice, not the
	// full ~1500-bar series, since this test is about crash recovery,
	// not about re-proving the dataset's own structure again.
	base := loaded.Candles["BTC-PERP"][:50]

	// Reference: uninterrupted.
	refInstID, _ := seedAndIngest(t, db, logger, base, "ref")
	refProc, refStore := newWorkerProcessor(db, logger)
	refInst := mustGetInstrument(t, db, refInstID)
	if _, err := refProc.processInstrument(ctx, refInst); err != nil {
		t.Fatalf("reference run: processInstrument: %v", err)
	}
	refFingerprint := setupFingerprints(t, refStore, refInstID)
	if len(refFingerprint) == 0 {
		t.Fatal("reference run produced no setups -- nothing to compare against")
	}

	// Crash: same candle data, a different instrument, fail right
	// after the engine commits for the second-to-last candle, before
	// that candle's own reconciliation and cursor advance.
	crashInstID, _ := seedAndIngest(t, db, logger, base, "crash")
	crashProc, crashStore := newWorkerProcessor(db, logger)
	failAt := base[len(base)-2].OpenTime.Add(time.Minute)
	crashProc.testFailBeforeReconcile = func(closeTime time.Time) bool { return closeTime.Equal(failAt) }
	crashInst := mustGetInstrument(t, db, crashInstID)
	if _, err := crashProc.processInstrument(ctx, crashInst); err == nil {
		t.Fatal("crash run: processInstrument returned nil, want the simulated-crash error")
	}

	// The cursor must sit at the PREVIOUS candle's close_time, not at
	// (or past) the one the simulated crash hit.
	cursorStore := cursor.NewStore(db)
	gotCursor, err := cursorStore.Get(ctx, crashInstID, "1m")
	if err != nil {
		t.Fatalf("cursor.Get after crash: %v", err)
	}
	wantCursor := base[len(base)-3].OpenTime.Add(time.Minute)
	if !gotCursor.Equal(wantCursor) {
		t.Fatalf("cursor after crash = %v, want %v (the candle before the simulated failure)", gotCursor, wantCursor)
	}

	// Recovery: a fresh processor (no hook), same instrument. Must
	// reprocess the failed candle (and the one after it) and reach
	// the same final state the reference run reached.
	recoverProc, _ := newWorkerProcessor(db, logger)
	if _, err := recoverProc.processInstrument(ctx, crashInst); err != nil {
		t.Fatalf("recovery run: processInstrument: %v", err)
	}

	recoveredCursor, err := cursorStore.Get(ctx, crashInstID, "1m")
	if err != nil {
		t.Fatalf("cursor.Get after recovery: %v", err)
	}
	wantFinalCursor := base[len(base)-1].OpenTime.Add(time.Minute)
	if !recoveredCursor.Equal(wantFinalCursor) {
		t.Fatalf("cursor after recovery = %v, want %v (fully caught up)", recoveredCursor, wantFinalCursor)
	}

	recoveredFingerprint := setupFingerprints(t, crashStore, crashInstID)
	if len(recoveredFingerprint) != len(refFingerprint) {
		t.Fatalf("recovered setups = %v\nreference setups = %v\n(different lengths)", recoveredFingerprint, refFingerprint)
	}
	for i := range refFingerprint {
		if recoveredFingerprint[i] != refFingerprint[i] {
			t.Fatalf("recovered setup[%d] = %q, reference = %q\nfull recovered: %v\nfull reference: %v",
				i, recoveredFingerprint[i], refFingerprint[i], recoveredFingerprint, refFingerprint)
		}
	}

	// A THIRD, freshly-constructed processor -- simulating an actual
	// worker restart, not just a second call on the same instance --
	// pointed at this now-fully-caught-up instrument must process
	// zero already-handled candles: nothing new since the persisted
	// cursor's own position, so processInstrument returns immediately
	// without touching the engine, setups, or the cursor again.
	beforeCursor := recoveredCursor
	restartedProc, _ := newWorkerProcessor(db, logger)
	if _, err := restartedProc.processInstrument(ctx, crashInst); err != nil {
		t.Fatalf("restarted-worker no-op run: processInstrument: %v", err)
	}
	afterCursor, err := cursorStore.Get(ctx, crashInstID, "1m")
	if err != nil {
		t.Fatalf("cursor.Get after no-op run: %v", err)
	}
	if !afterCursor.Equal(beforeCursor) {
		t.Fatalf("cursor moved on a no-op run: before=%v after=%v (should not have touched any candle)", beforeCursor, afterCursor)
	}
}

func mustGetInstrument(t *testing.T, db *sql.DB, id int64) instruments.Instrument {
	t.Helper()
	inst, err := instruments.NewStore(db).GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID(%d): %v", id, err)
	}
	return inst
}
