package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/engineview"
	"github.com/chibuike-kt/argus/internal/instruments"
)

// fakeEngineView is an engineViewStore with no database; it records what
// the handler asked for.
type fakeEngineView struct {
	window      engineview.Window
	status      []engineview.InstrumentStatus
	items       []engineview.ActivityItem
	next        *engineview.Cursor
	windowCalls int
	gotQuery    *engineview.ActivityQuery
	gotLimit    int32
}

func (f *fakeEngineView) StructureWindow(_ context.Context, _ int64, _ string, _, _ time.Time, limit int32) (engineview.Window, error) {
	f.windowCalls++
	f.gotLimit = limit
	return f.window, nil
}

func (f *fakeEngineView) EngineStatus(context.Context) ([]engineview.InstrumentStatus, error) {
	return f.status, nil
}

func (f *fakeEngineView) Activity(_ context.Context, q engineview.ActivityQuery) ([]engineview.ActivityItem, *engineview.Cursor, error) {
	f.gotQuery = &q
	return f.items, f.next, nil
}

func engineViewHandler(ev *fakeEngineView, insts ...instruments.Instrument) *apiHandler {
	byID := map[int64]instruments.Instrument{}
	for _, in := range insts {
		byID[in.ID] = in
	}
	return &apiHandler{engineView: ev, instruments: &fakeInstrumentsStore{all: insts, byID: byID}, logger: slog.Default()}
}

func wireJSON(t *testing.T, visit func(http.ResponseWriter) error) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := visit(rec); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func TestGetInstrumentStructure_Validation(t *testing.T) {
	from := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	ten := int32(9)
	big := int32(2001)
	cases := map[string]GetInstrumentStructureParams{
		"to before from":    {From: from, To: from.Add(-time.Minute)},
		"empty window":      {From: from, To: from},
		"more than 31 days": {From: from, To: from.Add(31*24*time.Hour + time.Minute)},
		"limit below min":   {From: from, To: from.Add(time.Hour), Limit: &ten},
		"limit above max":   {From: from, To: from.Add(time.Hour), Limit: &big},
		"unknown timeframe": {From: from, To: from.Add(time.Hour), Timeframe: ptr("2h")},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			ev := &fakeEngineView{}
			h := engineViewHandler(ev, enabledInstrumentDomain(1))
			resp, err := h.GetInstrumentStructure(context.Background(), GetInstrumentStructureRequestObject{Id: 1, Params: p})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := resp.(GetInstrumentStructure400JSONResponse); !ok {
				t.Fatalf("got %T, want 400", resp)
			}
			if ev.windowCalls != 0 {
				t.Fatal("store read despite an invalid request")
			}
		})
	}
}

