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

	aggregates   []marketdata.AggregateOHLCV
	aggregateErr error
	joinPoint    *marketdata.AggregateOHLCV
	joinPointErr error
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

func (f *fakeCandlesStore) AggregateRange(_ context.Context, _ int64, _ []marketdata.Window) ([]marketdata.AggregateOHLCV, error) {
	if f.aggregateErr != nil {
		return nil, f.aggregateErr
	}
	return f.aggregates, nil
}

func (f *fakeCandlesStore) LatestCompleteWindow(_ context.Context, _ int64, _ time.Duration) (*marketdata.AggregateOHLCV, error) {
	if f.joinPointErr != nil {
		return nil, f.joinPointErr
	}
	return f.joinPoint, nil
}

func timePtr(t time.Time) *time.Time { return &t }

// testInstrumentDomain's own SupportedIntervals lists all six (matching
// db/seed/dev_fixture_instruments.sql's own real default, owner
// approved 12 November 2026): most tests below are not about the
// interval gate itself, and the gate reads this list as its single
// source of truth (internal/httpapi/instruments.go), not AssetClass --
// a test that cares specifically about the gate sets its own narrower
// list instead (see TestListInstrumentCandles_IntervalNotInSupportedIntervalsIs400).
func testInstrumentDomain(id int64) instruments.Instrument {
	return instruments.Instrument{
		ID: id, Provider: "test-provider", NativeSymbol: "BTC-PERP", AssetClass: "crypto_perp",
		PriceScale: 2, QtyScale: 4, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m", "5m", "15m", "1h", "4h", "1d"}, Enabled: false,
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
	if !ok || len(got.Candles) != 1 {
		t.Fatalf("got %#v, want one candle", resp)
	}
	if got.Candles[0].Open != "100.50" || got.Candles[0].Volume != "1.5000" {
		t.Fatalf("got %+v, want decimal-string OHLCV scaled from the instrument", got.Candles[0])
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
	if !ok || len(got.Candles) != 2 {
		t.Fatalf("got %#v, want two candles", resp)
	}
	if got.Candles[0].Open != "2.00" || got.Candles[1].Open != "1.00" {
		t.Fatalf("got opens [%s, %s], want [2.00, 1.00] (newest first, from the fake's own Desc method)", got.Candles[0].Open, got.Candles[1].Open)
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
	if !ok || len(got.Candles) != 1 || got.Candles[0].Open != "1.00" {
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
	if !ok || len(got.Candles) != 2 {
		t.Fatalf("got %#v, want two candles", resp)
	}
	if got.Candles[0].Open != "2.00" || got.Candles[1].Open != "1.00" {
		t.Fatalf("got opens [%s, %s], want [2.00, 1.00] (newest first, no from/to needed)", got.Candles[0].Open, got.Candles[1].Open)
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

// --- live-chart-pipeline brief, Phase 1: interval ---

func TestListInstrumentCandles_UnknownIntervalIs400(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	bad := ListInstrumentCandlesParamsInterval("2m")
	from, to := time.Now(), time.Now().Add(time.Hour)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &bad, From: &from, To: &to},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400 for interval=%q", resp, bad)
	}
}

// TestListInstrumentCandles_IntervalNotInSupportedIntervalsIs400 is the
// gate itself (owner approved 12 November 2026, superseding the earlier
// asset_class-based check): an interval absent from the instrument's
// own supported_intervals is a 400, regardless of asset_class -- this
// instrument is "crypto_perp" (the asset class that used to be
// sufficient on its own) and still gets refused, because 5m is not in
// its own list. A forex instrument reaches the exact same 400 the same
// way, simply because its own row has never listed anything above 1m
// (ADR-007/calendar-aware sessions are still not built) -- there is no
// separate asset-class branch left to test.
func TestListInstrumentCandles_IntervalNotInSupportedIntervalsIs400(t *testing.T) {
	inst := testInstrumentDomain(1)
	inst.SupportedIntervals = []string{"1m"}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: inst}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	interval := N5m
	from, to := time.Now(), time.Now().Add(time.Hour)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &interval, From: &from, To: &to},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles400JSONResponse)
	if !ok {
		t.Fatalf("got %#v, want 400 for an interval absent from supported_intervals", resp)
	}
	if got.Error.Code != "invalid_argument" {
		t.Errorf("Error.Code = %q, want invalid_argument", got.Error.Code)
	}
}

// TestListInstrumentCandles_IntervalInSupportedIntervalsUsesAggregateRange
// is the other half of the same gate: an interval that IS in the
// instrument's own supported_intervals is served normally, through the
// aggregation path -- same instrument shape as the 400 case above,
// differing only in which intervals its own row lists.
func TestListInstrumentCandles_IntervalInSupportedIntervalsUsesAggregateRange(t *testing.T) {
	inst := testInstrumentDomain(1) // SupportedIntervals includes 5m
	agg := marketdata.AggregateOHLCV{
		Window: marketdata.Window{
			Start: clock.NewEventTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			End:   clock.NewEventTime(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)),
		},
		Open: 10000, High: 10200, Low: 9900, Close: 10100, Volume: 500, Revision: 1,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: inst}},
		candles:     &fakeCandlesStore{aggregates: []marketdata.AggregateOHLCV{agg}, joinPoint: &agg},
		logger:      slog.Default(),
	}
	interval := N5m
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &interval, From: &from, To: &to},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok || len(got.Candles) != 1 {
		t.Fatalf("got %#v, want one aggregated candle", resp)
	}
	c := got.Candles[0]
	if c.Open != "100.00" || c.High != "102.00" || c.Low != "99.00" || c.Close != "101.00" || c.Volume != "0.0500" {
		t.Errorf("c = %+v, want the AggregateOHLCV scaled by the instrument", c)
	}
	if !c.IsClosed {
		t.Errorf("IsClosed = false, want true for an aggregate (only complete windows are ever returned)")
	}
	if c.Revision != 1 {
		t.Errorf("Revision = %d, want 1 (the fake's own AggregateOHLCV.Revision, carried through unchanged)", c.Revision)
	}
}

