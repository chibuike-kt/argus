// Package backtest replays history through the real analysis engine,
// point in time, and evaluates the setups it produces with
// internal/outcomes -- strategy spec section 16, brief Part 2. It never
// reimplements detection: OnCandleClosed, ResolveTradePlan, Reconcile and
// InvalidateBreached are the exact same calls cmd/argus-worker's own
// processing loop makes, just driven over a bounded historical range
// instead of "since the cursor to latest."
package backtest

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/outcomes"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"
)

// timeframe matches cmd/argus-worker's own const: the engine only
// evaluates 1m today (analysis/candle_source.go's own doc comment).
const timeframe = "1m"

// Params is one backtest run's own scope.
type Params struct {
	InstrumentIDs    []int64
	Start, End       time.Time // candle open_time range to replay, [Start, End)
	AlgorithmVersion string
}

// Run replays history through the real analysis engine and setups
// lifecycle (never a second copy of detection), point in time, over
// [Start, End), for every instrument in Params, then evaluates every
// setup it produced with outcomes.Evaluate.
//
// db must point at a database set aside for this run, never the live
// dev/production database: Run reuses analysis.Store and setups.Store
// exactly as cmd/argus-worker does, which means it writes to the
// existing confirmed_swings/structure_events/structure_state/setups/
// setup_events tables exactly as a live worker would -- no new tables of
// its own, but not zero writes either (see this package's doc.go and the
// backtest brief's own report back for why the engine's own persisted-
// state design, ADR-009, makes that unavoidable when reusing the real
// engine rather than inventing a second copy of it).
func Run(ctx context.Context, db *sql.DB, params Params) (Report, error) {
	instStore := instruments.NewStore(db)
	mdStore := marketdata.NewStore(db, clock.Real{})
	candleSource := analysis.NewMarketdataSource(mdStore)
	analysisStore := analysis.NewStore(db)
	setupsStore := setups.NewStore(db)

	engine, err := analysis.NewEngine(db, candleSource, analysisStore, clock.Real{}, analysis.Config{
		// LeftBars/RightBars match cmd/argus-worker/main.go's own
		// hardcoded values exactly: the spec fixes both at 2 for v0.1
		// (analysis.Config's own doc comment), not a value this package
		// chooses independently.
		AlgorithmVersion: params.AlgorithmVersion, LeftBars: 2, RightBars: 2, Interval: time.Minute,
	})
	if err != nil {
		return Report{}, fmt.Errorf("backtest: new engine: %w", err)
	}

	var rows []Row
	var symbols []string
	var providers []string

	for _, instrumentID := range params.InstrumentIDs {
		inst, err := instStore.GetByID(ctx, instrumentID)
		if err != nil {
			return Report{}, fmt.Errorf("backtest: get instrument %d: %w", instrumentID, err)
		}
		symbols = append(symbols, inst.NativeSymbol)
		providers = append(providers, inst.Provider)

		candles, err := candleSource.ListCandles(ctx, instrumentID, params.Start, params.End)
		if err != nil {
			return Report{}, fmt.Errorf("backtest: list candles instrument=%d: %w", instrumentID, err)
		}

		var lastClose time.Time
		for _, c := range candles {
			if !c.IsClosed {
				continue
			}
			if err := engine.OnCandleClosed(ctx, instrumentID, timeframe, c); err != nil {
				return Report{}, fmt.Errorf("backtest: engine on candle closed instrument=%d open_time=%s: %w", instrumentID, c.OpenTime, err)
			}
			if err := reconcileSetupAt(ctx, db, analysisStore, setupsStore, candleSource, inst, c.CloseTime, params.AlgorithmVersion); err != nil {
				return Report{}, fmt.Errorf("backtest: reconcile instrument=%d open_time=%s: %w", instrumentID, c.OpenTime, err)
			}
			if _, err := setups.InvalidateBreached(ctx, db, setupsStore, instrumentID, timeframe, c.OpenTime, quant.Price(c.Close), params.AlgorithmVersion, c.CloseTime, clock.Real{}.Now()); err != nil {
				return Report{}, fmt.Errorf("backtest: invalidate breached instrument=%d open_time=%s: %w", instrumentID, c.OpenTime, err)
			}
			lastClose = c.CloseTime
		}
		if lastClose.IsZero() {
			continue // no closed candles for this instrument in range: nothing to evaluate
		}

		instRows, err := evaluateInstrument(ctx, setupsStore, candleSource, inst, lastClose)
		if err != nil {
			return Report{}, fmt.Errorf("backtest: evaluate instrument=%d: %w", instrumentID, err)
		}
		rows = append(rows, instRows...)
	}

	// Sorted by Key (every component a real-world fact, never a
	// database-assigned sequence value) rather than left in whatever
	// order ListByInstrument/the database happened to return: two runs
	// of the identical fixture against databases with different id
	// sequences must produce rows in the same order too, not just rows
	// with the same content (brief's own "two runs, same bytes").
	sort.Slice(rows, func(i, j int) bool { return setupKeyLess(rows[i].Key, rows[j].Key) })

	if err := checkNoKeyCollisions(rows); err != nil {
		return Report{}, err
	}

	dataset, honestyNote := datasetLabel(providers)
	meta := Meta{
		Dataset:          dataset,
		HonestyNote:      honestyNote,
		RulesVersion:     outcomes.RulesVersion,
		AlgorithmVersion: params.AlgorithmVersion,
		PeriodStart:      params.Start,
		PeriodEnd:        params.End,
		Instruments:      symbols,
	}
	return BuildReport(rows, meta), nil
}

