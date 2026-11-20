package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"
)

// setupEvidenceStore is what the evidence and lifecycle fields need from
// setups.Store (Phase 6) -- consumer-defined like setupsStore.
type setupEvidenceStore interface {
	LifecycleByID(ctx context.Context, setupIDs []int64) (map[int64]setups.Lifecycle, error)
	LatestEvidence(ctx context.Context, setupID int64) (json.RawMessage, string, time.Time, bool, error)
	LatestEvidenceByID(ctx context.Context, setupIDs []int64) (map[int64]setups.RecordedEvidence, error)
}

var _ setupEvidenceStore = (*setups.Store)(nil)

// withLifecycle adds how the setup was found and its lifecycle times.
func withLifecycle(out *Setup, s setups.Setup, lc setups.Lifecycle) {
	mode := SetupFoundMode(s.FoundMode)
	out.FoundMode = &mode
	recorded := lc.EvidenceRecorded
	out.EvidenceRecorded = &recorded
	out.DetectedAt = lc.DetectedAt
	out.TriggeredAt = lc.TriggeredAt
	out.InvalidatedAt = lc.InvalidatedAt
}

// enrichSetups sets every setup's lifecycle (one statement) and, when
// withEvidence, its newest recorded evidence (one more statement) --
// never a query per setup.
func (h *apiHandler) enrichSetups(ctx context.Context, rows []setups.Setup, out []Setup, scale quant.Scale, withEvidence bool) error {
	if h.evidence == nil || len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	lc, err := h.evidence.LifecycleByID(ctx, ids)
	if err != nil {
		return err
	}
	var ev map[int64]setups.RecordedEvidence
	if withEvidence {
		if ev, err = h.evidence.LatestEvidenceByID(ctx, ids); err != nil {
			return err
		}
	}
	for i, r := range rows {
		withLifecycle(&out[i], r, lc[r.ID])
		if withEvidence {
			rec, ok := ev[r.ID]
			doc, err := renderEvidence(r.ID, rec.Evidence, rec.EventType, rec.At, ok, scale)
			if err != nil {
				return err
			}
			out[i].Evidence = &doc
		}
	}
	return nil
}

// GetSetupEvidence serves GET /setups/{id}/evidence.
func (h *apiHandler) GetSetupEvidence(ctx context.Context, request GetSetupEvidenceRequestObject) (GetSetupEvidenceResponseObject, error) {
	row, err := h.store.GetByID(ctx, request.Id)
	if errors.Is(err, setups.ErrSetupNotFound) {
		return GetSetupEvidence404JSONResponse(apiError("not_found", "setup not found")), nil
	}
	if err != nil {
		h.logger.Error("httpapi: get setup for evidence", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("get setup %d: %w", request.Id, err)
	}
	inst, err := h.instruments.GetByID(ctx, row.InstrumentID)
	if err != nil {
		h.logger.Error("httpapi: get instrument for evidence", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("get instrument %d: %w", row.InstrumentID, err)
	}
	if h.evidence == nil {
		return GetSetupEvidence200JSONResponse(SetupEvidence{SetupId: row.ID}), nil
	}
	raw, eventType, at, ok, err := h.evidence.LatestEvidence(ctx, row.ID)
	if err != nil {
		h.logger.Error("httpapi: latest evidence", "setup_id", request.Id, "error", err)
		return nil, fmt.Errorf("latest evidence %d: %w", request.Id, err)
	}
	doc, err := renderEvidence(row.ID, raw, eventType, at, ok, inst.PriceScale)
	if err != nil {
		h.logger.Error("httpapi: render evidence", "setup_id", request.Id, "error", err)
		return nil, err
	}
	return GetSetupEvidence200JSONResponse(doc), nil
}

// renderEvidence turns the stored chain (scaled integer prices) into the
// wire document (decimal strings at the instrument's scale). ok false is
// "evidence not recorded for this setup": recorded false, nothing else.
func renderEvidence(setupID int64, raw json.RawMessage, eventType string, at time.Time, ok bool, scale quant.Scale) (SetupEvidence, error) {
	out := SetupEvidence{SetupId: setupID}
	if !ok || len(raw) == 0 {
		return out, nil
	}
	var ev analysis.PlanEvidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		return SetupEvidence{}, fmt.Errorf("decode evidence of setup %d: %w", setupID, err)
	}
	out.Recorded = true
	out.EventType = &eventType
	t := at.UTC()
	out.EvaluationTime = &t
	schema := int32(ev.Schema) //nolint:gosec // a small schema version number
	out.Schema = &schema
	out.Timeframe = &ev.Timeframe
	out.Direction = &ev.Direction
	out.StrategyVersion = &ev.StrategyVersion
	out.AlgorithmVersion = &ev.AlgorithmVersion
	asOf := ev.AsOf.UTC()
	out.AsOf = &asOf
	steps := make([]EvidenceStep, 0, len(ev.Steps))
	for _, s := range ev.Steps {
		step := EvidenceStep{
			Kind:    EvidenceStepKind(s.Kind),
			Section: s.Section,
			Result:  EvidenceStepResult(s.Result),
		}
		if s.Role != "" {
			role := s.Role
			step.Role = &role
		}
		if s.Reason != "" {
			reason := s.Reason
			step.Reason = &reason
		}
		if len(s.Attrs) > 0 {
			attrs := s.Attrs
			step.Attrs = &attrs
		}
		if len(s.Candles) > 0 {
			cs := make([]EvidenceCandle, 0, len(s.Candles))
			for _, c := range s.Candles {
				ec := EvidenceCandle{Role: c.Role}
				if !c.OpenTime.IsZero() {
					ot := c.OpenTime.UTC()
					ec.OpenTime = &ot
				}
				if c.CandleID != 0 {
					id := c.CandleID
					ec.CandleId = &id
				}
				if c.Revision != 0 {
					rev := c.Revision
					ec.Revision = &rev
				}
				cs = append(cs, ec)
			}
			step.Candles = &cs
		}
		if len(s.Levels) > 0 {
			ls := make([]EvidenceLevel, 0, len(s.Levels))
			for _, l := range s.Levels {
				el := EvidenceLevel{Name: l.Name, Price: l.Price.String(scale)}
				if l.SwingID != 0 {
					id := l.SwingID
					el.SwingId = &id
				}
				if l.Time != nil {
					lt := l.Time.UTC()
					el.Time = &lt
				}
				ls = append(ls, el)
			}
			step.Levels = &ls
		}
		steps = append(steps, step)
	}
	out.Steps = &steps
	return out, nil
}