// TestListInstrumentCandles_NonCryptoWithSupportedIntervalIsServed is
// the proof there is no leftover asset_class special case anywhere in
// the gate: a "forex_spot" instrument whose own row lists 5m (something
// no real forex row does today, since calendar-aware sessions are not
// built -- but the gate itself must not know or care why a row lists
// what it lists) is served normally, exactly like a crypto one would
// be. If asset_class were still checked anywhere, this would wrongly
// 400.
func TestListInstrumentCandles_NonCryptoWithSupportedIntervalIsServed(t *testing.T) {
	inst := testInstrumentDomain(1)
	inst.AssetClass = "forex_spot"
	inst.SupportedIntervals = []string{"1m", "5m"}
	agg := marketdata.AggregateOHLCV{
		Window: marketdata.Window{
			Start: clock.NewEventTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			End:   clock.NewEventTime(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)),
		},
		Open: 10000, High: 10200, Low: 9900, Close: 10100, Volume: 500, Revision: 1,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: inst}},
		candles:     &fakeCandlesStore{aggregates: []marketdata.AggregateOHLCV{agg}, joinPoint: &agg},
		logger:      slog.Default(),
	}
	interval := N5m
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &interval, From: &from, To: &to},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles200JSONResponse); !ok {
		t.Fatalf("got %#v, want 200: a non-crypto instrument whose own supported_intervals lists 5m is served, asset_class is not checked", resp)
	}
}

func TestListInstrumentCandles_AboveOneMinuteAscStillRequiresFromAndTo(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	interval := N5m
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &interval}, // order omitted -> asc
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400: above 1m, asc still requires from/to", resp)
	}
}

