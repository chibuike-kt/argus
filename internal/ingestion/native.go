package ingestion

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Native higher timeframe candles (L1.1; ADR-006 "Higher timeframe
// candles", decision D11): 15m, 1h and 4h candles fetched from the
// provider's own historical endpoint and stored, closed only, in the same
// candles table as the 1 minute series, each under its own interval.

// DefaultNativeVolumeNoiseDivisor is the float noise threshold of L1.1
// decision 1: a native candle's volume with more decimals than the
// instrument's quantity scale is rounded (half to even) only when the
// difference is below one unit of that scale divided by this number
// (1/1000 of a unit). Configurable through
// config.Hyperliquid.NativeVolumeNoiseDivisor.
const DefaultNativeVolumeNoiseDivisor int64 = 1000

// ErrNativeUnsupported is returned by IngestNative when the provider has
// no native higher timeframe candles.
var ErrNativeUnsupported = errors.New("ingestion: provider has no native higher timeframe candles")

// NativeProvider is the optional provider capability for native higher
// timeframe candles: HistoricalIntervalCandles returns every candle of
// interval whose window overlaps [start, end), oldest first, each open
// time once. It does not decide which candles are closed; IngestNative
// does, from the clock.
type NativeProvider interface {
	HistoricalIntervalCandles(ctx context.Context, symbol string, interval marketdata.Interval, start, end time.Time) ([]ProviderCandle, error)
}

// nativeStore is what the native path needs from marketdata.Store.
type nativeStore interface {
	InsertClosed(ctx context.Context, interval marketdata.Interval, t marketdata.Tick, now time.Time) (marketdata.Candle, bool, error)
	SupersedeCandleAt(ctx context.Context, interval marketdata.Interval, instrumentID int64, openTime clock.EventTime, corr marketdata.Correction) (marketdata.Candle, error)
	GetCurrentAt(ctx context.Context, interval marketdata.Interval, instrumentID int64, openTime clock.EventTime) (marketdata.Candle, error)
	// ListCurrentCandlesInRange is the 1 minute read marketdata.Aggregate
	// uses, for the comparison with the 1 minute aggregate.
	ListCurrentCandlesInRange(ctx context.Context, instrumentID int64, start, end time.Time) ([]store.Candle, error)
}

var _ nativeStore = (*marketdata.Store)(nil)

// ParseNativeVolume parses a native higher timeframe candle's volume at
// scale. A value that fits the scale is returned exactly (rounded false).
// A value with more decimals is rounded half to even only when the excess
// is float noise: |raw - rounded| below one unit of scale divided by
// divisor (L1.1 decision 1). Anything larger is the same rejection the 1
// minute path makes (ErrScaleMismatch). Exact arithmetic throughout
// (math/big), never a float.
func ParseNativeVolume(s string, scale quant.Scale, divisor int64) (q quant.Qty, rounded bool, err error) {
	if divisor <= 0 {
		return 0, false, fmt.Errorf("ingestion: native volume noise divisor %d must be positive", divisor)
	}
	q, err = quant.ParseQty(s, scale)
	if err == nil {
		return q, false, nil
	}
	if !errors.Is(err, quant.ErrPrecisionLoss) {
		return 0, false, err
	}
	r, err := quant.ParseQtyRounded(s, scale, quant.RoundHalfEven)
	if err != nil {
		return 0, false, err
	}
	raw, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, false, quant.ErrMalformedDecimal
	}
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	diff := new(big.Rat).Sub(raw, new(big.Rat).SetFrac(big.NewInt(int64(r)), unit))
	diff.Abs(diff)
	// threshold = (1 / 10^scale) / divisor
	threshold := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Mul(unit, big.NewInt(divisor)))
	if diff.Cmp(threshold) >= 0 {
		return 0, false, fmt.Errorf("native volume %q differs from %d at scale %d by %s, not below 1/%d of a unit: %w",
			s, r, scale, diff.FloatString(int(scale)+12), divisor, quant.ErrPrecisionLoss)
	}
	return r, true, nil
}

