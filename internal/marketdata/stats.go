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

// change24hOffset is rule 1's reference offset (identical to the 1D
// period; kept separate so the two rules stay independently readable).
const change24hOffset = 24 * time.Hour

// volume24hWindow is rule 2's window: the 1440 closed 1-minute candles
// ending at (and including) the latest closed one.
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

// Stats is one instrument's side-panel figures, every one derived from
// stored current (non-superseded), closed 1-minute candles. A nil
// pointer means absent (rules 1, 2, 4): the input it needs does not
// exist, and the caller must show it as absent, never as zero.
type Stats struct {
	InstrumentID int64
	// Latest is nil when the instrument has no closed candle at all;
	// every other field is then absent too.
	Latest *LatestClose
	// Change24h is rule 1.
	Change24h *Change
	// Volume24h is rule 2: base asset volume over the full 1440-candle
	// window, nil unless every one of those candles exists.
	Volume24h *quant.Qty
	// Performance is rule 4, keyed by Period.Key, one entry for every
	// period in PerformancePeriods (nil value: absent).
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
	latestOpenTime  time.Time
	latestClose     quant.Price
	volumeCount     int64
	volumeSum       quant.Qty
	referenceFound  []bool
	referenceCloses []quant.Price
}

// statsOffsets is the reference offsets sent to the query, in a fixed
// order: index 0 is change24hOffset, then one per performance period.
func statsOffsets() []time.Duration {
	out := make([]time.Duration, 0, 1+len(performancePeriods))
	out = append(out, change24hOffset)
	for _, p := range performancePeriods {
		out = append(out, p.Offset)
	}
	return out
}

// computeStats applies rules 1, 2 and 4 to in. It does no I/O.
func computeStats(instrumentID int64, in statsInputs) (Stats, error) {
	offsets := statsOffsets()
	if len(in.referenceFound) != len(offsets) || len(in.referenceCloses) != len(offsets) {
		return Stats{}, fmt.Errorf("marketdata: stats instrument=%d: %d offsets requested, got %d found flags and %d closes",
			instrumentID, len(offsets), len(in.referenceFound), len(in.referenceCloses))
	}

	s := Stats{
		InstrumentID: instrumentID,
		Latest:       &LatestClose{Close: in.latestClose, OpenTime: clock.NewEventTime(in.latestOpenTime)},
		Performance:  make(map[string]*Percent, len(performancePeriods)),
	}

	if in.referenceFound[0] {
		ref := in.referenceCloses[0]
		c := Change{Abs: PriceChange(int64(in.latestClose) - int64(ref))}
		if pct, ok := percentChange(in.latestClose, ref); ok {
			c.Pct = &pct
		}
		s.Change24h = &c
	}

	if in.volumeCount == volume24hCandles {
		v := in.volumeSum
		s.Volume24h = &v
	}

	for i, p := range performancePeriods {
		s.Performance[p.Key] = nil
		if !in.referenceFound[i+1] {
			continue
		}
		if pct, ok := percentChange(in.latestClose, in.referenceCloses[i+1]); ok {
			s.Performance[p.Key] = &pct
		}
	}
	return s, nil
}

// Stats returns instrumentID's side-panel figures (real-panel-data
// brief rules 1, 2, 4) from one SQL statement (GetCandleStatsInputs).
// An instrument with no closed candle yet is not an error: it returns
// Stats with Latest nil and every figure absent.
func (s *Store) Stats(ctx context.Context, instrumentID int64) (Stats, error) {
	offsets := statsOffsets()
	secs := make([]int64, len(offsets))
	for i, o := range offsets {
		secs[i] = int64(o / time.Second)
	}

	row, err := s.q.GetCandleStatsInputs(ctx, store.GetCandleStatsInputsParams{
		InstrumentID:        instrumentID,
		OffsetsSeconds:      secs,
		VolumeWindowSeconds: int64(volume24hWindow / time.Second),
	})
	if errors.Is(err, sql.ErrNoRows) {
		perf := make(map[string]*Percent, len(performancePeriods))
		for _, p := range performancePeriods {
			perf[p.Key] = nil
		}
		return Stats{InstrumentID: instrumentID, Performance: perf}, nil
	}
	if err != nil {
		return Stats{}, fmt.Errorf("marketdata: stats instrument=%d: %w", instrumentID, err)
	}

	closes := make([]quant.Price, len(row.ReferenceCloses))
	for i, c := range row.ReferenceCloses {
		closes[i] = quant.Price(c)
	}
	return computeStats(instrumentID, statsInputs{
		latestOpenTime:  row.LatestOpenTime,
		latestClose:     quant.Price(row.LatestClose),
		volumeCount:     row.VolumeWindowCount,
		volumeSum:       quant.Qty(row.VolumeWindowSum),
		referenceFound:  row.ReferenceFound,
		referenceCloses: closes,
	})
}
