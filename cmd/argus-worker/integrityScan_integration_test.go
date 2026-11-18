//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// holeProvider is the exchange for the integrity-scan tests: it has every
// minute (close "100", volume "1", the same values insertClosedCandles
// stores, so re-reading an existing minute changes nothing), records every
// window it is asked for, and can fail the first failDeep requests that
// reach further back than deepBefore (the hole repairs), not the ordinary
// tail backfill.
type holeProvider struct {
	mu         sync.Mutex
	windows    [][2]time.Time
	deepBefore time.Time
	failDeep   int
}

func (p *holeProvider) Instruments(context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}

func (p *holeProvider) HistoricalCandles(_ context.Context, symbol string, start, end time.Time) ([]ingestion.ProviderCandle, error) {
	p.mu.Lock()
	p.windows = append(p.windows, [2]time.Time{start, end})
	if p.failDeep > 0 && start.Before(p.deepBefore) {
		p.failDeep--
		p.mu.Unlock()
		return nil, fmt.Errorf("HTTP 502 (scripted)")
	}
	p.mu.Unlock()
	var out []ingestion.ProviderCandle
	for t := start.Truncate(time.Minute); t.Before(end); t = t.Add(time.Minute) {
		if t.Before(start) {
			continue
		}
		out = append(out, ingestion.ProviderCandle{Symbol: symbol, OpenTime: t, Open: "100", High: "100", Low: "100", Close: "100", Volume: "1", IsFinal: true})
	}
	if len(out) > 0 {
		out[len(out)-1].IsFinal = false
	}
	return out, nil
}

func (p *holeProvider) StreamCandles(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderCandle) error) error {
	<-ctx.Done()
	return nil
}

func (p *holeProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}

func (p *holeProvider) deepWindows() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, w := range p.windows {
		if w[0].Before(p.deepBefore) {
			n++
		}
	}
	return n
}

