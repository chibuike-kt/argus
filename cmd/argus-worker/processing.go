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

	// cursor is the open_time this process has already processed up to,
	// per instrument -- in-memory only, not persisted. On a cold start
	// (including every restart today) it is empty, so the first tick
	// for each instrument processes its entire currently-stored candle
	// history once; every tick after that only sees genuinely new
	// candles. That one-time replay is safe (analysis.Engine and
	// setups.Reconcile are both designed to be idempotent over an
	// already-processed range) and, for this brief's bounded
	// fixture/replay dataset, cheap. A persisted resume cursor (so a
	// restart does not even do that one replay) is a reasonable later
	// hardening step, not something this brief asked for -- see the
	// final report for why it was left out rather than guessed at.
	cursor map[int64]time.Time
}

func newProcessor(db *sql.DB, logger *slog.Logger, clk clock.Clock, instrumentStore *instruments.Store, candles analysis.CandleSource, engine *analysis.Engine, analysisStore *analysis.Store, setupsStore *setups.Store) *processor {
	return &processor{
		db:            db,
		logger:        logger,
		clk:           clk,
		instruments:   instrumentStore,
		candles:       candles,
		engine:        engine,
		analysisStore: analysisStore,
		setupsStore:   setupsStore,
		cursor:        make(map[int64]time.Time),
	}
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
	for _, inst := range enabled {
		if err := p.processInstrument(ctx, inst); err != nil {
			p.logger.Error("worker: process instrument", "instrument_id", inst.ID, "error", err)
		}
	}
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
func (p *processor) processInstrument(ctx context.Context, inst instruments.Instrument) error {
	latest, err := p.candles.LatestClosed(ctx, inst.ID)
	if errors.Is(err, analysis.ErrNoClosedCandles) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("latest closed candle: %w", err)
	}

	start := p.cursor[inst.ID]
	end := latest.CloseTime
	if !end.After(start) {
		return nil // nothing new since the last tick
	}

	newCandles, err := p.candles.ListCandles(ctx, inst.ID, start, end)
	if err != nil {
		return fmt.Errorf("list candles [%s,%s): %w", start, end, err)
	}

	for _, c := range newCandles {
		if !c.IsClosed {
			continue
		}
		if err := p.engine.OnCandleClosed(ctx, inst.ID, timeframe, c); err != nil {
			return fmt.Errorf("engine on candle closed open_time=%s: %w", c.OpenTime, err)
		}
		if err := p.reconcileSetup(ctx, inst, c.CloseTime); err != nil {
			return fmt.Errorf("reconcile setup open_time=%s: %w", c.OpenTime, err)
		}
	}

	p.cursor[inst.ID] = end
	return nil
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
func (p *processor) reconcileSetup(ctx context.Context, inst instruments.Instrument, asOf time.Time) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txAnalysisStore := p.analysisStore.WithTx(tx)
	txSetupsStore := p.setupsStore.WithTx(tx)

	state, err := txAnalysisStore.GetState(ctx, inst.ID, timeframe)
	if err != nil {
		return fmt.Errorf("get state: %w", err)
	}

	plan, ok, err := analysis.ResolveTradePlan(ctx, txAnalysisStore, p.candles, state, asOf, time.Minute, analysis.DefaultMinLegSpan, inst.TickSize)
	if err != nil {
		return fmt.Errorf("resolve trade plan: %w", err)
	}
	if !ok {
		// Undefined (a POI present whose entry is not strictly beyond
		// its stop) is an invariant violation per ResolveTradePlan's own
		// doc comment, not a legitimate PlanNone outcome -- surfaced,
		// never silently guessed at (CLAUDE.md rule 3).
		p.logger.Warn("worker: trade plan undefined, skipping reconciliation this step",
			"instrument_id", inst.ID, "timeframe", timeframe, "as_of", asOf)
		return tx.Commit()
	}

	now := p.clk.Now()
	setup, event, err := setups.Reconcile(ctx, txSetupsStore, inst.ID, timeframe, plan, algorithmVersion, asOf, now)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
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
	return nil
}
