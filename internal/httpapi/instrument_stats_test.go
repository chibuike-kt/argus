package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

type fakeStatsStore struct {
	byID  map[int64]marketdata.Stats
	err   error
	calls []int64
}

func (f *fakeStatsStore) Stats(_ context.Context, id int64) (marketdata.Stats, error) {
	f.calls = append(f.calls, id)
	if f.err != nil {
		return marketdata.Stats{}, f.err
	}
	return f.byID[id], nil
}

func pct(v int64) *marketdata.Percent { p := marketdata.Percent(v); return &p }

func fullStats(id int64) marketdata.Stats {
	vol := quant.Qty(15000)
	return marketdata.Stats{
		InstrumentID: id,
		Latest: &marketdata.LatestClose{
			Close:    10050,
			OpenTime: clock.NewEventTime(time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)),
		},
		Change24h: &marketdata.Change{Abs: -125, Pct: pct(-123)},
		Volume24h: &vol,
		Performance: map[string]*marketdata.Percent{
			"1D": pct(-123), "1W": nil, "1M": nil, "3M": nil, "6M": nil, "1Y": nil,
		},
	}
}

func statsHandler(inst map[int64]instruments.Instrument, st *fakeStatsStore) *apiHandler {
	return &apiHandler{
		instruments: &fakeInstrumentsStore{byID: inst},
		stats:       st,
		logger:      slog.Default(),
	}
}

func TestListInstrumentStats_RendersAtInstrumentScales(t *testing.T) {
	st := &fakeStatsStore{byID: map[int64]marketdata.Stats{7: fullStats(7)}}
	h := statsHandler(map[int64]instruments.Instrument{7: enabledInstrumentDomain(7)}, st)

	resp, err := h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{Params: ListInstrumentStatsParams{Ids: []int64{7}}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.(ListInstrumentStats200JSONResponse)
	if !ok || len(got) != 1 {
		t.Fatalf("got %#v, want one row", resp)
	}
	row := got[0]
	// price scale 2, qty scale 4 (testInstrumentDomain).
	if *row.LastClose != "100.50" || *row.Change24h != "-1.25" || *row.Change24hPct != "-1.23" || *row.Volume24h != "1.5000" {
		t.Fatalf("row = last %v chg %v pct %v vol %v", *row.LastClose, *row.Change24h, *row.Change24hPct, *row.Volume24h)
	}
	if !row.LastClosedOpenTime.Equal(time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("last_closed_open_time = %v", row.LastClosedOpenTime)
	}
	if *row.Performance.N1D != "-1.23" || row.Performance.N1W != nil {
		t.Fatalf("performance = %+v", row.Performance)
	}
}

// Absent values must cross the wire as explicit JSON null -- never
// omitted, never "0".
func TestListInstrumentStats_AbsentIsExplicitNull(t *testing.T) {
	st := &fakeStatsStore{byID: map[int64]marketdata.Stats{
		3: {InstrumentID: 3, Performance: map[string]*marketdata.Percent{}},
	}}
	h := statsHandler(map[int64]instruments.Instrument{3: enabledInstrumentDomain(3)}, st)
	resp, err := h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{Params: ListInstrumentStatsParams{Ids: []int64{3}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitListInstrumentStatsResponse(rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, key := range []string{`"last_close":null`, `"last_closed_open_time":null`, `"change_24h":null`, `"change_24h_pct":null`, `"volume_24h":null`, `"1D":null`, `"1Y":null`} {
		if !strings.Contains(body, key) {
			t.Errorf("body missing %s: %s", key, body)
		}
	}
	var raw []map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&raw); err != nil {
		t.Fatal(err)
	}
}

func TestListInstrumentStats_UnknownAndDisabledOmitted(t *testing.T) {
	st := &fakeStatsStore{byID: map[int64]marketdata.Stats{1: fullStats(1), 2: fullStats(2)}}
	h := statsHandler(map[int64]instruments.Instrument{
		1: enabledInstrumentDomain(1),
		2: testInstrumentDomain(2), // disabled
	}, st)
	resp, err := h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{Params: ListInstrumentStatsParams{Ids: []int64{2, 999, 1}}})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(ListInstrumentStats200JSONResponse)
	if len(got) != 1 || got[0].InstrumentId != 1 {
		t.Fatalf("got %+v, want only instrument 1", got)
	}
	// The disabled instrument's stats are never even computed.
	for _, id := range st.calls {
		if id == 2 || id == 999 {
			t.Fatalf("Stats called for %d", id)
		}
	}
}

func TestListInstrumentStats_DuplicatesOnceInOrder(t *testing.T) {
	st := &fakeStatsStore{byID: map[int64]marketdata.Stats{1: fullStats(1), 2: fullStats(2)}}
	h := statsHandler(map[int64]instruments.Instrument{1: enabledInstrumentDomain(1), 2: enabledInstrumentDomain(2)}, st)
	resp, _ := h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{Params: ListInstrumentStatsParams{Ids: []int64{2, 1, 2, 1}}})
	got := resp.(ListInstrumentStats200JSONResponse)
	if len(got) != 2 || got[0].InstrumentId != 2 || got[1].InstrumentId != 1 {
		t.Fatalf("got %+v, want [2, 1]", got)
	}
}

func TestListInstrumentStats_LimitAndEmpty(t *testing.T) {
	h := statsHandler(map[int64]instruments.Instrument{}, &fakeStatsStore{})

	ids := make([]int64, 51)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	resp, _ := h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{Params: ListInstrumentStatsParams{Ids: ids}})
	if _, ok := resp.(ListInstrumentStats400JSONResponse); !ok {
		t.Fatalf("51 ids: got %T, want 400", resp)
	}

	resp, _ = h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{Params: ListInstrumentStatsParams{Ids: ids[:50]}})
	if _, ok := resp.(ListInstrumentStats200JSONResponse); !ok {
		t.Fatalf("50 ids: got %T, want 200", resp)
	}

	resp, _ = h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{})
	if _, ok := resp.(ListInstrumentStats400JSONResponse); !ok {
		t.Fatalf("no ids: got %T, want 400", resp)
	}
}

func TestListInstrumentStats_StoreErrorIsInternal(t *testing.T) {
	h := statsHandler(map[int64]instruments.Instrument{1: enabledInstrumentDomain(1)}, &fakeStatsStore{err: errors.New("boom")})
	if _, err := h.ListInstrumentStats(context.Background(), ListInstrumentStatsRequestObject{Params: ListInstrumentStatsParams{Ids: []int64{1}}}); err == nil {
		t.Fatal("want error")
	}
}

// Every approved period must have a wire field: a new period added to
// marketdata without one here would be silently dropped.
func TestToAPIInstrumentStats_CoversEveryPeriod(t *testing.T) {
	perf := map[string]*marketdata.Percent{}
	for i, p := range marketdata.PerformancePeriods() {
		perf[p.Key] = pct(int64(i + 1))
	}
	out := toAPIInstrumentStats(marketdata.Stats{Performance: perf}, 2, 2)
	raw, _ := json.Marshal(out.Performance)
	var m map[string]*string
	_ = json.Unmarshal(raw, &m)
	for _, p := range marketdata.PerformancePeriods() {
		if m[p.Key] == nil {
			t.Errorf("period %s not rendered: %s", p.Key, raw)
		}
	}
}
