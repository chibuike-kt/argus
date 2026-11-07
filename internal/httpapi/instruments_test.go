package httpapi

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// fakeInstrumentsStore/fakeCandlesStore are instruments_test.go's own
// handwritten fakes -- the same reason fakeSetupsStore exists in
// setups_test.go (guide 10): routing/validation logic exercised
// without a database.
type fakeInstrumentsStore struct {
	all        []instruments.Instrument
	byID       map[int64]instruments.Instrument
	listErr    error
	getByIDErr error
}

func (f *fakeInstrumentsStore) ListAllFiltered(_ context.Context, afterID *int64, enabled *bool, limit int32) ([]instruments.Instrument, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := f.all
	if afterID != nil {
		filtered := make([]instruments.Instrument, 0, len(out))
		for _, inst := range out {
			if inst.ID > *afterID {
				filtered = append(filtered, inst)
			}
		}
		out = filtered
	}
	if enabled != nil {
		filtered := make([]instruments.Instrument, 0, len(out))
		for _, inst := range out {
			if inst.Enabled == *enabled {
				filtered = append(filtered, inst)
			}
		}
		out = filtered
	}
	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeInstrumentsStore) GetByID(_ context.Context, id int64) (instruments.Instrument, error) {
	if f.getByIDErr != nil {
		return instruments.Instrument{}, f.getByIDErr
	}
	inst, ok := f.byID[id]
	if !ok {
		return instruments.Instrument{}, instruments.ErrNotFound
	}
	return inst, nil
}

type fakeCandlesStore struct {
	rows []marketdata.Candle
	err  error
}

