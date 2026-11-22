package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/engineview"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
)

// engineViewStore is what the structure window, engine status and activity
// feed need from engineview.Store (Phase 6.2 and 6.5) -- consumer-defined,
// so the handlers' validation and mapping are unit tested with a fake.
type engineViewStore interface {
	StructureWindow(ctx context.Context, instrumentID int64, timeframe string, from, to time.Time, limit int32) (engineview.Window, error)
	EngineStatus(ctx context.Context) ([]engineview.InstrumentStatus, error)
	Activity(ctx context.Context, q engineview.ActivityQuery) ([]engineview.ActivityItem, *engineview.Cursor, error)
}

var _ engineViewStore = (*engineview.Store)(nil)

// Bounds for the structure window (implementation limits, matching the
// OpenAPI spec's own documented values).
const (
	defaultStructureLimit = 500
	minStructureLimit     = 10
	maxStructureLimit     = 2000
	maxStructureWindow    = 31 * 24 * time.Hour
)

// Bounds for the activity feed.
const (
	defaultActivityLimit   = 50
	maxActivityLimit       = 200
	maxActivityInstruments = 50
)

// maxEnabledInstruments bounds the one read of the enabled list the
// activity feed makes when no instrument is named.
const maxEnabledInstruments = 1000

// GetInstrumentStructure serves GET /instruments/{id}/structure.
func (h *apiHandler) GetInstrumentStructure(ctx context.Context, request GetInstrumentStructureRequestObject) (GetInstrumentStructureResponseObject, error) {
	p := request.Params
	limit := int32(defaultStructureLimit)
	if p.Limit != nil {
		limit = *p.Limit
	}
	if limit < minStructureLimit || limit > maxStructureLimit {
		return GetInstrumentStructure400JSONResponse(apiError("invalid_argument", fmt.Sprintf("limit must be between %d and %d", minStructureLimit, maxStructureLimit))), nil
	}
	if !p.From.Before(p.To) {
		return GetInstrumentStructure400JSONResponse(apiError("invalid_argument", "from must be before to")), nil
	}
	if p.To.Sub(p.From) > maxStructureWindow {
		return GetInstrumentStructure400JSONResponse(apiError("invalid_argument", "the window [from, to) can be at most 31 days")), nil
	}
	timeframe := defaultTimeframe
	if p.Timeframe != nil && *p.Timeframe != "" {
		timeframe = *p.Timeframe
	}
	if !evaluableTimeframes[timeframe] {
		return GetInstrumentStructure400JSONResponse(apiError("invalid_argument", "timeframe must be one of 1m, 5m, 15m, 1h, 4h, 1d")), nil
	}

	inst, err := h.instruments.GetByID(ctx, request.Id)
	if errors.Is(err, instruments.ErrNotFound) || (err == nil && !inst.Enabled) {
		return GetInstrumentStructure404JSONResponse(apiError("not_found", "instrument not found")), nil
	}
	if err != nil {
		h.logger.Error("httpapi: get instrument for structure", "instrument_id", request.Id, "error", err)
		return nil, fmt.Errorf("get instrument %d: %w", request.Id, err)
	}

	w, err := h.engineView.StructureWindow(ctx, inst.ID, timeframe, p.From, p.To, limit)
	if err != nil {
		h.logger.Error("httpapi: structure window", "instrument_id", inst.ID, "error", err)
		return nil, fmt.Errorf("structure window %d: %w", inst.ID, err)
	}
	return GetInstrumentStructure200JSONResponse(toAPIStructureWindow(inst.ID, timeframe, w, inst.PriceScale)), nil
}

// clock is the handler's clock (Deps.Clock), the real one when unset.
func (h *apiHandler) clock() clock.Clock {
	if h.clk == nil {
		return clock.Real{}
	}
	return h.clk
}

// evaluableTimeframes are the timeframes a structure row can carry (the
// chart's own intervals). Only 1m is analysed today, so the others return
// empty windows -- a real seam, not a 400, when a second one is analysed.
var evaluableTimeframes = map[string]bool{"1m": true, "5m": true, "15m": true, "1h": true, "4h": true, "1d": true}

