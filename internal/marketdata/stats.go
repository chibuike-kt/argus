package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Period is one performance tile's reference offset: the reference
// candle is the current, closed 1-minute candle whose open time is
// EXACTLY Offset before the latest closed one's (real-panel-data
// brief, rules 1 and 4). Never a nearby candle, never interpolated.
type Period struct {
	Key    string
	Offset time.Duration
}

// performancePeriods is the approved period table (real-panel-data
// brief rule 4: 1D = 24h, 1W = 7 days; 1M/3M/6M/1Y as fixed day
// counts -- 30/90/180/365 days -- owner decision recorded 16 November
// 2026, chosen over calendar months so every reference minute is an
// exact duration with no "31 March minus one month" rule needed).
// Order is the order the tiles are shown in.
var performancePeriods = [...]Period{
	{Key: "1D", Offset: 24 * time.Hour},
	{Key: "1W", Offset: 7 * 24 * time.Hour},
	{Key: "1M", Offset: 30 * 24 * time.Hour},
	{Key: "3M", Offset: 90 * 24 * time.Hour},
	{Key: "6M", Offset: 180 * 24 * time.Hour},
	{Key: "1Y", Offset: 365 * 24 * time.Hour},
}

// PerformancePeriods returns a copy of the approved period table.
func PerformancePeriods() []Period {
	out := make([]Period, len(performancePeriods))
	copy(out, performancePeriods[:])
	return out
}

// D3 (product overhaul brief, approved by the owner 16 November 2026)
// amends rules 1 and 2 of the panel brief: the live price is the forming
// candle's close; the 24h change compares it to the close of the closed
// candle whose open time is forming.open_time - 24h - 1 minute (the price
// at exactly 24h before the current minute began); the 24h volume is the
// base volume over the 1440 candles ending at the forming one.
//
// When no forming candle is newer than the latest closed one (between a
// minute's close and the next minute's first tick, or a feed that has
// stopped), the anchor is that closed candle and the reference is the
// closed candle exactly 24h before it: the original rule 1, which is the
// same "price exactly 24h earlier" for a price taken at that candle's own
// close. The volume window has the same shape either way: the 1440
// minutes ending at (and including) the anchor.
const (
	formingReferenceOffset = 24*time.Hour + time.Minute
	closedReferenceOffset  = 24 * time.Hour
)

// volume24hWindow is the 24h volume window: the 1440 1-minute candles
// ending at (and including) the anchor.
const volume24hWindow = 24 * time.Hour

// volume24hCandles is how many 1-minute candles volume24hWindow holds;
// the sum is present only when exactly this many exist.
const volume24hCandles = int64(volume24hWindow / time.Minute)

// pctScale is the number of decimal places a Percent carries (owner
// decision 16 November 2026: 2 dp, half-even).
const pctScale = 2

// PriceChange is a signed difference of two quant.Price values at the
// same instrument price scale. quant.Price is non-negative by contract,
// so a change (which can be negative) is its own type rather than a
// Price. The difference of two non-negative int64 values always fits in
// int64, so no overflow check is needed to construct one.
type PriceChange int64

// String renders c as an exact signed decimal at scale, e.g. "-12.50".
// Zero has no sign.
func (c PriceChange) String(scale quant.Scale) string {
	if c < 0 {
		return "-" + quant.Price(-c).String(scale)
	}
	return quant.Price(c).String(scale)
}

// Percent is a percentage scaled by 10^pctScale (hundredths of a
// percent): Percent(-123) is -1.23%.
type Percent int64

// String renders p in percent units with pctScale decimals and no "%"
// sign, e.g. "-1.23". Zero has no sign.
func (p Percent) String() string {
	if p < 0 {
		return "-" + quant.Price(-p).String(pctScale)
	}
	return quant.Price(p).String(pctScale)
}

// percentChange returns (latest-ref)/ref*100, rounded half-even to
// pctScale decimals, computed exactly with big.Int (no float64). ok is
// false when ref is zero (the ratio is undefined: absent, not guessed)
// or the result does not fit in int64.
func percentChange(latest, ref quant.Price) (Percent, bool) {
	if ref <= 0 {
		return 0, false
	}
	// num/den = (latest-ref) * 100 * 10^pctScale / ref
	num := new(big.Int).Sub(big.NewInt(int64(latest)), big.NewInt(int64(ref)))
	num.Mul(num, big.NewInt(100*pow10(pctScale)))
	den := big.NewInt(int64(ref))

	neg := num.Sign() < 0
	num.Abs(num)
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	// Half-even: compare 2r with den.
	twice := new(big.Int).Lsh(r, 1)
	switch twice.Cmp(den) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return 0, false
	}
	v := q.Int64()
	if neg {
		v = -v
	}
	return Percent(v), true
}

