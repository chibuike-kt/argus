package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"
)

// fakeSessionResolver/fakeUserLookup satisfy this package's own
// sessionResolver/userLookup interfaces (auth_middleware.go) with no
// database at all -- deny-by-default (ADR-015) now runs ahead of every
// /api/v1 handler, this package's own pre-auth router tests included,
// so a NewRouter-level test needs *some* session to reach its handler;
// these fakes accept any non-empty raw token as valid.
type fakeSessionResolver struct{ userID int64 }

func (f fakeSessionResolver) Resolve(_ context.Context, rawToken string) (session.Session, error) {
	if rawToken == "" {
		return session.Session{}, session.ErrNotFound
	}
	return session.Session{ID: 1, UserID: f.userID}, nil
}

func (f fakeSessionResolver) Touch(context.Context, int64) error { return nil }

type fakeUserLookup struct{ user identity.User }

func (f fakeUserLookup) GetByID(context.Context, int64) (identity.User, error) {
	return f.user, nil
}

// authTestCookie is the fixed session cookie every router-level test
// in this file attaches, matching fakeSessionResolver's own "any
// non-empty token is valid" rule.
const authTestCookie = "test-session-token"

func withAuthTestDeps(d Deps) Deps {
	d.Sessions = fakeSessionResolver{userID: 1}
	d.Users = fakeUserLookup{user: identity.User{ID: 1, Email: "router-test@example.com"}}
	d.AuthPolicy = config.AuthPolicy{CookieNameLocal: "argus_session"}
	return d
}

// setupsTestInstruments is the fakeInstrumentsStore every setups.go
// handler test below needs once it reaches the instrument-scale lookup
// (web-wiring-followup brief, Part 3): instrument 42 at
// testInstrumentDomain's own price_scale 2, matching testSetup()'s own
// InstrumentID.
func setupsTestInstruments() *fakeInstrumentsStore {
	return &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{42: testInstrumentDomain(42)}}
}

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

func (f *fakeSetupsStore) ListByInstrumentFiltered(_ context.Context, _ int64, _ string, state *setups.State, _ int32) ([]setups.Setup, error) {
	if f.err != nil {
		return nil, f.err
	}
	if state == nil {
		return f.setups, nil
	}
	out := make([]setups.Setup, 0, len(f.setups))
	for _, s := range f.setups {
		if s.State == *state {
			out = append(out, s)
		}
	}
	return out, nil
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

// TestToAPIPlanSnapshot_DecimalStringFormatting is the web-wiring-
// followup brief's own required proof for Part 3: entry/stop/
// targets[].level/poi.low/poi.high are emitted as decimal strings built
// from the setup's instrument price_scale (quant.Price.String(scale)),
// not bare scaled integers -- at scale 0 (no fractional digits), at
// scale 2 (the common case), and across a three-target list. rr is an
// exact rational string, left untouched by this formatting.
func TestToAPIPlanSnapshot_DecimalStringFormatting(t *testing.T) {
	plan := setups.PlanSnapshot{
		Status: "actionable", Direction: "bullish",
		Entry: quant.Price(10050), Stop: quant.Price(9900),
		Targets: []setups.TargetSnapshot{
			{Level: quant.Price(10200), RR: "1/1"},
			{Level: quant.Price(10400), RR: "3/2"},
			{Level: quant.Price(10700), RR: "4"},
		},
		POI: setups.POISnapshot{
			Type: "order_block", Direction: "bullish",
			Low: quant.Price(10000), High: quant.Price(10100),
		},
		ProtectedSwingID: 7,
	}

	atScale2 := toAPIPlanSnapshot(plan, quant.Scale(2))
	if atScale2.Entry != "100.50" || atScale2.Stop != "99.00" {
		t.Fatalf("scale 2: entry/stop = %q/%q, want \"100.50\"/\"99.00\"", atScale2.Entry, atScale2.Stop)
	}
	if atScale2.Poi.Low != "100.00" || atScale2.Poi.High != "101.00" {
		t.Fatalf("scale 2: poi low/high = %q/%q, want \"100.00\"/\"101.00\"", atScale2.Poi.Low, atScale2.Poi.High)
	}
	wantLevels := []string{"102.00", "104.00", "107.00"}
	wantRRs := []string{"1/1", "3/2", "4"}
	if len(atScale2.Targets) != 3 {
		t.Fatalf("scale 2: got %d targets, want 3", len(atScale2.Targets))
	}
	for i, target := range atScale2.Targets {
		if target.Level != wantLevels[i] || target.Rr != wantRRs[i] {
			t.Fatalf("scale 2: target[%d] = %+v, want level=%q rr=%q (rr untouched)", i, target, wantLevels[i], wantRRs[i])
		}
	}

	atScale0 := toAPIPlanSnapshot(plan, quant.Scale(0))
	if atScale0.Entry != "10050" || atScale0.Stop != "9900" {
		t.Fatalf("scale 0: entry/stop = %q/%q, want \"10050\"/\"9900\" (no fractional digits)", atScale0.Entry, atScale0.Stop)
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
}

func TestListSetups_OK(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, instruments: setupsTestInstruments(), logger: slog.Default()}
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

func TestListSetups_StateFilter(t *testing.T) {
	triggered := testSetup()
	triggered.ID, triggered.State = 2, setups.StateTriggered
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup(), triggered}}, instruments: setupsTestInstruments(), logger: slog.Default()}
	state := ListSetupsParamsState("triggered")
	resp, err := h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 42, State: &state}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitListSetupsResponse(rec); err != nil {
		t.Fatalf("VisitListSetupsResponse: %v", err)
	}
	var got []Setup
	decodeBody(t, rec, &got)
	if len(got) != 1 || got[0].Id != 2 {
		t.Fatalf("got %+v, want only the triggered setup", got)
	}
}

func TestListSetups_InvalidState(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, logger: slog.Default()}
	state := ListSetupsParamsState("not-a-real-state")
	resp, err := h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 42, State: &state}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	if _, ok := resp.(ListSetups400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400", resp)
	}
}

func TestListSetups_LimitOutOfRange(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, logger: slog.Default()}
	tooMany := int32(maxSetupsLimit + 1)
	resp, err := h.ListSetups(context.Background(), ListSetupsRequestObject{Params: ListSetupsParams{InstrumentId: 42, Limit: &tooMany}})
	if err != nil {
		t.Fatalf("ListSetups: %v", err)
	}
	if _, ok := resp.(ListSetups400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for limit=%d", resp, tooMany)
	}
}

func TestListSetups_Empty(t *testing.T) {
	h := &apiHandler{store: &fakeSetupsStore{setups: nil}, instruments: setupsTestInstruments(), logger: slog.Default()}
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
	r := NewRouter(withAuthTestDeps(Deps{Logger: slog.Default()}))
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/setups", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "argus_session", Value: authTestCookie})
	resp, err := http.DefaultClient.Do(req)
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
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}}, instruments: setupsTestInstruments(), logger: slog.Default()}
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
	h := &apiHandler{store: &fakeSetupsStore{setups: []setups.Setup{testSetup()}, events: []setups.SetupEvent{ev}}, instruments: setupsTestInstruments(), logger: slog.Default()}

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
