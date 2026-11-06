package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/cursor"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/setups"
)

// timeframe is the only timeframe the analysis engine can evaluate
// today. analysis/candle_source.go's own doc comment is explicit about
// why: confirmed_swings/structure_events/structure_state carry a
// timeframe column as a real seam, but there is no aggregated-candle
// table yet for anything other than the canonical 1-minute series, so
// looping over an instrument's SupportedIntervals here would claim
// multi-timeframe support the engine does not actually have.
const timeframe = "1m"

// algorithmVersion is passed to analysis.Engine as Config.AlgorithmVersion
// and to setups.Reconcile as its strategy/algorithm version string (see
// reconcile.go's doc comment on why those are the same value today).
const algorithmVersion = "swing-structure-v1"

// processor drives analysis.Engine and setups.Reconcile over every
// enabled instrument, once per tick. It is the worker's replacement for
// the heartbeat ticker: a real, sequential, per-instrument processing
// loop (this brief's own scope -- concurrency and per-instrument
// partitioning are explicitly a later concern, ADR-005's own "open
// decisions" list).
type processor struct {
	db            *sql.DB
	logger        *slog.Logger
	clk           clock.Clock
	instruments   *instruments.Store
	candles       analysis.CandleSource
	engine        *analysis.Engine
	analysisStore *analysis.Store
	setupsStore   *setups.Store
	cursorStore   *cursor.Store

	// cursorCache mirrors the persisted processing_cursors row per
	// instrument, so most ticks avoid a DB round trip just to find out
	// where to resume -- the DB row (cursorStore), not this map, is
	// the durable source of truth a restart reads from. Populated
	// lazily on first sight of each instrument (see processInstrument),
	// not pre-loaded at startup, since the enabled-instrument set can
	// change between ticks.
	cursorCache map[int64]time.Time

	// testFailBeforeReconcile, when non-nil, is called with each
	// candle's close_time right after analysis.Engine's step commits
	// but before setups reconciliation (and the cursor advance) runs
	// for it. Returning true fails that candle at exactly that point,
	// simulating a crash between the two commits -- a test-only hook
	// (see processing_cursor_integration_test.go); newProcessor never
	// sets it, so it is always nil, a no-op, in production.
	testFailBeforeReconcile func(closeTime time.Time) bool
}

func newProcessor(db *sql.DB, logger *slog.Logger, clk clock.Clock, instrumentStore *instruments.Store, candles analysis.CandleSource, engine *analysis.Engine, analysisStore *analysis.Store, setupsStore *setups.Store, cursorStore *cursor.Store) *processor {
	return &processor{
		db:            db,
		logger:        logger,
		clk:           clk,
		instruments:   instrumentStore,
		candles:       candles,
		engine:        engine,
		analysisStore: analysisStore,
		setupsStore:   setupsStore,
		cursorStore:   cursorStore,
		cursorCache:   make(map[int64]time.Time),
	}
}

// lagWarnThreshold is Part D's own stated bound: an instrument more
// than this far behind its own latest closed candle is worth a Warn,
// not just a Debug line -- either the worker is falling behind a live
// feed, or something stopped it from fully catching up this tick.
const lagWarnThreshold = 5 * time.Minute

// instrumentRunResult is processInstrument's own bookkeeping for
// run's end-of-tick summary and per-instrument log line (worker-
// hardening brief, Part D) -- candlesProcessed and eventsWritten
// count only candles/events that actually committed, never ones a
// failed step (or the test-only crash hook) left uncommitted.
type instrumentRunResult struct {
	cursorBefore, cursorAfter time.Time
	latestClose               time.Time
	candlesProcessed          int
	eventsWritten             int
}

func (r instrumentRunResult) lag() time.Duration {
	return r.latestClose.Sub(r.cursorAfter)
}