// NormalizeNative is NormalizeCandle for a native candle of interval: the
// open time must be a boundary of interval, prices must fit the
// instrument's price scale exactly (no rounding, as for 1 minute), and the
// volume follows ParseNativeVolume. rounded reports a float-noise volume
// rounding, which the caller logs and counts.
func (n *Normalizer) NormalizeNative(ctx context.Context, provider string, interval marketdata.Interval, c ProviderCandle, volumeNoiseDivisor int64) (nc NormalizedCandle, rounded bool, err error) {
	facts, err := n.instruments.Get(ctx, provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, false, fmt.Errorf("ingestion: normalize %s candle %s/%s: %w", interval, provider, c.Symbol, err)
	}
	openTime := c.OpenTime.UTC()
	if !interval.Aligned(openTime) {
		return NormalizedCandle{}, false, fmt.Errorf("ingestion: normalize %s candle %s/%s open_time=%s: %w",
			interval, provider, c.Symbol, openTime, marketdata.ErrNotIntervalAligned)
	}
	open, err := n.parsePrice(c.Open, facts.PriceScale, "open", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, false, err
	}
	high, err := n.parsePrice(c.High, facts.PriceScale, "high", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, false, err
	}
	low, err := n.parsePrice(c.Low, facts.PriceScale, "low", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, false, err
	}
	closePrice, err := n.parsePrice(c.Close, facts.PriceScale, "close", provider, c.Symbol)
	if err != nil {
		return NormalizedCandle{}, false, err
	}
	volume, rounded, err := ParseNativeVolume(c.Volume, facts.QtyScale, volumeNoiseDivisor)
	if err != nil {
		return NormalizedCandle{}, false, wrapParseError(err, "volume", provider, c.Symbol, c.Volume, facts.QtyScale)
	}
	if high < low || high < open || high < closePrice || low > open || low > closePrice {
		return NormalizedCandle{}, false, fmt.Errorf("ingestion: normalize %s candle %s/%s open_time=%s: %w",
			interval, provider, c.Symbol, openTime, ErrOHLCInconsistent)
	}
	tickAligned := open.IsMultipleOf(facts.TickSize) && high.IsMultipleOf(facts.TickSize) &&
		low.IsMultipleOf(facts.TickSize) && closePrice.IsMultipleOf(facts.TickSize)
	return NormalizedCandle{
		InstrumentID: facts.ID,
		Provider:     provider,
		OpenTime:     clock.NewEventTime(openTime),
		Open:         open,
		High:         high,
		Low:          low,
		Close:        closePrice,
		Volume:       volume,
		TickAligned:  tickAligned,
		IngestedAt:   clock.NewIngestionTime(n.clk.Now()),
		IsFinal:      true,
	}, rounded, nil
}

// SupportsNative reports whether the provider has native higher timeframe
// candles (implements NativeProvider). The dev fixture provider does not.
func (s *Service) SupportsNative() bool {
	_, ok := s.provider.(NativeProvider)
	return ok
}

// InstrumentID resolves symbol to its registered instrument id.
func (s *Service) InstrumentID(ctx context.Context, symbol string) (int64, error) {
	facts, err := s.instruments.Get(ctx, s.providerName, symbol)
	if err != nil {
		return 0, err
	}
	return facts.ID, nil
}

// NativeResult counts what one IngestNative call did.
type NativeResult struct {
	Fetched   int // candles the provider returned
	Forming   int // not closed at now: not stored
	Inserted  int // new closed candles stored at revision 1
	Unchanged int // already stored with identical values
	Corrected int // stored with different values: a new revision
	Rejected  int // failed normalization (logged)
	Rounded   int // volumes rounded as float noise (logged, counted)
}

// SetNativeVolumeNoiseDivisor overrides DefaultNativeVolumeNoiseDivisor
// (config.Hyperliquid.NativeVolumeNoiseDivisor). Values <= 0 are ignored.
func (s *Service) SetNativeVolumeNoiseDivisor(d int64) {
	if d > 0 {
		s.nativeNoiseDivisor = d
	}
}

// NativeVolumeRoundings is how many native volumes this Service has
// rounded as float noise since it started.
func (s *Service) NativeVolumeRoundings() int64 { return s.nativeRounded.Load() }

// NativeAggregateMismatches is how many native candles differed from the
// aggregate of their stored 1 minute candles (CompareNativeWithAggregate).
func (s *Service) NativeAggregateMismatches() int64 { return s.nativeMismatches.Load() }

