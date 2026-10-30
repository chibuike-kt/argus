package analysis

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// PlanStatus is section 14's minimal, derived trade-plan status.
type PlanStatus int

const (
	// PlanNone means section 13 returned no POI: there is no plan.
	PlanNone PlanStatus = iota
	// PlanActionable means a POI is present, not invalidated, and
	// price has not yet reached the entry.
	PlanActionable
	// PlanTriggered means a finalized candle after the POI formed has
	// reached the entry, and the plan is not invalidated.
	PlanTriggered
	// PlanInvalidated means a finalized candle has closed strictly
	// through the protected level. Outranks PlanTriggered.
	PlanInvalidated
)

func (s PlanStatus) String() string {
	switch s {
	case PlanActionable:
		return "actionable"
	case PlanTriggered:
		return "triggered"
	case PlanInvalidated:
		return "invalidated"
	default:
		return "none"
	}
}

// Target is one trade-plan target: a structural level strictly beyond
// the entry in the trade direction. RR is kept as an exact fraction
// (math/big.Rat), never float64 (guide 13): R:R is informational only
// (ADR-012), so what matters is that it reproduces bit-for-bit under
// the same inputs (ADR-019), not approximate trading math. RR is never
// nil on a constructed Target.
type Target struct {
	Level quant.Price
	RR    *big.Rat
}

// TradePlan is section 14's derived risk-entry plan. Entry, Stop,
// Targets, and ProtectedSwingID are only meaningful when
// Status != PlanNone. POI is the section-13 result this plan was built
// from (zero value when Status == PlanNone).
type TradePlan struct {
	Status    PlanStatus
	Direction Direction
	Entry     quant.Price
	Stop      quant.Price
	Targets   []Target

	POI              POI
	ProtectedSwingID int64
}

// priceDistance returns the absolute difference between a and b.
func priceDistance(a, b quant.Price) quant.Price {
	if a > b {
		return a - b
	}
	return b - a
}

// collectOpposingSwingLevels returns every confirmed swing of
// swingType known as of asOf, walking pages most-recent-first via the
// same batched lookup section 9 uses (evaluateStructuralCandidates),
// continuing until the candidate history is exhausted. Unlike section
// 9, which stops at the first qualifying swing, targets needs every
// candidate: it ranks by distance from the entry, not by recency, so
// an older swing can rank ahead of a newer one.
func collectOpposingSwingLevels(ctx context.Context, s *Store, instrumentID int64, timeframe string, swingType SwingType, asOf time.Time) ([]Swing, error) {
	var all []Swing
	cursor := asOf
	for {
		batch, err := s.ListLatestSwingsConfirmedBefore(ctx, instrumentID, timeframe, swingType, cursor, asOf, structuralLiquidityScanBatch)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return all, nil
		}
		all = append(all, batch...)
		if len(batch) < structuralLiquidityScanBatch {
			return all, nil
		}
		oldest := batch[len(batch)-1]
		prior, err := s.LatestSwingBefore(ctx, instrumentID, timeframe, swingType, oldest.PivotTime.Time())
		if errors.Is(err, ErrSwingNotFound) {
			return all, nil
		}
		if err != nil {
			return nil, err
		}
		cursor = prior.PivotTime.Time()
	}
}

// resolveTargets resolves section 14's targets: confirmed opposing
// swings (section 1) plus the current transactional-liquidity level
// (section 10) -- kept as an explicit second source per spec section
// 14, though in practice it is already one of the confirmed opposing
// swings; the two converge to the same set, and de-duplication below
// collapses any overlap. Levels strictly beyond entry in the trade
// direction are sorted nearest-first, collapsed when within minTick of
// the nearer level already kept, and capped at three.
func resolveTargets(ctx context.Context, store *Store, state StateSnapshot, direction Direction, entry quant.Price, asOf time.Time, minTick quant.Price) ([]quant.Price, error) {
	var swingType SwingType
	switch direction {
	case DirectionBullish:
		swingType = SwingHigh
	case DirectionBearish:
		swingType = SwingLow
	}

	swings, err := collectOpposingSwingLevels(ctx, store, state.InstrumentID, state.Timeframe, swingType, asOf)
	if err != nil {
		return nil, err
	}
	levels := make([]quant.Price, 0, len(swings)+1)
	for _, sw := range swings {
		levels = append(levels, sw.Price)
	}
	txLiq, ok, err := ResolveTransactionalLiquidity(ctx, store, state, asOf)
	if err != nil {
		return nil, err
	}
	if ok {
		levels = append(levels, txLiq.Price)
	}

	beyond := make([]quant.Price, 0, len(levels))
	for _, lvl := range levels {
		switch direction {
		case DirectionBullish:
			if lvl > entry {
				beyond = append(beyond, lvl)
			}
		case DirectionBearish:
			if lvl < entry {
				beyond = append(beyond, lvl)
			}
		}
	}
	sort.Slice(beyond, func(i, j int) bool {
		return priceDistance(beyond[i], entry) < priceDistance(beyond[j], entry)
	})

	deduped := make([]quant.Price, 0, len(beyond))
	for _, lvl := range beyond {
		if len(deduped) > 0 && priceDistance(lvl, deduped[len(deduped)-1]) <= minTick {
			continue
		}
		deduped = append(deduped, lvl)
	}
	if len(deduped) > 3 {
		deduped = deduped[:3]
	}
	return deduped, nil
}