// TestListInstrumentCandles_AboveOneMinuteDescNoFromToIsLatestN proves
// addendum item C: order=desc above 1m, with neither from nor to, is
// now the same "latest N" shortcut 1m already had -- anchored at the
// per-interval join point (review item 5), not an error.
func TestListInstrumentCandles_AboveOneMinuteDescNoFromToIsLatestN(t *testing.T) {
	inst := testInstrumentDomain(1)
	joinPoint := marketdata.AggregateOHLCV{
		Window: marketdata.Window{
			Start: clock.NewEventTime(time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)),
			End:   clock.NewEventTime(time.Date(2026, 1, 1, 1, 5, 0, 0, time.UTC)),
		},
		Open: 10000, High: 10200, Low: 9900, Close: 10100, Volume: 500, Revision: 1,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: inst}},
		candles:     &fakeCandlesStore{joinPoint: &joinPoint, aggregates: []marketdata.AggregateOHLCV{joinPoint}},
		logger:      slog.Default(),
	}
	interval := N5m
	desc := Desc
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &interval, Order: &desc},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok {
		t.Fatalf("got %#v, want 200 (latest-N, not an error)", resp)
	}
	if len(got.Candles) != 1 {
		t.Fatalf("got %d candles, want 1", len(got.Candles))
	}
	if got.LastClosedOpenTime == nil || !got.LastClosedOpenTime.Equal(joinPoint.Window.Start.Time()) {
		t.Errorf("LastClosedOpenTime = %v, want %s", got.LastClosedOpenTime, joinPoint.Window.Start.Time())
	}
}

// TestListInstrumentCandles_AboveOneMinuteDescNoFromTo_NoJoinPointYet
// proves the "nothing yet" case is zero candles, not an error.
func TestListInstrumentCandles_AboveOneMinuteDescNoFromTo_NoJoinPointYet(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{joinPoint: nil},
		logger:      slog.Default(),
	}
	interval := N5m
	desc := Desc
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &interval, Order: &desc},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok {
		t.Fatalf("got %#v, want 200 with zero candles, not an error", resp)
	}
	if len(got.Candles) != 0 {
		t.Errorf("got %d candles, want 0", len(got.Candles))
	}
}

func TestListInstrumentCandles_LastClosedJoinPoint(t *testing.T) {
	inst := testInstrumentDomain(1)
	latest := marketdata.AggregateOHLCV{
		Window: marketdata.Window{
			Start: clock.NewEventTime(time.Date(2026, 1, 1, 0, 9, 0, 0, time.UTC)),
			End:   clock.NewEventTime(time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)),
		},
		Revision: 3,
	}
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: inst}},
		candles:     &fakeCandlesStore{rows: nil, joinPoint: &latest},
		logger:      slog.Default(),
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{From: &from, To: &to},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok {
		t.Fatalf("got %#v, want 200", resp)
	}
	if got.LastClosedOpenTime == nil || !got.LastClosedOpenTime.Equal(latest.Window.Start.Time()) {
		t.Errorf("LastClosedOpenTime = %v, want %s", got.LastClosedOpenTime, latest.Window.Start.Time())
	}
	if got.LastClosedRevision == nil || *got.LastClosedRevision != 3 {
		t.Errorf("LastClosedRevision = %v, want 3", got.LastClosedRevision)
	}
}

func TestListInstrumentCandles_NoClosedCandleYet_JoinPointIsNil(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{joinPoint: nil},
		logger:      slog.Default(),
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{From: &from, To: &to},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	got, ok := resp.(ListInstrumentCandles200JSONResponse)
	if !ok {
		t.Fatalf("got %#v, want 200", resp)
	}
	if got.LastClosedOpenTime != nil || got.LastClosedRevision != nil {
		t.Errorf("join point = (%v, %v), want (nil, nil) when the instrument has no closed candle yet", got.LastClosedOpenTime, got.LastClosedRevision)
	}
}

func TestListInstrumentCandles_AggregatedIntervalMax1000Holds(t *testing.T) {
	h := &apiHandler{
		instruments: &fakeInstrumentsStore{byID: map[int64]instruments.Instrument{1: testInstrumentDomain(1)}},
		candles:     &fakeCandlesStore{},
		logger:      slog.Default(),
	}
	interval := N5m
	tooMany := int32(maxInstrumentCandles + 1)
	from, to := time.Now(), time.Now().Add(time.Hour)
	resp, err := h.ListInstrumentCandles(context.Background(), ListInstrumentCandlesRequestObject{
		Id: 1, Params: ListInstrumentCandlesParams{Interval: &interval, From: &from, To: &to, Limit: &tooMany},
	})
	if err != nil {
		t.Fatalf("ListInstrumentCandles: %v", err)
	}
	if _, ok := resp.(ListInstrumentCandles400JSONResponse); !ok {
		t.Fatalf("got %#v, want 400: the 1000 cap must hold for aggregated intervals too", resp)
	}
}