// toAPIStructureWindow renders a window for the wire: prices at the
// instrument's scale, every time in UTC whatever location it arrived in.
func toAPIStructureWindow(instrumentID int64, timeframe string, w engineview.Window, scale quant.Scale) StructureWindow {
	out := StructureWindow{
		InstrumentId: instrumentID,
		Timeframe:    timeframe,
		From:         w.From.UTC(),
		To:           w.To.UTC(),
		Swings:       make([]StructureSwing, 0, len(w.Swings)),
		Breaks:       make([]StructureBreak, 0, len(w.Breaks)),
		Sweeps:       make([]StructureSweep, 0, len(w.Sweeps)),
	}
	if w.NextFrom != nil {
		nf := w.NextFrom.UTC()
		out.NextFrom = &nf
	}
	level := func(l engineview.Level) StructureLevel {
		return StructureLevel{SwingId: l.SwingID, Price: l.Price.String(scale), Time: l.Time.UTC()}
	}
	for _, s := range w.Swings {
		out.Swings = append(out.Swings, StructureSwing{
			Id: s.ID, SwingType: StructureSwingSwingType(s.Type), PivotTime: s.PivotTime.UTC(), Price: s.Price.String(scale), ConfirmedAt: s.ConfirmedAt.UTC(),
		})
	}
	for _, b := range w.Breaks {
		ab := StructureBreak{
			Id: b.ID, EventType: StructureBreakEventType(b.EventType), DirectionBefore: b.DirectionBefore, DirectionAfter: b.DirectionAfter,
			TriggerOpenTime: b.TriggerOpenTime.UTC(), SweepValidated: b.SweepValidated, SweepId: b.SweepID, ProtectedSwingId: b.ProtectedSwingID,
		}
		if b.BrokenSwing != nil {
			l := level(*b.BrokenSwing)
			ab.BrokenSwing = &l
		}
		out.Breaks = append(out.Breaks, ab)
	}
	for _, s := range w.Sweeps {
		out.Sweeps = append(out.Sweeps, StructureSweep{
			Id: s.ID, Side: StructureSweepSide(s.Side), TriggerOpenTime: s.TriggerOpenTime.UTC(),
			PenetrationExtreme: s.PenetrationExtreme.String(scale), ReclaimClose: s.ReclaimClose.String(scale),
			SweptSwing: level(s.Swept),
		})
	}
	return out
}

// GetEngineStatus serves GET /engine/status.
func (h *apiHandler) GetEngineStatus(ctx context.Context, _ GetEngineStatusRequestObject) (GetEngineStatusResponseObject, error) {
	rows, err := h.engineView.EngineStatus(ctx)
	if err != nil {
		h.logger.Error("httpapi: engine status", "error", err)
		return nil, fmt.Errorf("engine status: %w", err)
	}
	out := EngineStatus{AsOf: h.clock().Now().UTC(), Instruments: make([]EngineInstrumentStatus, 0, len(rows))}
	for _, r := range rows {
		st := EngineInstrumentStatus{
			InstrumentId: r.InstrumentID, NativeSymbol: r.NativeSymbol, Provider: r.Provider,
			Analysed: r.Analysed, EvaluatedThrough: r.EvaluatedThrough, LastEvaluatedAt: r.LastEvaluatedAt,
			LatestClosedCloseTime: r.LatestClosedCloseTime, ClosedCandlesBehind: r.ClosedCandlesBehind,
		}
		if r.Analysed {
			tf := r.Timeframe
			st.Timeframe = &tf
		}
		if s, ok := r.BehindSeconds(); ok {
			st.BehindSeconds = &s
		}
		out.Instruments = append(out.Instruments, st)
	}
	return GetEngineStatus200JSONResponse(out), nil
}