// run processes every enabled instrument once, sequentially. A single
// instrument's failure is logged and does not stop the others --
// matching guide 14's "classify before retrying" rule, a per-instrument
// store or analysis error is retried next tick for free (the cursor for
// that instrument was never advanced), so logging and continuing here
// is not swallowing the failure, only deferring its retry to the next
// tick.
func (p *processor) run(ctx context.Context) {
	enabled, err := p.instruments.ListEnabled(ctx)
	if err != nil {
		p.logger.Error("worker: list enabled instruments", "error", err)
		return
	}

	var totalCandles, totalEvents, totalErrored int
	for _, inst := range enabled {
		result, err := p.processInstrument(ctx, inst)
		if err != nil {
			totalErrored++
			p.logger.Error("worker: process instrument", "instrument_id", inst.ID, "error", err)
			continue
		}
		totalCandles += result.candlesProcessed
		totalEvents += result.eventsWritten

		p.logger.Debug("worker: tick processed instrument",
			"instrument_id", inst.ID,
			"candles_processed", result.candlesProcessed,
			"cursor_before", result.cursorBefore,
			"cursor_after", result.cursorAfter,
			"lag", result.lag(),
		)
		if lag := result.lag(); lag > lagWarnThreshold {
			p.logger.Warn("worker: instrument processing lag exceeds threshold",
				"instrument_id", inst.ID, "lag", lag, "threshold", lagWarnThreshold)
		}
	}

	p.logger.Info("worker: tick complete",
		"instruments_processed", len(enabled),
		"candles_processed", totalCandles,
		"setup_events_written", totalEvents,
		"instruments_errored", totalErrored,
	)
}

// processInstrument processes every candle newly closed since this
// instrument's cursor, in order: the structure engine first, then the
// section 12-14 resolver chain and setups.Reconcile, for that one
// candle, before moving to the next -- never the whole new batch
// through the engine followed by a single reconciliation at the
// latest candle. Reconciling at every candle, not just the batch's
// last one, is required, not a style choice: TradePlan.Status can
// legitimately pass through an intermediate state (actionable ->
// triggered -> invalidated) within one batch if the worker fell behind,
// and skipping straight to the last candle's status would silently
// lose the triggered event that spec section 15.4 requires exist.
//
// The returned instrumentRunResult reflects progress actually made
// before any error: a caller can log candles_processed/cursor_after
// even when err != nil, which is exactly the "retried next tick for
// free" case run's own doc comment describes.
func (p *processor) processInstrument(ctx context.Context, inst instruments.Instrument) (instrumentRunResult, error) {
	latest, err := p.candles.LatestClosed(ctx, inst.ID)
	if errors.Is(err, analysis.ErrNoClosedCandles) {
		return instrumentRunResult{}, nil
	}
	if err != nil {
		return instrumentRunResult{}, fmt.Errorf("latest closed candle: %w", err)
	}

	start, err := p.loadCursor(ctx, inst.ID)
	if err != nil {
		return instrumentRunResult{}, fmt.Errorf("load cursor: %w", err)
	}
	result := instrumentRunResult{cursorBefore: start, cursorAfter: start, latestClose: latest.CloseTime}

	end := latest.CloseTime
	if !end.After(start) {
		return result, nil // nothing new since the last tick
	}

	newCandles, err := p.candles.ListCandles(ctx, inst.ID, start, end)
	if err != nil {
		return result, fmt.Errorf("list candles [%s,%s): %w", start, end, err)
	}

	for _, c := range newCandles {
		if !c.IsClosed {
			continue
		}
		if err := p.engine.OnCandleClosed(ctx, inst.ID, timeframe, c); err != nil {
			return result, fmt.Errorf("engine on candle closed open_time=%s: %w", c.OpenTime, err)
		}
		if p.testFailBeforeReconcile != nil && p.testFailBeforeReconcile(c.CloseTime) {
			return result, fmt.Errorf("processing: simulated crash after engine, before reconcile, at %s", c.CloseTime)
		}
		wrote, err := p.reconcileSetup(ctx, inst, c.CloseTime)
		if err != nil {
			return result, fmt.Errorf("reconcile setup open_time=%s: %w", c.OpenTime, err)
		}
		if wrote {
			result.eventsWritten++
		}
		// Advanced only now, after BOTH the engine step and setups
		// reconciliation above have committed -- a crash between them
		// (or at the simulated hook above) leaves the persisted
		// cursor at the PREVIOUS candle's close_time, so a restart
		// reprocesses this exact candle. Safe: analysis.Engine and
		// setups.Reconcile are both idempotent over an
		// already-processed position (worker-hardening brief, Part B).
		now := p.clk.Now()
		if err := p.cursorStore.SetAt(ctx, inst.ID, timeframe, c.CloseTime, now); err != nil {
			return result, fmt.Errorf("advance cursor open_time=%s: %w", c.OpenTime, err)
		}
		p.cursorCache[inst.ID] = c.CloseTime
		result.cursorAfter = c.CloseTime
		result.candlesProcessed++
	}

	return result, nil
}