// IngestNative fetches symbol's native interval candles overlapping
// [start, end) and stores every one that has closed by now (close time at
// or before now; a forming candle is never stored, L1.1 decision 5). A
// candle already stored with the same values is left alone; with
// different values it is corrected through the same revision rule as the
// 1 minute series (SupersedeCandleAt). A candle that fails normalization
// is logged and skipped, like a 1 minute candle.
func (s *Service) IngestNative(ctx context.Context, symbol string, interval marketdata.Interval, start, end, now time.Time) (NativeResult, error) {
	var res NativeResult
	if err := interval.Validate(); err != nil {
		return res, err
	}
	d, _ := interval.Duration()
	np, ok := s.provider.(NativeProvider)
	if !ok {
		return res, ErrNativeUnsupported
	}
	raw, err := np.HistoricalIntervalCandles(ctx, symbol, interval, start, end)
	if err != nil {
		return res, fmt.Errorf("ingestion: native %s %s/%s [%s,%s): %w", interval, s.providerName, symbol, start, end, err)
	}
	res.Fetched = len(raw)
	for _, c := range raw {
		if c.OpenTime.Add(d).After(now) {
			res.Forming++
			continue
		}
		nc, rounded, err := s.normalizer.NormalizeNative(ctx, s.providerName, interval, c, s.nativeNoiseDivisor)
		if err != nil {
			res.Rejected++
			s.rejectCandle(symbol, c, err)
			continue
		}
		if rounded {
			res.Rounded++
			n := s.nativeRounded.Add(1)
			s.logger.Info("ingestion: native volume rounded (float noise)",
				"provider", s.providerName, "symbol", symbol, "interval", interval, "open_time", nc.OpenTime,
				"raw_volume", c.Volume, "stored_volume", nc.Volume, "rounded_total", n)
		}
		if err := s.applyNative(ctx, symbol, interval, nc, now, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (s *Service) applyNative(ctx context.Context, symbol string, interval marketdata.Interval, nc NormalizedCandle, now time.Time, res *NativeResult) error {
	stored, inserted, err := s.native.InsertClosed(ctx, interval, toTick(nc), now)
	if err != nil {
		return fmt.Errorf("ingestion: store native %s candle %s/%s open_time=%s: %w", interval, s.providerName, symbol, nc.OpenTime, err)
	}
	switch {
	case inserted:
		res.Inserted++
	case candleValuesEqual(stored, nc):
		res.Unchanged++
	default:
		s.logger.Warn("ingestion: native candle corrected by the provider",
			"provider", s.providerName, "symbol", symbol, "interval", interval, "open_time", nc.OpenTime,
			"revision", stored.Revision,
			"stored_open", stored.Open, "stored_high", stored.High, "stored_low", stored.Low, "stored_close", stored.Close, "stored_volume", stored.Volume,
			"new_open", nc.Open, "new_high", nc.High, "new_low", nc.Low, "new_close", nc.Close, "new_volume", nc.Volume)
		if _, err := s.native.SupersedeCandleAt(ctx, interval, nc.InstrumentID, nc.OpenTime, toCorrection(nc)); err != nil {
			return fmt.Errorf("ingestion: correct native %s candle %s/%s open_time=%s: %w", interval, s.providerName, symbol, nc.OpenTime, err)
		}
		res.Corrected++
	}
	return nil
}

// NativeComparison is the result of CompareNativeWithAggregate.
type NativeComparison struct {
	// Compared is false when the window's 1 minute candles are not all
	// present and closed, or the native candle is not stored.
	Compared bool
	// Differs lists the fields that differ: open, high, low, close, volume.
	Differs []string
}

// CompareNativeWithAggregate compares the stored native candle of interval
// at openTime with the aggregate of its stored 1 minute candles, when all
// of them are present and closed (L1.1 decision 6). A difference is logged
// at WARN and counted (NativeAggregateMismatches); nothing is overwritten:
// the native candle stays as the provider sent it.
func (s *Service) CompareNativeWithAggregate(ctx context.Context, symbol string, interval marketdata.Interval, instrumentID int64, openTime time.Time) (NativeComparison, error) {
	d, ok := interval.Duration()
	if !ok {
		return NativeComparison{}, interval.Validate()
	}
	native, err := s.native.GetCurrentAt(ctx, interval, instrumentID, clock.NewEventTime(openTime))
	if errors.Is(err, marketdata.ErrCandleNotFound) {
		return NativeComparison{}, nil
	}
	if err != nil {
		return NativeComparison{}, err
	}
	w := marketdata.Window{Start: clock.NewEventTime(openTime), End: clock.NewEventTime(openTime.Add(d))}
	agg, incomplete, err := marketdata.Aggregate(ctx, s.native, instrumentID, w)
	if err != nil {
		return NativeComparison{}, err
	}
	if incomplete != nil {
		return NativeComparison{}, nil
	}
	var differs []string
	if native.Open != agg.Open {
		differs = append(differs, "open")
	}
	if native.High != agg.High {
		differs = append(differs, "high")
	}
	if native.Low != agg.Low {
		differs = append(differs, "low")
	}
	if native.Close != agg.Close {
		differs = append(differs, "close")
	}
	if native.Volume != agg.Volume {
		differs = append(differs, "volume")
	}
	if len(differs) > 0 {
		n := s.nativeMismatches.Add(1)
		s.logger.Warn("ingestion: native candle differs from its 1 minute aggregate (kept as the provider sent it)",
			"provider", s.providerName, "symbol", symbol, "interval", interval, "open_time", openTime, "fields", differs,
			"native_open", native.Open, "native_high", native.High, "native_low", native.Low, "native_close", native.Close, "native_volume", native.Volume,
			"aggregate_open", agg.Open, "aggregate_high", agg.High, "aggregate_low", agg.Low, "aggregate_close", agg.Close, "aggregate_volume", agg.Volume,
			"mismatches_total", n)
	}
	return NativeComparison{Compared: true, Differs: differs}, nil
}

// WatchCandlesNotify is WatchCandles that also calls onClosed with the
// open time of every 1 minute candle the live stream reports closed (a
// final value; also one the normalizer rejected and skipped, since the
// minute has still ended). onClosed must not block: the worker uses
// it to schedule the fetch of a native candle whose window just closed.
func (s *Service) WatchCandlesNotify(ctx context.Context, symbol string, onClosed func(openTime time.Time)) error {
	return s.provider.StreamCandles(ctx, symbol, func(ctx context.Context, c ProviderCandle) error {
		if err := s.handleLiveCandle(ctx, symbol, c); err != nil {
			return err
		}
		if onClosed != nil && c.IsFinal {
			onClosed(c.OpenTime.UTC())
		}
		return nil
	})
}