// ListActivity serves GET /activity.
func (h *apiHandler) ListActivity(ctx context.Context, request ListActivityRequestObject) (ListActivityResponseObject, error) {
	p := request.Params
	limit := int32(defaultActivityLimit)
	if p.Limit != nil {
		limit = *p.Limit
	}
	if limit < 1 || limit > maxActivityLimit {
		return ListActivity400JSONResponse(apiError("invalid_argument", fmt.Sprintf("limit must be between 1 and %d", maxActivityLimit))), nil
	}
	q := engineview.ActivityQuery{Limit: limit, Setups: true, Structure: true, Gaps: true}
	if p.Kind != nil && len(*p.Kind) > 0 {
		q.Setups, q.Structure, q.Gaps = false, false, false
		for _, k := range *p.Kind {
			switch k {
			case ListActivityParamsKindSetup:
				q.Setups = true
			case ListActivityParamsKindStructure:
				q.Structure = true
			case ListActivityParamsKindGap:
				q.Gaps = true
			default:
				return ListActivity400JSONResponse(apiError("invalid_argument", "kind must be setup, structure or gap")), nil
			}
		}
	}
	if p.Cursor != nil && *p.Cursor != "" {
		c, err := engineview.DecodeCursor(*p.Cursor)
		if err != nil {
			return ListActivity400JSONResponse(apiError("invalid_argument", "cursor is not valid")), nil
		}
		q.Before = &c
	}

	// Only enabled instruments ever appear (deny by default): the named
	// ids are intersected with the enabled list, never trusted as given.
	enabledOnly := true
	enabled, err := h.instruments.ListAllFiltered(ctx, nil, &enabledOnly, maxEnabledInstruments)
	if err != nil {
		h.logger.Error("httpapi: list enabled instruments for activity", "error", err)
		return nil, fmt.Errorf("list enabled instruments: %w", err)
	}
	isEnabled := make(map[int64]bool, len(enabled))
	for _, in := range enabled {
		isEnabled[in.ID] = true
	}
	if p.InstrumentId != nil && len(*p.InstrumentId) > 0 {
		if len(*p.InstrumentId) > maxActivityInstruments {
			return ListActivity400JSONResponse(apiError("invalid_argument", fmt.Sprintf("at most %d instrument_id values", maxActivityInstruments))), nil
		}
		seen := make(map[int64]bool, len(*p.InstrumentId))
		for _, id := range *p.InstrumentId {
			if id <= 0 {
				return ListActivity400JSONResponse(apiError("invalid_argument", "instrument_id must be a positive integer")), nil
			}
			if isEnabled[id] && !seen[id] {
				seen[id] = true
				q.InstrumentIDs = append(q.InstrumentIDs, id)
			}
		}
	} else {
		for _, in := range enabled {
			q.InstrumentIDs = append(q.InstrumentIDs, in.ID)
		}
	}

	page := ActivityPage{Items: []ActivityItem{}}
	if len(q.InstrumentIDs) == 0 {
		return ListActivity200JSONResponse(page), nil
	}
	items, next, err := h.engineView.Activity(ctx, q)
	if err != nil {
		h.logger.Error("httpapi: activity", "error", err)
		return nil, fmt.Errorf("activity: %w", err)
	}
	for _, it := range items {
		page.Items = append(page.Items, toAPIActivityItem(it))
	}
	if next != nil {
		s := next.Encode()
		page.NextCursor = &s
	}
	return ListActivity200JSONResponse(page), nil
}

func toAPIActivityItem(it engineview.ActivityItem) ActivityItem {
	out := ActivityItem{
		Kind: ActivityItemKind(it.Kind), Id: it.ID, InstrumentId: it.InstrumentID, Timeframe: it.Timeframe,
		At: it.At, EventTime: it.EventTime, Detail: it.Detail, SetupId: it.SetupID, GapEnd: it.GapEnd,
	}
	str := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	out.Direction = str(it.Direction)
	out.PoiType = str(it.POIType)
	out.FoundMode = str(it.FoundMode)
	return out
}