func TestGetInstrumentStructure_DisabledOrMissingIs404(t *testing.T) {
	from := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	for _, id := range []int64{1, 2} {
		ev := &fakeEngineView{}
		h := engineViewHandler(ev, testInstrumentDomain(1)) // 1 disabled, 2 missing
		resp, err := h.GetInstrumentStructure(context.Background(), GetInstrumentStructureRequestObject{Id: id, Params: GetInstrumentStructureParams{From: from, To: from.Add(time.Hour)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := resp.(GetInstrumentStructure404JSONResponse); !ok || ev.windowCalls != 0 {
			t.Fatalf("id %d: got %T calls=%d, want 404 and no read", id, resp, ev.windowCalls)
		}
	}
}

func TestGetInstrumentStructure_RendersPricesAtScaleAndNextFrom(t *testing.T) {
	from := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lagos := time.FixedZone("WAT", 3600)
	next := from.Add(30 * time.Minute)
	ev := &fakeEngineView{window: engineview.Window{
		From: from, To: next, NextFrom: &next,
		Swings: []engineview.Swing{{ID: 1, Type: "high", PivotTime: from.In(lagos), Price: 703740, ConfirmedAt: from.Add(3 * time.Minute)}},
		Breaks: []engineview.Break{
			{ID: 2, EventType: "bos", DirectionBefore: "bearish", DirectionAfter: "bearish", TriggerOpenTime: from.Add(5 * time.Minute), BrokenSwing: &engineview.Level{SwingID: 1, Price: 703120, Time: from}},
			{ID: 3, EventType: "direction_established", DirectionBefore: "undetermined", DirectionAfter: "bullish", TriggerOpenTime: from.Add(6 * time.Minute)},
		},
		Sweeps: []engineview.Sweep{{ID: 4, Side: "buy", TriggerOpenTime: from.Add(9 * time.Minute), PenetrationExtreme: 703760, ReclaimClose: 703580, Swept: engineview.Level{SwingID: 1, Price: 703740, Time: from}}},
	}}
	h := engineViewHandler(ev, enabledInstrumentDomain(1))
	resp, err := h.GetInstrumentStructure(context.Background(), GetInstrumentStructureRequestObject{Id: 1, Params: GetInstrumentStructureParams{From: from, To: from.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.gotLimit != defaultStructureLimit {
		t.Fatalf("limit %d, want the default %d", ev.gotLimit, defaultStructureLimit)
	}
	code, body := wireJSON(t, resp.(GetInstrumentStructure200JSONResponse).VisitGetInstrumentStructureResponse)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	raw, _ := json.Marshal(body)
	s := string(raw)
	for _, want := range []string{`"price":"7037.40"`, `"pivot_time":"2026-11-21T00:00:00Z"`, `"next_from":"2026-11-21T00:30:00Z"`, `"penetration_extreme":"7037.60"`, `"broken_swing":{"price":"7031.20"`} {
		if !strings.Contains(s, want) {
			t.Errorf("body lacks %s: %s", want, s)
		}
	}
	breaks := body["breaks"].([]any)
	if _, has := breaks[1].(map[string]any)["broken_swing"]; has {
		t.Error("direction_established must have no broken_swing")
	}
}

func TestGetEngineStatus_BehindFromTheCursor(t *testing.T) {
	through := time.Date(2026, 11, 22, 1, 40, 0, 0, time.UTC)
	latest := through.Add(6 * time.Minute)
	evaluated := through.Add(28 * time.Second)
	now := time.Date(2026, 11, 22, 1, 47, 0, 0, time.UTC)
	ev := &fakeEngineView{status: []engineview.InstrumentStatus{
		{InstrumentID: 4, NativeSymbol: "BTC", Provider: "hyperliquid", Analysed: true, Timeframe: "1m", EvaluatedThrough: &through, LastEvaluatedAt: &evaluated, LatestClosedCloseTime: &latest, ClosedCandlesBehind: 6},
		{InstrumentID: 7, NativeSymbol: "DOGE", Provider: "hyperliquid", LatestClosedCloseTime: &latest},
	}}
	h := engineViewHandler(ev)
	h.clk = clock.NewFixed(now)
	resp, err := h.GetEngineStatus(context.Background(), GetEngineStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(GetEngineStatus200JSONResponse)
	if !got.AsOf.Equal(now) || len(got.Instruments) != 2 {
		t.Fatalf("got %+v", got)
	}
	btc := got.Instruments[0]
	if btc.BehindSeconds == nil || *btc.BehindSeconds != 360 || btc.ClosedCandlesBehind != 6 || *btc.Timeframe != "1m" {
		t.Fatalf("btc = %+v, want 360s and 6 candles behind", btc)
	}
	doge := got.Instruments[1]
	if doge.Analysed || doge.BehindSeconds != nil || doge.EvaluatedThrough != nil || doge.Timeframe != nil {
		t.Fatalf("never analysed = %+v, want no figures", doge)
	}
	// Null fields are sent as null, not left out (the client tells "not
	// analysed" from "missing field" by them).
	_, body := wireJSON(t, resp.VisitGetEngineStatusResponse)
	row := body["instruments"].([]any)[1].(map[string]any)
	if v, ok := row["behind_seconds"]; !ok || v != nil {
		t.Fatalf("behind_seconds = %v present=%v, want explicit null", v, ok)
	}
}

func TestListActivity_KindsCursorAndEnabledOnly(t *testing.T) {
	disabled := testInstrumentDomain(3)
	ev := &fakeEngineView{}
	h := engineViewHandler(ev, enabledInstrumentDomain(1), enabledInstrumentDomain(2), disabled)

	// Named ids are intersected with the enabled list: 3 is disabled, 9
	// does not exist, 1 is named twice.
	ids := []int64{1, 3, 9, 1}
	kinds := []ListActivityParamsKind{ListActivityParamsKindSetup, ListActivityParamsKindGap}
	c := engineview.Cursor{At: time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC), Kind: "setup", ID: 5}
	cur := c.Encode()
	if _, err := h.ListActivity(context.Background(), ListActivityRequestObject{Params: ListActivityParams{InstrumentId: &ids, Kind: &kinds, Cursor: &cur}}); err != nil {
		t.Fatal(err)
	}
	q := ev.gotQuery
	if q == nil || len(q.InstrumentIDs) != 1 || q.InstrumentIDs[0] != 1 {
		t.Fatalf("instrument ids = %+v, want only the enabled [1]", q)
	}
	if !q.Setups || q.Structure || !q.Gaps || q.Limit != defaultActivityLimit {
		t.Fatalf("query = %+v, want setups and gaps, default limit", q)
	}
	if q.Before == nil || !q.Before.At.Equal(c.At) || q.Before.ID != 5 {
		t.Fatalf("cursor = %+v, want the decoded one", q.Before)
	}

	// No ids: every enabled instrument, all kinds.
	ev.gotQuery = nil
	if _, err := h.ListActivity(context.Background(), ListActivityRequestObject{}); err != nil {
		t.Fatal(err)
	}
	if q := ev.gotQuery; len(q.InstrumentIDs) != 2 || !q.Setups || !q.Structure || !q.Gaps {
		t.Fatalf("default query = %+v", q)
	}

	// Only disabled ids: an empty page, no read.
	ev.gotQuery = nil
	only := []int64{3}
	resp, err := h.ListActivity(context.Background(), ListActivityRequestObject{Params: ListActivityParams{InstrumentId: &only}})
	if err != nil {
		t.Fatal(err)
	}
	if page := resp.(ListActivity200JSONResponse); len(page.Items) != 0 || page.NextCursor != nil || ev.gotQuery != nil {
		t.Fatalf("disabled-only = %+v, read=%v", page, ev.gotQuery != nil)
	}
}

func TestListActivity_Rejects(t *testing.T) {
	bad := "not-a-cursor"
	zero := int32(0)
	many := make([]int64, 51)
	for i := range many {
		many[i] = int64(i + 1)
	}
	neg := []int64{-1}
	cases := map[string]ListActivityParams{
		"bad cursor":     {Cursor: &bad},
		"zero limit":     {Limit: &zero},
		"51 instruments": {InstrumentId: &many},
		"negative id":    {InstrumentId: &neg},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			ev := &fakeEngineView{}
			h := engineViewHandler(ev, enabledInstrumentDomain(1))
			resp, err := h.ListActivity(context.Background(), ListActivityRequestObject{Params: p})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := resp.(ListActivity400JSONResponse); !ok || ev.gotQuery != nil {
				t.Fatalf("got %T read=%v, want 400 and no read", resp, ev.gotQuery != nil)
			}
		})
	}
}

func TestListActivity_RendersItemsAndNextCursor(t *testing.T) {
	setupID := int64(274)
	gapEnd := time.Date(2026, 11, 18, 9, 22, 0, 0, time.UTC)
	ev := &fakeEngineView{
		items: []engineview.ActivityItem{
			{Kind: "setup", ID: 900, InstrumentID: 1, Timeframe: "1m", At: time.Date(2026, 11, 22, 1, 36, 20, 0, time.UTC), EventTime: time.Date(2026, 11, 22, 0, 20, 0, 0, time.UTC), Detail: "invalidated", SetupID: &setupID, Direction: "bearish", POIType: "order_block", FoundMode: "replay"},
			{Kind: "gap", ID: 5, InstrumentID: 1, Timeframe: "1m", At: gapEnd, EventTime: gapEnd.Add(-3 * time.Minute), Detail: "backfilled", GapEnd: &gapEnd},
		},
		next: &engineview.Cursor{At: gapEnd, Kind: "gap", ID: 5},
	}
	h := engineViewHandler(ev, enabledInstrumentDomain(1))
	resp, err := h.ListActivity(context.Background(), ListActivityRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	page := resp.(ListActivity200JSONResponse)
	if len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("page = %+v", page)
	}
	if c, err := engineview.DecodeCursor(*page.NextCursor); err != nil || c.Kind != "gap" || c.ID != 5 {
		t.Fatalf("next cursor %q decodes to %+v err %v", *page.NextCursor, c, err)
	}
	s, g := page.Items[0], page.Items[1]
	if *s.SetupId != 274 || *s.FoundMode != "replay" || *s.Direction != "bearish" || s.Detail != "invalidated" {
		t.Fatalf("setup item = %+v", s)
	}
	if g.SetupId != nil || g.Direction != nil || g.FoundMode != nil || g.GapEnd == nil || g.Detail != "backfilled" {
		t.Fatalf("gap item = %+v, want no setup fields and its end", g)
	}
}

func ptr[T any](v T) *T { return &v }