func (f *fakeCandlesStore) ListClosedInRangeLimited(_ context.Context, _ int64, _, _ time.Time, _ int32) ([]marketdata.Candle, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func (f *fakeCandlesStore) ListClosedInRangeLimitedDesc(_ context.Context, _ int64, _, _ *time.Time, _ int32) ([]marketdata.Candle, error) {
	if f.err != nil {
		return nil, f.err
	}
	reversed := make([]marketdata.Candle, len(f.rows))
	for i, c := range f.rows {
		reversed[len(f.rows)-1-i] = c
	}
	return reversed, nil
}

func timePtr(t time.Time) *time.Time { return &t }

func testInstrumentDomain(id int64) instruments.Instrument {
	return instruments.Instrument{
		ID: id, Provider: "test-provider", NativeSymbol: "BTC-PERP", AssetClass: "crypto_perp",
		PriceScale: 2, QtyScale: 4, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m"}, Enabled: false,
	}
}

// enabledInstrumentDomain is testInstrumentDomain's own sibling for
// tests that are not about enablement itself but need a row the new
// default enabled=true filter (web-wiring-polish brief, item 2) will
// actually return.
func enabledInstrumentDomain(id int64) instruments.Instrument {
	inst := testInstrumentDomain(id)
	inst.Enabled = true
	return inst
}

func TestListInstruments_OK(t *testing.T) {
	h := &apiHandler{instruments: &fakeInstrumentsStore{all: []instruments.Instrument{enabledInstrumentDomain(1)}}, logger: slog.Default()}
	resp, err := h.ListInstruments(context.Background(), ListInstrumentsRequestObject{})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	got, ok := resp.(ListInstruments200JSONResponse)
	if !ok || len(got) != 1 {
		t.Fatalf("got %#v, want one instrument", resp)
	}
	if got[0].Id != 1 || got[0].TickSize != "0.01" {
		t.Fatalf("got %+v, want id=1 tick_size=0.01", got[0])
	}
}

func TestListInstruments_AfterIDAndLimit(t *testing.T) {
	all := []instruments.Instrument{enabledInstrumentDomain(1), enabledInstrumentDomain(2), enabledInstrumentDomain(3)}
	h := &apiHandler{instruments: &fakeInstrumentsStore{all: all}, logger: slog.Default()}

	afterID := int64(1)
	limit := int32(1)
	resp, err := h.ListInstruments(context.Background(), ListInstrumentsRequestObject{
		Params: ListInstrumentsParams{AfterId: &afterID, Limit: &limit},
	})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	got, ok := resp.(ListInstruments200JSONResponse)
	if !ok || len(got) != 1 || got[0].Id != 2 {
		t.Fatalf("got %#v, want one instrument with id=2", resp)
	}
}

// TestListInstruments_EnabledDefaultsToTrue is the web-wiring-polish
// brief's own required proof for item 2's default: calling the
// endpoint with no ?enabled= query param at all must pass enabled=true
// to the store, not nil/"no filter" -- the bug being fixed is exactly a
// disabled row showing up on an unfiltered call.
func TestListInstruments_EnabledDefaultsToTrue(t *testing.T) {
	disabled := testInstrumentDomain(1)
	enabled := enabledInstrumentDomain(2)
	h := &apiHandler{instruments: &fakeInstrumentsStore{all: []instruments.Instrument{disabled, enabled}}, logger: slog.Default()}

	resp, err := h.ListInstruments(context.Background(), ListInstrumentsRequestObject{})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	got, ok := resp.(ListInstruments200JSONResponse)
	if !ok || len(got) != 1 || got[0].Id != 2 {
		t.Fatalf("got %#v, want only the enabled instrument (id=2)", resp)
	}
}

func TestListInstruments_EnabledFalseAndAll(t *testing.T) {
	disabled := testInstrumentDomain(1)
	enabled := enabledInstrumentDomain(2)
	h := &apiHandler{instruments: &fakeInstrumentsStore{all: []instruments.Instrument{disabled, enabled}}, logger: slog.Default()}

	falseStr := False
	resp, err := h.ListInstruments(context.Background(), ListInstrumentsRequestObject{
		Params: ListInstrumentsParams{Enabled: &falseStr},
	})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	got, ok := resp.(ListInstruments200JSONResponse)
	if !ok || len(got) != 1 || got[0].Id != 1 {
		t.Fatalf("enabled=false: got %#v, want only the disabled instrument (id=1)", resp)
	}

	allStr := All
	resp, err = h.ListInstruments(context.Background(), ListInstrumentsRequestObject{
		Params: ListInstrumentsParams{Enabled: &allStr},
	})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	got, ok = resp.(ListInstruments200JSONResponse)
	if !ok || len(got) != 2 {
		t.Fatalf("enabled=all: got %#v, want both instruments", resp)
	}
}

func TestListInstruments_EnabledBadValue(t *testing.T) {
	h := &apiHandler{instruments: &fakeInstrumentsStore{all: []instruments.Instrument{enabledInstrumentDomain(1)}}, logger: slog.Default()}
	bad := ListInstrumentsParamsEnabled("maybe")
	resp, err := h.ListInstruments(context.Background(), ListInstrumentsRequestObject{
		Params: ListInstrumentsParams{Enabled: &bad},
	})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	if _, ok := resp.(ListInstruments400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for enabled=%q", resp, bad)
	}
}

func TestListInstruments_LimitOutOfRange(t *testing.T) {
	h := &apiHandler{instruments: &fakeInstrumentsStore{all: []instruments.Instrument{testInstrumentDomain(1)}}, logger: slog.Default()}
	tooMany := int32(maxInstrumentsLimit + 1)
	resp, err := h.ListInstruments(context.Background(), ListInstrumentsRequestObject{
		Params: ListInstrumentsParams{Limit: &tooMany},
	})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	if _, ok := resp.(ListInstruments400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for limit=%d", resp, tooMany)
	}
}

func TestListInstrumentCandles_OK(t *testing.T) {
	inst := testInstrumentDomain(7)
	candle := marketdata.Candle{
		OpenTime:  clock.NewEventTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		CloseTime: clock.NewEventTime(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)),
		Open:      quant.Price(10050), High: quant.Price(10100), Low: quant.Price(10000), Close: quant.Price(10075),
		Volume: quant.Qty(15000), IsClosed: true, Revision: 1,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{7: inst}},
		candles:     &fakeCandlesStore{rows: []marketdata.Candle{candle}},
		logger:      slog.Default(),
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 7, Params: ListInstrumentCandlesParams{From: &from, To: &to},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok || len(got) != 1 {
		t.Fatalf("got %#v, want one candle", resp)
	}
	if got[0].Open != "100.50" || got[0].Volume != "1.5000" {
		t.Fatalf("got %+v, want decimal-string OHLCV scaled from the instrument", got[0])
	}
}

func TestListInstrumentCandles_InstrumentNotFound(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 404, Params: ListInstrumentCandlesParams{From: timePtr(time.Now()), To: timePtr(time.Now().Add(time.Hour))},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles404JSONResponse); !ok {
		t.Fatalf("got %#v, want 404", resp)
	}
}