func pow10(n int) int64 {
	v := int64(1)
	for range n {
		v *= 10
	}
	return v
}

// Change is a change against one exact reference candle.
type Change struct {
	Abs PriceChange
	// Pct is nil when the reference close is zero (undefined ratio).
	Pct *Percent
}

// LiveBase is everything D3 needs for one anchor minute except the
// anchor's own close and volume: the change reference and the closed
// minutes of the volume window before the anchor. The hub keeps one per
// instrument and applies each forming tick of the same minute to it
// (Apply), so a tick costs no query; the REST endpoint applies the
// anchor row it just read. Both go through Apply, so the first load and
// every live update use the same arithmetic.
type LiveBase struct {
	AnchorOpenTime clock.EventTime
	// AnchorClosed is true when the anchor is a closed candle (no
	// forming row newer than it).
	AnchorClosed bool
	// ReferenceOpenTime is the exact minute the change reference was
	// looked up at, found or not.
	ReferenceOpenTime clock.EventTime
	ReferenceFound    bool
	ReferenceClose    quant.Price
	// PriorCount and PriorVolume are the count and base volume sum of
	// the current, closed candles in the volume window strictly before
	// the anchor (at most volume24hCandles - 1).
	PriorCount  int64
	PriorVolume quant.Qty
}

// LiveFigures are the live side-panel figures (D3). Change24h and
// Volume24h are nil when absent: the input they need does not exist,
// and the caller must show it as absent, never as zero.
type LiveFigures struct {
	// LastPrice is the anchor's close: the forming candle's latest
	// close, or the latest closed candle's close when nothing is forming.
	LastPrice quant.Price
	// AsOf is the exchange event time the price belongs to: the anchor
	// candle's open time. The provider's candle feed carries no
	// per-trade timestamp, so this is minute-grained.
	AsOf clock.EventTime
	// AnchorClosed is true when LastPrice is a closed candle's close.
	AnchorClosed bool
	// LastTickAt is when ARGUS stored the anchor's latest value (the
	// row's ingested_at): an ingestion time, not an exchange time.
	LastTickAt clock.IngestionTime
	Change24h  *Change
	// ReferenceOpenTime is the minute Change24h is measured against.
	ReferenceOpenTime clock.EventTime
	// Volume24h is present only when all volume24hCandles minutes exist.
	Volume24h *quant.Qty
	// VolumeMinutes is how many of the volume24hCandles minutes exist
	// (the anchor included), so an absent Volume24h can say why.
	VolumeMinutes int64
}

// Apply computes the live figures for the anchor whose own close and
// volume are price and volume, last stored at lastTickAt. It does no I/O.
func (b LiveBase) Apply(price quant.Price, volume quant.Qty, lastTickAt clock.IngestionTime) (LiveFigures, error) {
	f := LiveFigures{
		LastPrice:         price,
		AsOf:              b.AnchorOpenTime,
		AnchorClosed:      b.AnchorClosed,
		LastTickAt:        lastTickAt,
		ReferenceOpenTime: b.ReferenceOpenTime,
		VolumeMinutes:     b.PriorCount + 1,
	}
	if b.ReferenceFound {
		c := Change{Abs: PriceChange(int64(price) - int64(b.ReferenceClose))}
		if pct, ok := percentChange(price, b.ReferenceClose); ok {
			c.Pct = &pct
		}
		f.Change24h = &c
	}
	if f.VolumeMinutes == volume24hCandles {
		v, err := b.PriorVolume.Add(volume)
		if err != nil {
			return LiveFigures{}, fmt.Errorf("marketdata: 24h volume at %s: %w", b.AnchorOpenTime, err)
		}
		f.Volume24h = &v
	}
	return f, nil
}

// Stats is one instrument's side-panel figures, every one derived from
// stored current (non-superseded) 1-minute candles. A nil pointer means
// absent: the input it needs does not exist, and the caller must show it
// as absent, never as zero.
type Stats struct {
	InstrumentID int64
	// Latest is the latest closed candle (the performance anchor and
	// "finalized through"); nil when no candle is closed yet.
	Latest *LatestClose
	// Live is D3's figures; nil when the instrument has no current
	// candle at all (and then Base is nil too).
	Live *LiveFigures
	// Base is what Live was computed from, for the hub's per-tick path.
	Base *LiveBase
	// Performance is rule 4 (unchanged by D3: anchored to the latest
	// closed candle), keyed by Period.Key, one entry for every period in
	// PerformancePeriods (nil value: absent).
	Performance map[string]*Percent
}

// LatestClose is the latest current, closed 1-minute candle's close and
// open time.
type LatestClose struct {
	Close    quant.Price
	OpenTime clock.EventTime
}