// loadCursor returns this instrument's resume point, reading the
// in-memory cache when warm and falling back to the persisted
// cursorStore (ultimately ErrNotFound -- ADR-005's "workers must
// tolerate... process restarts": the zero time, a full replay from
// the beginning, same as any other cold start) only on first sight of
// this instrument in the process's lifetime.
func (p *processor) loadCursor(ctx context.Context, instrumentID int64) (time.Time, error) {
	if t, ok := p.cursorCache[instrumentID]; ok {
		return t, nil
	}
	t, err := p.cursorStore.Get(ctx, instrumentID, timeframe)
	if errors.Is(err, cursor.ErrNotFound) {
		t = time.Time{}
	} else if err != nil {
		return time.Time{}, err
	}
	p.cursorCache[instrumentID] = t
	return t, nil
}

// reconcileSetup runs the section 12-14 resolver chain at asOf and
// hands the result to setups.Reconcile, as one atomic unit -- the same
// contract analysis.Engine.step has with analysis.Store, applied here
// to setups.Store instead (reconcile.go's own doc comment). This is a
// transaction separate from the one analysis.Engine already committed
// inside OnCandleClosed above, not a shared one: Engine owns its own
// per-candle atomic unit, and does not (and must not: setups depends on
// analysis, so the reverse import would cycle) know setups exists.
// minTick is the instrument's own registered tick size (ADR-006) --
// the semantically correct value for "two targets within one tick
// collapse to one", not an arbitrary constant.
//
// wrote reports whether a setup_events row was actually written this
// call (false for a no-op reconciliation, same as setups.Reconcile's
// own nil-event return) -- run's own tick-summary line counts this,
// per Part D.
func (p *processor) reconcileSetup(ctx context.Context, inst instruments.Instrument, asOf time.Time) (wrote bool, err error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txAnalysisStore := p.analysisStore.WithTx(tx)
	txSetupsStore := p.setupsStore.WithTx(tx)

	state, err := txAnalysisStore.GetState(ctx, inst.ID, timeframe)
	if err != nil {
		return false, fmt.Errorf("get state: %w", err)
	}

	plan, ok, err := analysis.ResolveTradePlan(ctx, txAnalysisStore, p.candles, state, asOf, time.Minute, analysis.DefaultMinLegSpan, inst.TickSize)
	if err != nil {
		return false, fmt.Errorf("resolve trade plan: %w", err)
	}
	if !ok {
		// Undefined (a POI present whose entry is not strictly beyond
		// its stop) is an invariant violation per ResolveTradePlan's own
		// doc comment, not a legitimate PlanNone outcome -- surfaced,
		// never silently guessed at (CLAUDE.md rule 3).
		p.logger.Warn("worker: trade plan undefined, skipping reconciliation this step",
			"instrument_id", inst.ID, "timeframe", timeframe, "as_of", asOf)
		return false, tx.Commit()
	}

	now := p.clk.Now()
	setup, event, err := setups.Reconcile(ctx, txSetupsStore, inst.ID, timeframe, plan, algorithmVersion, asOf, now)
	if err != nil {
		return false, fmt.Errorf("reconcile: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}

	if event != nil {
		p.logger.Info("worker: setup lifecycle event",
			"setup_id", setup.ID,
			"instrument_id", inst.ID,
			"timeframe", timeframe,
			"event_type", event.Type.String(),
			"state", setup.State.String(),
			"direction", setup.Direction,
			"poi_type", setup.POIType,
		)
	}
	return event != nil, nil
}