func TestListInstrumentCandles_OrderDescUsesDescStore(t *testing.T) {
	inst := testInstrumentDomain(7)
	first := marketdata.Candle{
		OpenTime: clock.NewEventTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Open:     quant.Price(100), High: quant.Price(100), Low: quant.Price(100), Close: quant.Price(100),
		Volume: quant.Qty(1), IsClosed: true, Revision: 1,
	}
	second := marketdata.Candle{
		OpenTime: clock.NewEventTime(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)),
		Open:     quant.Price(200), High: quant.Price(200), Low: quant.Price(200), Close: quant.Price(200),
		Volume: quant.Qty(2), IsClosed: true, Revision: 1,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{7: inst}},
		candles:     &fakeCandlesStore{rows: []marketdata.Candle{first, second}},
		logger:      slog.Default(),
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	desc := Desc
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 7, Params: ListInstrumentCandlesParams{From: &from, To: &to, Order: &desc},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok || len(got) != 2 {
		t.Fatalf("got %#v, want two candles", resp)
	}
	if got[0].Open != "2.00" || got[1].Open != "1.00" {
		t.Fatalf("got opens [%s, %s], want [2.00, 1.00] (newest first, from the fake's own Desc method)", got[0].Open, got[1].Open)
	}
}

func TestListInstrumentCandles_OrderAscIsUnchangedDefault(t *testing.T) {
	inst := testInstrumentDomain(7)
	candle := marketdata.Candle{
		OpenTime: clock.NewEventTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Open:     quant.Price(100), High: quant.Price(100), Low: quant.Price(100), Close: quant.Price(100),
		Volume: quant.Qty(1), IsClosed: true, Revision: 1,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{7: inst}},
		candles:     &fakeCandlesStore{rows: []marketdata.Candle{candle}},
		logger:      slog.Default(),
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	asc := Asc
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 7, Params: ListInstrumentCandlesParams{From: &from, To: &to, Order: &asc},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok || len(got) != 1 || got[0].Open != "1.00" {
		t.Fatalf("got %#v, want the ascending fake's own one row, unchanged", resp)
	}
}

func TestListInstrumentCandles_OrderBadValue(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	bad := ListInstrumentCandlesParamsOrder("sideways")
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{From: timePtr(time.Now()), To: timePtr(time.Now().Add(time.Hour)), Order: &bad},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for order=%q", resp, bad)
	}
}

func TestListInstrumentCandles_LimitOutOfRange(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	tooMany := int32(maxInstrumentCandles + 1)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{From: timePtr(time.Now()), To: timePtr(time.Now().Add(time.Hour)), Limit: &tooMany},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for limit=%d", resp, tooMany)
	}
}

func TestListInstrumentCandles_ToNotAfterFrom(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	now := time.Now()
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{From: &now, To: &now},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for to == from", resp)
	}
}

// TestListInstrumentCandles_OrderDescNoFromReturnsNewestFirst is the
// web-wiring-polish brief's own required proof for item 1: order=desc
// is "give me the latest N candles," which should not need a [from, to)
// range at all. Neither From nor To is set here.
func TestListInstrumentCandles_OrderDescNoFromReturnsNewestFirst(t *testing.T) {
	inst := testInstrumentDomain(7)
	older := marketdata.Candle{
		OpenTime: clock.NewEventTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Open:     quant.Price(100), High: quant.Price(100), Low: quant.Price(100), Close: quant.Price(100),
		Volume: quant.Qty(1), IsClosed: true, Revision: 1,
	}
	newer := marketdata.Candle{
		OpenTime: clock.NewEventTime(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)),
		Open:     quant.Price(200), High: quant.Price(200), Low: quant.Price(200), Close: quant.Price(200),
		Volume: quant.Qty(2), IsClosed: true, Revision: 1,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{7: inst}},
		candles:     &fakeCandlesStore{rows: []marketdata.Candle{older, newer}},
		logger:      slog.Default(),
	}
	limit := int32(2)
	desc := Desc
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 7, Params: ListInstrumentCandlesParams{Order: &desc, Limit: &limit},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok || len(got) != 2 {
		t.Fatalf("got %#v, want two candles", resp)
	}
	if got[0].Open != "2.00" || got[1].Open != "1.00" {
		t.Fatalf("got opens [%s, %s], want [2.00, 1.00] (newest first, no from/to needed)", got[0].Open, got[1].Open)
	}
}

// TestListInstrumentCandles_OrderAscNoFromStillRejected proves asc (the
// default) keeps the original contract: no from (or to) is still a 400,
// exactly as before order=desc existed.
func TestListInstrumentCandles_OrderAscNoFromStillRejected(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles400JSONResponse)
	if !ok {
		t.Fatalf("got %#v, want 400 for asc with no from", resp)
	}
	if got.Error.Message != "Query argument from is required, but not found" {
		t.Fatalf("message = %q, want the unchanged required-param message", got.Error.Message)
	}
}