func deleteMinutes(t *testing.T, db *sql.DB, instrumentID int64, from time.Time, minutes int) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`DELETE FROM candles WHERE instrument_id = $1 AND open_time >= $2 AND open_time < $3`,
		instrumentID, from, from.Add(time.Duration(minutes)*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

type seriesState struct {
	rows, revised int
	maxIngested   time.Time
}

func stateOf(t *testing.T, db *sql.DB, instrumentID int64) seriesState {
	t.Helper()
	var s seriesState
	if err := db.QueryRowContext(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE revision > 1), coalesce(max(ingested_at), 'epoch')
		FROM candles WHERE instrument_id = $1`, instrumentID).Scan(&s.rows, &s.revised, &s.maxIngested); err != nil {
		t.Fatal(err)
	}
	return s
}

// A 65-minute hole INSIDE the stored series (not at its newest end) is
// found and filled. The old backfill only reached back to the newest stored
// candle minus 5 minutes, so it never saw this hole.
func TestIntegration_IntegrityScan_Heals65MinuteHoleInsideStoredData(t *testing.T) {
	fake := &holeProvider{}
	h := newOutageHarness(t, fake)
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-time.Minute)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 360) // the last 6 hours
	holeStart := newest.Add(-3 * time.Hour)
	deleteMinutes(t, h.db, h.instrumentID, holeStart, 65)

	h.runUntil(t, 30*time.Second, func() bool {
		return len(missingMinutes(t, h.db, h.instrumentID, newest.Add(-359*time.Minute), newest)) == 0
	})
	if miss := missingMinutes(t, h.db, h.instrumentID, newest.Add(-359*time.Minute), newest); len(miss) > 0 {
		t.Fatalf("%d minutes still missing, first %s", len(miss), miss[0])
	}
	if s := stateOf(t, h.db, h.instrumentID); s.revised != 0 {
		t.Fatalf("%d candles were revised; healing a hole must not touch existing candles", s.revised)
	}
}

func TestIntegration_IntegrityScan_HealsSeveralFourMinuteHoles(t *testing.T) {
	fake := &holeProvider{}
	h := newOutageHarness(t, fake)
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-time.Minute)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 360)
	for _, back := range []time.Duration{50 * time.Minute, 2 * time.Hour, 3*time.Hour + 7*time.Minute, 5 * time.Hour} {
		deleteMinutes(t, h.db, h.instrumentID, newest.Add(-back), 4)
	}
	h.runUntil(t, 30*time.Second, func() bool {
		return len(missingMinutes(t, h.db, h.instrumentID, newest.Add(-359*time.Minute), newest)) == 0
	})
	if miss := missingMinutes(t, h.db, h.instrumentID, newest.Add(-359*time.Minute), newest); len(miss) > 0 {
		t.Fatalf("%d minutes still missing: %v", len(miss), miss)
	}
	if s := stateOf(t, h.db, h.instrumentID); s.revised != 0 {
		t.Fatalf("%d candles were revised", s.revised)
	}
}

// REST fails twice for the hole repair, then works: the hole is filled.
func TestIntegration_IntegrityScan_RESTFailureThenSuccess(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-time.Minute)
	fake := &holeProvider{deepBefore: newest.Add(-time.Hour), failDeep: 2}
	h := newOutageHarness(t, fake)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 360)
	holeStart := newest.Add(-4 * time.Hour)
	deleteMinutes(t, h.db, h.instrumentID, holeStart, 30)

	h.runUntil(t, 30*time.Second, func() bool {
		return len(missingMinutes(t, h.db, h.instrumentID, holeStart, holeStart.Add(30*time.Minute))) == 0
	})
	if miss := missingMinutes(t, h.db, h.instrumentID, holeStart, holeStart.Add(29*time.Minute)); len(miss) > 0 {
		t.Fatalf("%d minutes still missing after REST recovered", len(miss))
	}
	if n := fake.deepWindows(); n < 3 {
		t.Fatalf("hole fetched %d times, want two failures then a success (>= 3)", n)
	}
}

// Nothing missing: the scan fetches nothing and writes nothing -- no new
// rows, no revisions, no ingested_at change.
func TestIntegration_IntegrityScan_NothingMissingMeansZeroWrites(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-time.Minute)
	fake := &holeProvider{deepBefore: newest.Add(-10 * time.Minute)}
	h := newOutageHarness(t, fake)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 360)
	// The scan window (BackfillDays = 3) reaches before the stored 6 hours:
	// the minutes before the instrument's first candle are not holes.
	before := stateOf(t, h.db, h.instrumentID)

	integrityScan(context.Background(), "test", h.symbol, h.svc, h.state, h.cfg, h.logger, clock.Real{})

	if n := len(fake.windows); n != 0 {
		t.Fatalf("scan fetched %d windows with nothing missing: %v", n, fake.windows)
	}
	if after := stateOf(t, h.db, h.instrumentID); after != before {
		t.Fatalf("scan wrote with nothing missing: before %+v after %+v", before, after)
	}
}

// A hole older than Hyperliquid's retention cannot be fetched: it is
// recorded in candle_gaps and logged, the scan never asks the exchange for
// it, and a second scan neither records it again nor retries it.
func TestIntegration_IntegrityScan_HoleBeyondRetentionRecordedNotRetried(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-time.Minute)
	retentionEdge := now.Add(-hyperliquidRetentionCandles * time.Minute).Add(retentionSafetyMargin)
	fake := &holeProvider{deepBefore: retentionEdge}
	h := newOutageHarness(t, fake)
	h.cfg.Hyperliquid.BackfillDays = 5 // window reaches past retention (about 3.47 days)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 5*24*60)
	holeStart := now.Add(-108 * time.Hour) // 4.5 days back: beyond retention
	deleteMinutes(t, h.db, h.instrumentID, holeStart, 65)

	ctx := context.Background()
	integrityScan(ctx, "test", h.symbol, h.svc, h.state, h.cfg, h.logger, clock.Real{})
	store := marketdata.NewStore(h.db, clock.Real{})
	gaps, err := store.ListOpenGaps(ctx, h.instrumentID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, g := range gaps {
		if g.Start.Time().Equal(holeStart) && g.End.Time().Equal(holeStart.Add(65*time.Minute)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("hole beyond retention not recorded; open gaps: %+v", gaps)
	}
	if n := fake.deepWindows(); n != 0 {
		t.Fatalf("asked the exchange %d times for minutes older than its retention", n)
	}

	// Second scan: the recorded hole is known -- not reported, recorded or
	// fetched again.
	windowsBefore := len(fake.windows)
	counting := &countingLost{integrityState: h.state}
	integrityScan(ctx, "test", h.symbol, h.svc, counting, h.cfg, h.logger, clock.Real{})
	gaps2, _ := store.ListOpenGaps(ctx, h.instrumentID)
	if len(gaps2) != len(gaps) || len(fake.windows) != windowsBefore || counting.n != 0 {
		t.Fatalf("second scan retried: gaps %d -> %d, fetches %d -> %d, lost-gap records %d", len(gaps), len(gaps2), windowsBefore, len(fake.windows), counting.n)
	}
}

// Scale: the scan statement at 30,000 stored rows per instrument (about
// three weeks of 1-minute data), and at ten times that, over the widest
// window (the whole stored span) and over the default 3-day window.
func TestIntegration_IntegrityScan_Timing(t *testing.T) {
	if os.Getenv("ARGUS_RUN_SLOW_TESTS") != "1" {
		t.Skip("slow: set ARGUS_RUN_SLOW_TESTS=1")
	}
	ctx := context.Background()
	for _, n := range []int{30_000, 300_000} {
		h := newOutageHarness(t, &holeProvider{})
		newest := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
		insertClosedCandles(t, h.db, h.instrumentID, newest, n)
		for i := 1; i <= 20; i++ { // 20 scattered 4-minute holes
			deleteMinutes(t, h.db, h.instrumentID, newest.Add(-time.Duration(i*n/21)*time.Minute), 4)
		}
		if _, err := h.db.Exec(`ANALYZE candles`); err != nil {
			t.Fatal(err)
		}
		store := marketdata.NewStore(h.db, clock.Real{})
		for _, w := range []struct {
			name string
			from time.Time
		}{{"whole stored span", newest.Add(-time.Duration(n) * time.Minute)}, {"3-day window", newest.Add(-72 * time.Hour)}} {
			const runs = 10
			var total, worst time.Duration
			var found int
			for range runs {
				start := time.Now()
				ranges, err := store.MissingRanges(ctx, h.instrumentID, w.from, newest.Add(time.Minute), w.from)
				el := time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				found = len(ranges)
				total += el
				worst = max(worst, el)
			}
			t.Logf("rows=%d window=%s: %d ranges, mean %v, worst %v", n, w.name, found, total/runs, worst)
		}
	}
}

type countingLost struct {
	integrityState
	n int
}

func (c *countingLost) recordLostGap(ctx context.Context, instrumentID int64, start, end time.Time) error {
	c.n++
	return c.integrityState.recordLostGap(ctx, instrumentID, start, end)
}

// An open candle_gaps row INSIDE retention (the dev stack has hundreds:
// unaligned, overlapping, recorded by the periodic backfill's own tail and
// never resolved) does not hide a hole: those minutes can be fetched, so
// they are.
func TestIntegration_IntegrityScan_OpenGapRowInsideRetentionDoesNotHideAHole(t *testing.T) {
	fake := &holeProvider{}
	h := newOutageHarness(t, fake)
	now := time.Now().UTC().Truncate(time.Minute)
	newest := now.Add(-time.Minute)
	insertClosedCandles(t, h.db, h.instrumentID, newest, 360)
	holeStart := newest.Add(-2 * time.Hour)
	deleteMinutes(t, h.db, h.instrumentID, holeStart, 4)
	if _, err := h.db.Exec(`INSERT INTO candle_gaps (instrument_id, gap_start, gap_end, detected_at) VALUES ($1, $2, $3, now())`,
		h.instrumentID, holeStart.Add(-4*time.Minute-37*time.Second), holeStart.Add(time.Minute+23*time.Second)); err != nil {
		t.Fatal(err)
	}
	integrityScan(context.Background(), "test", h.symbol, h.svc, h.state, h.cfg, h.logger, clock.Real{})
	if miss := missingMinutes(t, h.db, h.instrumentID, holeStart, holeStart.Add(3*time.Minute)); len(miss) > 0 {
		t.Fatalf("%d minutes under an open gap row inside retention left unfilled: %v", len(miss), miss)
	}
}
