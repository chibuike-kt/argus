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

func (f *fakeInstrumentsStore) ListAll(_ context.Context) ([]instruments.Instrument, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.all, nil
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

func testInstrumentDomain(id int64) instruments.Instrument {
	return instruments.Instrument{
		ID: id, Provider: "test-provider", NativeSymbol: "BTC-PERP", AssetClass: "crypto_perp",
		PriceScale: 2, QtyScale: 4, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m"}, Enabled: false,
	}
}

func TestListInstruments_OK(t *testing.T) {
	h := &apiHandler{instruments: &fakeInstrumentsStore{all: []instruments.Instrument{testInstrumentDomain(1)}}, logger: slog.Default()}
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
		Id: 7, Params: ListInstrumentCandlesParams{From: from, To: to},
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
		Id: 404, Params: ListInstrumentCandlesParams{From: time.Now(), To: time.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles404JSONResponse); !ok {
		t.Fatalf("got %#v, want 404", resp)
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
		Id: 1, Params: ListInstrumentCandlesParams{From: time.Now(), To: time.Now().Add(time.Hour), Limit: &tooMany},
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
		Id: 1, Params: ListInstrumentCandlesParams{From: now, To: now},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for to == from", resp)
	}
}
