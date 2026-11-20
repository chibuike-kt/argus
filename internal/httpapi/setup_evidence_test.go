package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/setups"
)

// fakeEvidenceStore is a setupEvidenceStore with no database. It counts
// calls so a test can prove the list path makes one statement per kind,
// not one per setup.
type fakeEvidenceStore struct {
	lifecycle      map[int64]setups.Lifecycle
	evidence       map[int64]setups.RecordedEvidence
	lifecycleCalls int
	byIDCalls      int
}

func (f *fakeEvidenceStore) LifecycleByID(_ context.Context, _ []int64) (map[int64]setups.Lifecycle, error) {
	f.lifecycleCalls++
	return f.lifecycle, nil
}

func (f *fakeEvidenceStore) LatestEvidence(_ context.Context, setupID int64) (json.RawMessage, string, time.Time, bool, error) {
	rec, ok := f.evidence[setupID]
	if !ok {
		return nil, "", time.Time{}, false, nil
	}
	return rec.Evidence, rec.EventType, rec.At, true, nil
}

func (f *fakeEvidenceStore) LatestEvidenceByID(_ context.Context, _ []int64) (map[int64]setups.RecordedEvidence, error) {
	f.byIDCalls++
	return f.evidence, nil
}

// recordedChain is a one-step chain whose level is 12345 scaled at the
// test instrument's price_scale 2, so it must reach the wire as "123.45".
func recordedChain(t *testing.T) setups.RecordedEvidence {
	t.Helper()
	// A non-UTC time on purpose: the wire must carry it as UTC.
	lagos := time.FixedZone("WAT", 3600)
	pivot := time.Date(2026, 11, 6, 12, 30, 0, 0, lagos)
	raw, err := json.Marshal(analysis.PlanEvidence{
		Schema: 1, InstrumentID: 42, Timeframe: "1m", Direction: "bullish",
		AsOf: pivot, StrategyVersion: "sv", AlgorithmVersion: "av",
		Steps: []analysis.EvidenceStep{{
			Kind: "sweep", Role: "swept", Section: "4", Result: "met",
			Candles: []analysis.EvidenceCandle{{Role: "sweep", OpenTime: pivot, CandleID: 7, Revision: 2}},
			Levels:  []analysis.EvidenceLevel{{Name: "swept_low", Price: 12345, SwingID: 9, Time: &pivot}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal chain: %v", err)
	}
	return setups.RecordedEvidence{Evidence: raw, EventType: "detected", At: pivot}
}

func getEvidence(t *testing.T, h *apiHandler) SetupEvidence {
	t.Helper()
	resp, err := h.GetSetupEvidence(context.Background(), GetSetupEvidenceRequestObject{Id: 1})
	if err != nil {
		t.Fatalf("GetSetupEvidence: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitGetSetupEvidenceResponse(rec); err != nil {
		t.Fatalf("visit: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got SetupEvidence
	decodeBody(t, rec, &got)
	return got
}

func TestGetSetupEvidence_NotRecorded(t *testing.T) {
	h := &apiHandler{
		store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, instruments: setupsTestInstruments(),
		evidence: &fakeEvidenceStore{}, logger: slog.Default(),
	}
	got := getEvidence(t, h)
	if got.SetupId != 1 || got.Recorded || got.Steps != nil {
		t.Fatalf("got %+v, want setup 1, recorded false, no steps", got)
	}
}

func TestGetSetupEvidence_Recorded(t *testing.T) {
	h := &apiHandler{
		store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, instruments: setupsTestInstruments(),
		evidence: &fakeEvidenceStore{evidence: map[int64]setups.RecordedEvidence{1: recordedChain(t)}}, logger: slog.Default(),
	}
	got := getEvidence(t, h)
	if !got.Recorded || got.Steps == nil || len(*got.Steps) != 1 {
		t.Fatalf("got %+v, want one recorded step", got)
	}
	step := (*got.Steps)[0]
	if step.Kind != "sweep" || step.Result != "met" || step.Levels == nil || step.Candles == nil {
		t.Fatalf("step = %+v", step)
	}
	if price := (*step.Levels)[0].Price; price != "123.45" {
		t.Fatalf("level price = %q, want 123.45 (scale 2 decimal string)", price)
	}
	ot := (*step.Candles)[0].OpenTime
	if ot == nil || ot.Location() != time.UTC || !ot.Equal(time.Date(2026, 11, 6, 11, 30, 0, 0, time.UTC)) {
		t.Fatalf("candle open_time = %v, want 2026-11-06T11:30:00Z in UTC", ot)
	}
	if got.EventType == nil || *got.EventType != "detected" {
		t.Fatalf("event_type = %v, want detected", got.EventType)
	}
}

func TestGetSetupEvidence_NotFound(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{getByIDErr: setups.ErrSetupNotFound}, evidence: &fakeEvidenceStore{}, logger: slog.Default()}
	resp, err := h.GetSetupEvidence(context.Background(), GetSetupEvidenceRequestObject{Id: 1})
	if err != nil {
		t.Fatalf("GetSetupEvidence: %v", err)
	}
	if _, ok := resp.(GetSetupEvidence404JSONResponse); !ok {
		t.Fatalf("response = %T, want 404", resp)
	}
}

// TestListSetups_IncludeEvidence proves include_evidence attaches each
// setup's own chain and lifecycle, with one lifecycle and one evidence
// statement for the whole page (no per-setup query), and that without
// the flag no evidence is fetched.
func TestListSetups_IncludeEvidence(t *testing.T) {
	recorded, old := testSetup(), testSetup()
	recorded.FoundMode = "live"
	old.ID, old.FoundMode = 2, "unknown"
	detected := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	ev := &fakeEvidenceStore{
		lifecycle: map[int64]setups.Lifecycle{
			1: {DetectedAt: &detected, EvidenceRecorded: true},
			2: {DetectedAt: &detected},
		},
		evidence: map[int64]setups.RecordedEvidence{1: recordedChain(t)},
	}
	h := &apiHandler{
		store: &fakeSetupsStore{setups: []setups.Setup{recorded, old}}, instruments: setupsTestInstruments(),
		evidence: ev, logger: slog.Default(),
	}
	yes := true
	resp, err := h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 42, IncludeEvidence: &yes}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	list, ok := resp.(ListSetups200JSONResponse)
	if !ok || len(list) != 2 {
		t.Fatalf("response = %#v", resp)
	}
	if ev.lifecycleCalls != 1 || ev.byIDCalls != 1 {
		t.Fatalf("calls: lifecycle %d, evidence %d, want 1 and 1", ev.lifecycleCalls, ev.byIDCalls)
	}
	if list[0].Evidence == nil || !list[0].Evidence.Recorded || *list[0].FoundMode != "live" || !*list[0].EvidenceRecorded {
		t.Fatalf("setup 1 = %+v, want recorded evidence, live", list[0])
	}
	if list[1].Evidence == nil || list[1].Evidence.Recorded || *list[1].FoundMode != "unknown" || *list[1].EvidenceRecorded {
		t.Fatalf("setup 2 = %+v, want evidence not recorded, unknown", list[1])
	}
	if list[0].DetectedAt == nil || !list[0].DetectedAt.Equal(detected) || list[0].TriggeredAt != nil {
		t.Fatalf("lifecycle times = %v %v", list[0].DetectedAt, list[0].TriggeredAt)
	}

	ev.byIDCalls = 0
	resp, err = h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 42}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	list = resp.(ListSetups200JSONResponse)
	if ev.byIDCalls != 0 || list[0].Evidence != nil || list[0].FoundMode == nil {
		t.Fatalf("without include_evidence: evidence calls %d, evidence %v, found_mode %v", ev.byIDCalls, list[0].Evidence, list[0].FoundMode)
	}
}

func TestGetSetup_Lifecycle(t *testing.T) {
	s := testSetup()
	s.FoundMode = "replay"
	trig := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
	h := &apiHandler{
		store: &fakeSetupsStore{setups: []setups.Setup{s}}, instruments: setupsTestInstruments(),
		evidence: &fakeEvidenceStore{lifecycle: map[int64]setups.Lifecycle{1: {TriggeredAt: &trig}}}, logger: slog.Default(),
	}
	resp, err := h.GetSetup(context.Background(), GetSetupRequestObject{Id: 1})
	if err != nil {
		t.Fatalf("GetSetup: %v", err)
	}
	got := resp.(GetSetup200JSONResponse)
	if got.FoundMode == nil || *got.FoundMode != "replay" || got.TriggeredAt == nil || !got.TriggeredAt.Equal(trig) || got.Evidence != nil {
		t.Fatalf("got %+v, want replay, triggered at %v, no evidence block", got, trig)
	}
}