// computeRR returns (target-entry)/(entry-stop) for bullish, mirrored
// for bearish, as an exact big.Rat. The guard in ResolveTradePlan
// already ensures entry != stop whenever this is called, so the
// denominator is never zero.
func computeRR(direction Direction, target, entry, stop quant.Price) *big.Rat {
	var num, den int64
	switch direction {
	case DirectionBullish:
		num, den = int64(target-entry), int64(entry-stop)
	case DirectionBearish:
		num, den = int64(entry-target), int64(stop-entry)
	}
	return big.NewRat(num, den)
}

// resolvePlanStatus scans finalized candles from the POI's own
// formation candle (exclusive) through asOf for the two status
// triggers: a close strictly through stop (invalidated) and a
// low/high reaching entry (triggered). The formation candle itself is
// excluded: its own low/high trivially straddle its own high/low (the
// entry level, when the POI is the order block itself), which would
// otherwise mark every plan triggered at the instant it forms.
//
// Point-in-time: every candle is bounded by close_time <= asOf, the
// same discipline as sections 12 and 13; status recomputes
// deterministically as later candles close or a correction replays.
func resolvePlanStatus(ctx context.Context, candles CandleSource, state StateSnapshot, poi POI, entry, stop quant.Price, asOf time.Time) (PlanStatus, error) {
	window, err := candles.ListCandles(ctx, state.InstrumentID, poi.OrderBlockOpenTime, asOf)
	if err != nil {
		return PlanNone, err
	}

	invalidated, triggered := false, false
	for _, c := range window {
		if !c.IsClosed || c.CloseTime.After(asOf) || c.ID == poi.OrderBlockCandleID {
			continue
		}
		switch poi.Direction {
		case DirectionBullish:
			if c.Close < int64(stop) {
				invalidated = true
			}
			if c.Low <= int64(entry) {
				triggered = true
			}
		case DirectionBearish:
			if c.Close > int64(stop) {
				invalidated = true
			}
			if c.High >= int64(entry) {
				triggered = true
			}
		}
	}

	switch {
	case invalidated:
		return PlanInvalidated, nil
	case triggered:
		return PlanTriggered, nil
	default:
		return PlanActionable, nil
	}
}

// ResolveTradePlan resolves the active structure's trade plan (spec
// section 14): the risk entry derived from the current POI (section
// 13). ok is false -- undefined, never a PlanNone result -- only when
// a POI is present but its entry is not strictly beyond its stop (the
// guard spec section 14 states explicitly: do not clamp or fabricate).
// A POI of POINone is itself a defined result, PlanNone, not
// undefined.
//
// Point-in-time: ResolvePOI (and everything it depends on) and
// resolveTargets/resolvePlanStatus each bound their own candle and
// swing reads by asOf; ResolveTradePlan adds no further reads of its
// own beyond the protected-anchor lookup, which is itself a snapshot
// of the live state passed in (already the caller's asOf-correct
// state).
func ResolveTradePlan(ctx context.Context, store *Store, candles CandleSource, state StateSnapshot, asOf time.Time, interval time.Duration, minLegSpan int, minTick quant.Price) (TradePlan, bool, error) {
	poi, err := ResolvePOI(ctx, store, candles, state, asOf, interval, minLegSpan)
	if err != nil {
		return TradePlan{}, false, err
	}
	if poi.Type == POINone {
		return TradePlan{Status: PlanNone, Direction: state.Direction, POI: poi}, true, nil
	}

	var anchorID *int64
	switch poi.Direction {
	case DirectionBullish:
		anchorID = secondFibLowAnchor(state)
	case DirectionBearish:
		anchorID = secondFibHighAnchor(state)
	}
	if anchorID == nil {
		// Invariant violation, not a legitimate undefined outcome: a
		// present POI requires a present order block (ResolvePOI/
		// ResolveOrderBlock), which itself requires this same anchor to
		// be non-nil. CLAUDE.md rule 3 -- stop and flag rather than guess.
		return TradePlan{}, false, fmt.Errorf("analysis: trade plan: POI %v present with no protected anchor", poi.Type)
	}
	anchor, err := store.GetSwingByID(ctx, *anchorID)
	if err != nil {
		return TradePlan{}, false, err
	}

	var entry, stop quant.Price
	switch poi.Direction {
	case DirectionBullish:
		entry, stop = poi.High, anchor.Price
	case DirectionBearish:
		entry, stop = poi.Low, anchor.Price
	}

	beyond := false
	switch poi.Direction {
	case DirectionBullish:
		beyond = entry > stop
	case DirectionBearish:
		beyond = entry < stop
	}
	if !beyond {
		return TradePlan{}, false, nil
	}

	levels, err := resolveTargets(ctx, store, state, poi.Direction, entry, asOf, minTick)
	if err != nil {
		return TradePlan{}, false, err
	}
	targets := make([]Target, len(levels))
	for i, lvl := range levels {
		targets[i] = Target{Level: lvl, RR: computeRR(poi.Direction, lvl, entry, stop)}
	}

	status, err := resolvePlanStatus(ctx, candles, state, poi, entry, stop, asOf)
	if err != nil {
		return TradePlan{}, false, err
	}

	return TradePlan{
		Status: status, Direction: poi.Direction, Entry: entry, Stop: stop, Targets: targets,
		POI: poi, ProtectedSwingID: anchor.ID,
	}, true, nil
}
