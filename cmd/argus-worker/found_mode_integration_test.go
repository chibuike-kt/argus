//go:build integration

package main

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/setups"
)

// Phase 6.1 (owner decision 3): setups.found_mode is "live" when the
// candle that created the setup was the newest closed candle when the
// worker processed it, "replay" when the worker was catching up. The
// same 50 real candles are processed two ways; the decisions must be the
// same, only the found mode differs. Every new setup records evidence.
func TestIntegration_FoundMode_ReplayWhenCatchingUpLiveWhenCurrent(t *testing.T) {
	db := testWorkerDB(t)
	logger := quietLogger()
	ctx := context.Background()
	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatal(err)
	}
	base := loaded.Candles["BTC-PERP"][:50]

	// Catch-up: all 50 stored, then one processing run.
	replayID, _ := seedAndIngest(t, db, logger, base, "replay")
	replayProc, replayStore := newWorkerProcessor(db, logger)
	if _, err := replayProc.processInstrument(ctx, mustGetInstrument(t, db, replayID)); err != nil {
		t.Fatal(err)
	}

	// Live: the first 10 stored and processed, then one candle at a
	// time, each processed as it arrives.
	liveID, liveSymbol := seedAndIngest(t, db, logger, base[:10], "live")
	liveProc, liveStore := newWorkerProcessor(db, logger)
	if _, err := liveProc.processInstrument(ctx, mustGetInstrument(t, db, liveID)); err != nil {
		t.Fatal(err)
	}
	firstBatchClose := base[9].OpenTime.Add(time.Minute) // close of candle 10
	for k := 11; k <= len(base); k++ {
		ingestMore(t, db, logger, liveSymbol, base[:k])
		if _, err := liveProc.processInstrument(ctx, mustGetInstrument(t, db, liveID)); err != nil {
			t.Fatal(err)
		}
	}

	// The same decisions either way.
	rf, lf := setupFingerprints(t, replayStore, replayID), setupFingerprints(t, liveStore, liveID)
	if len(rf) == 0 {
		t.Fatal("the 50-candle slice created no setup; the test needs at least one")
	}
	if len(rf) != len(lf) {
		t.Fatalf("replay run %v, live run %v: want the same setups", rf, lf)
	}
	for i := range rf {
		if rf[i] != lf[i] {
			t.Fatalf("replay run %v, live run %v: want the same setups", rf, lf)
		}
	}

	lastClose := base[len(base)-1].OpenTime.Add(time.Minute)
	check := func(store *setups.Store, id int64, want func(detectedAt string, atLast, inFirstBatch bool) setups.FoundMode) {
		all, err := store.ListByInstrument(ctx, id, "1m")
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, len(all))
		for i, s := range all {
			ids[i] = s.ID
		}
		lc, err := store.LifecycleByID(ctx, ids)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range all {
			l := lc[s.ID]
			if l.DetectedAt == nil {
				t.Fatalf("setup %d has no detected time", s.ID)
			}
			w := want(l.DetectedAt.String(), l.DetectedAt.Equal(lastClose), !l.DetectedAt.After(firstBatchClose))
			if s.FoundMode != w {
				t.Fatalf("setup %d detected %s: found mode %q, want %q", s.ID, l.DetectedAt, s.FoundMode, w)
			}
			if !l.EvidenceRecorded {
				t.Fatalf("setup %d: no evidence recorded", s.ID)
			}
		}
	}
	check(replayStore, replayID, func(_ string, atLast, _ bool) setups.FoundMode {
		if atLast {
			return setups.FoundLive
		}
		return setups.FoundReplay
	})
	check(liveStore, liveID, func(_ string, _, inFirstBatch bool) setups.FoundMode {
		if inFirstBatch {
			return setups.FoundReplay
		}
		return setups.FoundLive
	})
}

// ingestMore ingests candles (a longer prefix of the same series) under an
// already-seeded symbol: the fixture ingest upserts, so the earlier
// candles are unchanged and only the new ones are added.
func ingestMore(t *testing.T, db *sql.DB, logger *slog.Logger, symbol string, base []ingestion.ProviderCandle) {
	t.Helper()
	candles := make([]ingestion.ProviderCandle, len(base))
	for i, c := range base {
		c.Symbol = symbol
		candles[i] = c
	}
	data := fixture.Data{Candles: map[string][]ingestion.ProviderCandle{symbol: candles}}
	if err := runDevFixtureIngest(context.Background(), logger, data, instruments.NewStore(db), marketdata.NewStore(db, clock.Real{}), nil); err != nil {
		t.Fatalf("ingest more (%d candles): %v", len(base), err)
	}
}
