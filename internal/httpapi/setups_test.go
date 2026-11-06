package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/setups"
)

// fakeSetupsStore is the handwritten fake setupsStore's own doc comment
// promises -- found/not-found/empty-list are pure routing and mapping
// logic, so these tests exercise them directly, without a database
// (guide 10). The real *setups.Store is exercised by that package's
// own integration tests, and server.go wires it in for production.
type fakeSetupsStore struct {
	setups        []setups.Setup
	events        []setups.SetupEvent
	err           error
	getByIDErr    error
	listEventsErr error
}

func (f *fakeSetupsStore) ListByInstrument(_ context.Context, _ int64, _ string) ([]setups.Setup, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.setups, nil
}

func (f *fakeSetupsStore) GetByID(_ context.Context, _ int64) (setups.Setup, error) {
	if f.getByIDErr != nil {
		return setups.Setup{}, f.getByIDErr
	}
	if len(f.setups) == 0 {
		return setups.Setup{}, setups.ErrSetupNotFound
	}
	return f.setups[0], nil
}

func (f *fakeSetupsStore) ListEvents(_ context.Context, _ int64) ([]setups.SetupEvent, error) {
	if f.listEventsErr != nil {
		return nil, f.listEventsErr
	}
	return f.events, nil
}

func testSetup() setups.Setup {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	return setups.Setup{
		ID: 1, InstrumentID: 42, Timeframe: "1m",
		POIType: "order_block", POISourceCandleID: 100,
		Direction: "bullish", StrategyVersion: "swing-structure-v1",
		State: setups.StateActionable, POISourceCandleRevision: 1,
		Plan:           setups.PlanSnapshot{Status: "actionable", Direction: "bullish", Entry: 90, Stop: 70},
		EvaluationTime: now, AlgorithmVersion: "swing-structure-v1",
		CreatedAt: now, UpdatedAt: now,
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
}

func TestListSetups_OK(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, logger: slog.Default()}
	resp, err := h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 42}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitListSetupsResponse(rec); err != nil {
		t.Fatalf("VisitListSetupsResponse: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []Setup
	decodeBody(t, rec, &got)
	if len(got) != 1 || got[0].Id != 1 || got[0].State != SetupState("actionable") {
		t.Fatalf("got %+v", got)
	}
}

func TestListSetups_Empty(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{setups: nil}, logger: slog.Default()}
	resp, err := h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 42}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitListSetupsResponse(rec); err != nil {
		t.Fatalf("VisitListSetupsResponse: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// An empty result is "[]", never "null" -- guide's own JSON detail
	// (var items []T encodes null; items := []T{} encodes []) applied
	// to a real response contract, not just a style note.
	if got := rec.Body.String(); got != "[]\n" {
		t.Fatalf("body = %q, want []", got)
	}
}

func TestListSetups_InvalidInstrumentID(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{}, logger: slog.Default()}
	resp, err := h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 0}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitListSetupsResponse(rec); err != nil {
		t.Fatalf("VisitListSetupsResponse: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var got Error
	decodeBody(t, rec, &got)
	if got.Error.Code != "invalid_argument" {
		t.Fatalf("error.code = %q, want invalid_argument", got.Error.Code)
	}
}

// TestListSetups_MissingRequiredParam exercises the router-level
// binding error path (ChiServerOptions.ErrorHandlerFunc, wired in
// server.go), not apiHandler directly -- a request missing
// instrument_id entirely never reaches ListSetups at all. NewRouter
// only needs a non-nil Logger for this case: the request fails before
// touching the database.
func TestListSetups_MissingRequiredParam(t *testing.T) {
	r := NewRouter(Deps{Logger: slog.Default()})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/setups")
	if err != nil {
		t.Fatalf("GET /api/v1/setups: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	var got Error
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Error.Code != "invalid_argument" {
		t.Fatalf("error.code = %q, want invalid_argument", got.Error.Code)
	}
}

func TestGetSetup_Found(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, logger: slog.Default()}
	resp, err := h.GetSetup(context.Background(), GetSetupRequestObject{Id: 1})
	if err != nil {
		t.Fatalf("GetSetup: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitGetSetupResponse(rec); err != nil {
		t.Fatalf("VisitGetSetupResponse: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got Setup
	decodeBody(t, rec, &got)
	if got.Id != 1 || got.InstrumentId != 42 {
		t.Fatalf("got %+v", got)
	}
}

func TestGetSetup_NotFound(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{getByIDErr: setups.ErrSetupNotFound}, logger: slog.Default()}
	resp, err := h.GetSetup(context.Background(), GetSetupRequestObject{Id: 999})
	if err != nil {
		t.Fatalf("GetSetup: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitGetSetupResponse(rec); err != nil {
		t.Fatalf("VisitGetSetupResponse: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var got Error
	decodeBody(t, rec, &got)
	if got.Error.Code != "not_found" {
		t.Fatalf("error.code = %q, want not_found", got.Error.Code)
	}
}

func TestListSetupEvents_Found(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	ev := setups.SetupEvent{ID: 5, SetupID: 1, Type: setups.EventDetected, Plan: setups.PlanSnapshot{}, EvaluationTime: now, AlgorithmVersion: "v1", CreatedAt: now}
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}, events: []setups.SetupEvent{ev}}, logger: slog.Default()}

	resp, err := h.ListSetupEvents(context.Background(), ListSetupEventsRequestObject{Id: 1})
	if err != nil {
		t.Fatalf("ListSetupEvents: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitListSetupEventsResponse(rec); err != nil {
		t.Fatalf("VisitListSetupEventsResponse: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []SetupEvent
	decodeBody(t, rec, &got)
	if len(got) != 1 || got[0].Id != 5 || got[0].EventType != SetupEventEventType("detected") {
		t.Fatalf("got %+v", got)
	}
}

func TestListSetupEvents_NotFound(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{getByIDErr: setups.ErrSetupNotFound}, logger: slog.Default()}
	resp, err := h.ListSetupEvents(context.Background(), ListSetupEventsRequestObject{Id: 999})
	if err != nil {
		t.Fatalf("ListSetupEvents: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitListSetupEventsResponse(rec); err != nil {
		t.Fatalf("VisitListSetupEventsResponse: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var got Error
	decodeBody(t, rec, &got)
	if got.Error.Code != "not_found" {
		t.Fatalf("error.code = %q, want not_found", got.Error.Code)
	}
}

func TestPing(t *testing.T) {
	h := &apiHandler{}
	resp, err := h.Ping(context.Background(), PingRequestObject{})
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitPingResponse(rec); err != nil {
		t.Fatalf("VisitPingResponse: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