// setupKeyLess orders two SetupKeys deterministically: by symbol, then
// by when each identity was first detected, then (for the vanishingly
// unlikely tie) by POI type and direction.
func setupKeyLess(a, b SetupKey) bool {
	if a.Symbol != b.Symbol {
		return a.Symbol < b.Symbol
	}
	if !a.DetectedAt.Equal(b.DetectedAt) {
		return a.DetectedAt.Before(b.DetectedAt)
	}
	if a.POIType != b.POIType {
		return a.POIType < b.POIType
	}
	return a.Direction < b.Direction
}

// distinctProviders returns the sorted, deduplicated provider names
// across providers.
func distinctProviders(providers []string) []string {
	seen := make(map[string]bool, len(providers))
	var out []string
	for _, p := range providers {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// datasetLabel derives the report's own dataset label and honesty
// sentence from the run's own instruments' provider(s), rather than
// hardcoding "dev fixture dataset": ADR-007 is still open, so nothing
// about today's provider set is guaranteed to still be "fixture" once it
// resolves, and this report must say what it actually ran on, not what
// was true the last time someone wrote this string by hand.
func datasetLabel(providers []string) (dataset, honestyNote string) {
	names := distinctProviders(providers)
	label := strings.Join(names, ", ")
	if label == "" {
		label = "no instruments"
	}
	dataset = fmt.Sprintf("%s dataset", label)
	honestyNote = fmt.Sprintf("%s data is not market evidence: this report proves the plumbing works, it is not evidence about the model.", label)
	return dataset, honestyNote
}

// reconcileSetupAt is cmd/argus-worker/processing.go's own reconcileSetup,
// reproduced here rather than imported (that file is package main, not
// importable): the section 12-14 resolver chain plus setups.Reconcile,
// as one atomic unit, for one instrument at one closed candle.
func reconcileSetupAt(ctx context.Context, db *sql.DB, analysisStore *analysis.Store, setupsStore *setups.Store, candles analysis.CandleSource, inst instruments.Instrument, asOf time.Time, version string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txAnalysisStore := analysisStore.WithTx(tx)
	txSetupsStore := setupsStore.WithTx(tx)

	state, err := txAnalysisStore.GetState(ctx, inst.ID, timeframe)
	if err != nil {
		return fmt.Errorf("get state: %w", err)
	}

	plan, ok, err := analysis.ResolveTradePlan(ctx, txAnalysisStore, candles, state, asOf, time.Minute, analysis.DefaultMinLegSpan, inst.TickSize)
	if err != nil {
		return fmt.Errorf("resolve trade plan: %w", err)
	}
	if !ok {
		// Invariant violation (a POI present whose entry is not strictly
		// beyond its stop), not a legitimate PlanNone -- matches
		// cmd/argus-worker's own handling: skip this step rather than
		// guess (CLAUDE.md rule 3), never silently paper over it.
		return tx.Commit()
	}

	if _, _, err := setups.Reconcile(ctx, txSetupsStore, inst.ID, timeframe, plan, version, asOf, clock.Real{}.Now()); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	return tx.Commit()
}

// evaluateInstrument evaluates every setup Run's replay produced for
// inst, using each one's own first ("detected") plan snapshot -- the
// plan as it was originally proposed -- as the plan outcomes.Evaluate
// simulates. A later amendment (ADR-011: a POI source-candle correction)
// updates the setup's own persisted Plan but is not re-evaluated as a
// separate case in V1: flagged in the backtest brief's own report back,
// not decided silently.
func evaluateInstrument(ctx context.Context, setupsStore *setups.Store, candles analysis.CandleSource, inst instruments.Instrument, asOf time.Time) ([]Row, error) {
	all, err := setupsStore.ListByInstrument(ctx, inst.ID, timeframe)
	if err != nil {
		return nil, fmt.Errorf("list setups: %w", err)
	}

	var rows []Row
	for _, s := range all {
		events, err := setupsStore.ListEvents(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("list events setup=%d: %w", s.ID, err)
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("setup %d has no events (invariant violation)", s.ID)
		}
		detected := events[0]

		var invalidatedAt *time.Time
		for _, ev := range events {
			if ev.Type == setups.EventInvalidated {
				t := ev.EvaluationTime
				invalidatedAt = &t
				break
			}
		}

		plan, err := planFromSnapshot(detected.Plan)
		if err != nil {
			return nil, fmt.Errorf("setup %d: %w", s.ID, err)
		}

		window, err := candles.ListCandles(ctx, inst.ID, detected.EvaluationTime, asOf.Add(time.Minute))
		if err != nil {
			return nil, fmt.Errorf("list candles setup=%d: %w", s.ID, err)
		}
		usable := make([]analysis.CandleInput, 0, len(window))
		for _, c := range window {
			if c.CloseTime.After(detected.EvaluationTime) {
				usable = append(usable, c)
			}
		}

		o, err := outcomes.Evaluate(plan, usable, invalidatedAt, inst.TickSize, asOf)
		if err != nil {
			return nil, fmt.Errorf("evaluate setup=%d: %w", s.ID, err)
		}

		rrs := make([]*big.Rat, len(plan.Targets))
		for i, t := range plan.Targets {
			rrs[i] = t.RR
		}

		key := SetupKey{
			Symbol: inst.NativeSymbol, Timeframe: timeframe, POIType: detected.Plan.POI.Type,
			Direction: plan.Direction.String(), DetectedAt: detected.EvaluationTime,
			AlgorithmVersion: detected.AlgorithmVersion,
		}
		rows = append(rows, Row{
			InstrumentID: inst.ID, Symbol: inst.NativeSymbol, Direction: plan.Direction.String(),
			Timeframe: timeframe, POIType: detected.Plan.POI.Type, Key: key, rawSetupID: s.ID,
			Entry: plan.Entry, Stop: plan.Stop, PriceScale: inst.PriceScale,
			TargetRRs: rrs, Outcome: o,
		})
	}
	return rows, nil
}

// planFromSnapshot reverses setups.newPlanSnapshot (unexported, so this
// is its own small inverse) back into an analysis.TradePlan Evaluate can
// consume.
func planFromSnapshot(snap setups.PlanSnapshot) (analysis.TradePlan, error) {
	var direction analysis.Direction
	switch snap.Direction {
	case "bullish":
		direction = analysis.DirectionBullish
	case "bearish":
		direction = analysis.DirectionBearish
	default:
		return analysis.TradePlan{}, fmt.Errorf("backtest: plan snapshot has unknown direction %q", snap.Direction)
	}

	var status analysis.PlanStatus
	switch snap.Status {
	case "actionable":
		status = analysis.PlanActionable
	case "triggered":
		status = analysis.PlanTriggered
	case "invalidated":
		status = analysis.PlanInvalidated
	default:
		return analysis.TradePlan{}, fmt.Errorf("backtest: plan snapshot has unknown status %q", snap.Status)
	}

	targets := make([]analysis.Target, len(snap.Targets))
	for i, t := range snap.Targets {
		rrVal, ok := new(big.Rat).SetString(t.RR)
		if !ok {
			return analysis.TradePlan{}, fmt.Errorf("backtest: target %d has malformed RR %q", i, t.RR)
		}
		targets[i] = analysis.Target{Level: t.Level, RR: rrVal}
	}

	return analysis.TradePlan{
		Status: status, Direction: direction, Entry: snap.Entry, Stop: snap.Stop, Targets: targets,
	}, nil
}