// statsInputs is what GetCandleStatsInputs returns, decoupled from the
// sqlc row type so computeStats is a pure function over plain values.
type statsInputs struct {
	base            LiveBase
	anchorClose     quant.Price
	anchorVolume    quant.Qty
	anchorTickAt    clock.IngestionTime
	latestFound     bool
	latestOpenTime  time.Time
	latestClose     quant.Price
	referenceFound  []bool
	referenceCloses []quant.Price
}

// statsOffsets is the performance reference offsets sent to the query,
// one per performance period, in order.
func statsOffsets() []time.Duration {
	out := make([]time.Duration, 0, len(performancePeriods))
	for _, p := range performancePeriods {
		out = append(out, p.Offset)
	}
	return out
}

func emptyPerformance() map[string]*Percent {
	perf := make(map[string]*Percent, len(performancePeriods))
	for _, p := range performancePeriods {
		perf[p.Key] = nil
	}
	return perf
}

// computeStats applies D3 and rule 4 to in. It does no I/O.
func computeStats(instrumentID int64, in statsInputs) (Stats, error) {
	offsets := statsOffsets()
	if len(in.referenceFound) != len(offsets) || len(in.referenceCloses) != len(offsets) {
		return Stats{}, fmt.Errorf("marketdata: stats instrument=%d: %d offsets requested, got %d found flags and %d closes",
			instrumentID, len(offsets), len(in.referenceFound), len(in.referenceCloses))
	}

	live, err := in.base.Apply(in.anchorClose, in.anchorVolume, in.anchorTickAt)
	if err != nil {
		return Stats{}, fmt.Errorf("marketdata: stats instrument=%d: %w", instrumentID, err)
	}
	base := in.base
	s := Stats{
		InstrumentID: instrumentID,
		Live:         &live,
		Base:         &base,
		Performance:  emptyPerformance(),
	}
	if !in.latestFound {
		return s, nil
	}
	s.Latest = &LatestClose{Close: in.latestClose, OpenTime: clock.NewEventTime(in.latestOpenTime)}
	for i, p := range performancePeriods {
		if !in.referenceFound[i] {
			continue
		}
		if pct, ok := percentChange(in.latestClose, in.referenceCloses[i]); ok {
			s.Performance[p.Key] = &pct
		}
	}
	return s, nil
}

// Stats returns instrumentID's side-panel figures (D3 and rule 4) from
// one SQL statement (GetCandleStatsInputs). An instrument with no
// current candle yet is not an error: it returns Stats with Live, Latest
// and every figure absent.
func (s *Store) Stats(ctx context.Context, instrumentID int64) (Stats, error) {
	offsets := statsOffsets()
	secs := make([]int64, len(offsets))
	for i, o := range offsets {
		secs[i] = int64(o / time.Second)
	}

	row, err := s.q.GetCandleStatsInputs(ctx, store.GetCandleStatsInputsParams{
		InstrumentID:                  instrumentID,
		OffsetsSeconds:                secs,
		VolumeWindowSeconds:           int64(volume24hWindow / time.Second),
		FormingReferenceOffsetSeconds: int64(formingReferenceOffset / time.Second),
		ClosedReferenceOffsetSeconds:  int64(closedReferenceOffset / time.Second),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Stats{InstrumentID: instrumentID, Performance: emptyPerformance()}, nil
	}
	if err != nil {
		return Stats{}, fmt.Errorf("marketdata: stats instrument=%d: %w", instrumentID, err)
	}

	closes := make([]quant.Price, len(row.ReferenceCloses))
	for i, c := range row.ReferenceCloses {
		closes[i] = quant.Price(c)
	}
	return computeStats(instrumentID, statsInputs{
		base: LiveBase{
			AnchorOpenTime:    clock.NewEventTime(row.AnchorOpenTime),
			AnchorClosed:      row.AnchorIsClosed,
			ReferenceOpenTime: clock.NewEventTime(row.ChangeReferenceOpenTime),
			ReferenceFound:    row.ChangeReferenceFound,
			ReferenceClose:    quant.Price(row.ChangeReferenceClose),
			PriorCount:        row.VolumeWindowCount,
			PriorVolume:       quant.Qty(row.VolumeWindowSum),
		},
		anchorClose:     quant.Price(row.AnchorClose),
		anchorVolume:    quant.Qty(row.AnchorVolume),
		anchorTickAt:    clock.NewIngestionTime(row.AnchorIngestedAt),
		latestFound:     row.LatestFound,
		latestOpenTime:  row.LatestOpenTime,
		latestClose:     quant.Price(row.LatestClose),
		referenceFound:  row.ReferenceFound,
		referenceCloses: closes,
	})
}
