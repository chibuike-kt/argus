package backtest

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/chibuike-kt/argus/internal/outcomes"
	"github.com/chibuike-kt/argus/internal/quant"
)

// smallSampleThreshold is the bucket size below which this report labels
// a bucket's own numbers "small sample" (spec section 16: "a small
// sample is labelled as such"). The spec does not name a number, and
// none exists anywhere else in the docs (checked docs/adr and
// docs/strategy) -- this is this package's own placeholder, not an
// approved rule, flagged here and in the backtest brief's own report
// back. 30 is the conventional rule-of-thumb minimum for an
// approximately stable proportion estimate; the product owner has not
// reviewed it.
const smallSampleThreshold = 30

// costSensitivityBps are the round trip cost assumptions spec section 16
// requires, in basis points of the entry price: "illustrative, not
// measured" (not fit to any real fee/funding/spread data).
var costSensitivityBps = []int{5, 10, 20}

// Row is one evaluated setup: enough of its own plan (for cost-
// sensitivity and later-target reporting) plus the Outcome itself.
type Row struct {
	InstrumentID int64
	Symbol       string
	Direction    string // "bullish" or "bearish"
	Timeframe    string
	POIType      string // "order_block" or "qmr" -- whatever analysis.POIType.String() produced
	SetupID      int64

	Entry, Stop quant.Price
	PriceScale  quant.Scale
	// TargetRRs is the plan's own Targets[i].RR, in order, so a later
	// target's own R can be looked up by index without re-deriving it.
	TargetRRs []*big.Rat

	Outcome outcomes.Outcome
}

// grossR returns this row's own gross R multiple and whether it counts
// toward the hit-rate/average-R denominator at all (only stopped and
// target_hit rows do -- never_filled, never_filled_invalidated and open
// are excluded from win rate, spec section 16).
func (r Row) grossR() (*big.Rat, bool) {
	switch r.Outcome.Status {
	case outcomes.StatusTargetHit:
		return r.Outcome.RAtFirstTarget, true
	case outcomes.StatusStopped:
		return big.NewRat(-1, 1), true
	default:
		return nil, false
	}
}

// costInR converts a round trip cost in basis points of the entry price
// into an R multiple, using this row's own risk distance
// (entry-stop in bullish terms; the distance is the same magnitude
// either direction, so no direction branch is needed here).
func (r Row) costInR(bps int) *big.Rat {
	entryMinusStop := int64(r.Entry) - int64(r.Stop)
	if entryMinusStop < 0 {
		entryMinusStop = -entryMinusStop
	}
	if entryMinusStop == 0 {
		return big.NewRat(0, 1)
	}
	costInPrice := new(big.Rat).Mul(big.NewRat(int64(r.Entry), 1), big.NewRat(int64(bps), 10000))
	return new(big.Rat).Quo(costInPrice, big.NewRat(entryMinusStop, 1))
}

// Meta is report-level information that does not vary per bucket.
type Meta struct {
	Dataset          string // e.g. "dev fixture dataset" -- see the honesty statement this produces
	RulesVersion     string
	AlgorithmVersion string
	PeriodStart      time.Time
	PeriodEnd        time.Time
	Instruments      []string // symbols included in this run, for the report header
}

// LaterTargetStat is one later-target index's own supplementary rate and
// average R, computed over the same closed-trade population as the
// bucket's own headline HitRate/AverageR -- not the headline figure
// itself (the whole position is assumed closed at the first target,
// ADR-014), purely "how often would holding further have also worked."
type LaterTargetStat struct {
	Index    int
	Rate     *big.Rat
	AverageR *big.Rat
}

// bucketKey groups rows by instrument, direction, timeframe and POI type
// (brief Part 2's own required grouping), using whatever these values
// the engine actually produced, never an invented category.
type bucketKey struct {
	instrument, direction, timeframe, poiType string
}

// Bucket is one (instrument, direction, timeframe, POI type) group's own
// aggregated counts and figures.
type Bucket struct {
	Instrument, Direction, Timeframe, POIType string

	Total                   int
	NeverFilled             int
	NeverFilledInvalidated  int
	Open                    int
	Stopped                 int
	TargetHit               int
	AmbiguousResolvedAsStop int

	SmallSample bool

	closedGrossR []*big.Rat
	// laterCounts/laterSums are keyed by target index, over the same
	// closed population closedGrossR already represents.
	laterCounts map[int]int
	laterSums   map[int]*big.Rat
}

// ClosedCount is the hit-rate/average-R denominator: stopped plus
// target_hit, excluding never_filled, never_filled_invalidated and open
// (spec section 16).
func (b Bucket) ClosedCount() int { return b.Stopped + b.TargetHit }

// HitRate is TargetHit / ClosedCount, nil when ClosedCount is zero (no
// closed trade to rate at all).
func (b Bucket) HitRate() *big.Rat {
	if b.ClosedCount() == 0 {
		return nil
	}
	return big.NewRat(int64(b.TargetHit), int64(b.ClosedCount()))
}

// AverageR is the mean gross R across every closed trade (a stopped row
// contributing exactly -1, a target_hit row contributing its own
// RAtFirstTarget), nil when ClosedCount is zero.
func (b Bucket) AverageR() *big.Rat {
	if len(b.closedGrossR) == 0 {
		return nil
	}
	sum := new(big.Rat)
	for _, r := range b.closedGrossR {
		sum.Add(sum, r)
	}
	return sum.Quo(sum, big.NewRat(int64(len(b.closedGrossR)), 1))
}

// LaterTarget returns index's own supplementary rate/average-R, over the
// same closed population AverageR/HitRate use. ok is false when no
// closed row in this bucket ever reached that target index.
func (b Bucket) LaterTarget(index int) (LaterTargetStat, bool) {
	count, ok := b.laterCounts[index]
	if !ok || count == 0 {
		return LaterTargetStat{}, false
	}
	sum := b.laterSums[index]
	avg := new(big.Rat).Quo(sum, big.NewRat(int64(count), 1))
	rate := big.NewRat(int64(count), int64(b.ClosedCount()))
	return LaterTargetStat{Index: index, Rate: rate, AverageR: avg}, true
}

// sortedLaterTargetIndices returns this bucket's own later-target indices
// that have at least one hit, ascending, for deterministic report output.
func (b Bucket) sortedLaterTargetIndices() []int {
	out := make([]int, 0, len(b.laterCounts))
	for idx, n := range b.laterCounts {
		if n > 0 {
			out = append(out, idx)
		}
	}
	sort.Ints(out)
	return out
}

// Report is a complete backtest run's output: every bucket, plus the
// overall (not per-bucket, see report_test.go) cost-sensitivity table.
type Report struct {
	Meta
	// Rows is every evaluated setup, unaggregated -- the JSON report's
	// own detail level; Buckets is the Markdown report's own summary
	// level, derived from the same rows.
	Rows    []Row
	Buckets []Bucket

	// CostAdjusted maps a round trip cost (basis points of entry price,
	// costSensitivityBps) to the overall average R after deducting that
	// cost from every closed row -- illustrative, not measured (spec
	// section 16).
	CostAdjusted map[int]*big.Rat

	TotalRows                    int
	TotalNeverFilled             int
	TotalNeverFilledInvalidated  int
	TotalOpen                    int
	TotalAmbiguousResolvedAsStop int
}

// BuildReport aggregates rows into a Report. It never tunes or filters
// anything to make a number look better (brief's own "important honesty
// rule"): every row given to it is counted exactly once, in exactly one
// bucket.
func BuildReport(rows []Row, meta Meta) Report {
	buckets := map[bucketKey]*Bucket{}
	var order []bucketKey

	rpt := Report{Meta: meta, Rows: rows, CostAdjusted: map[int]*big.Rat{}}

	var allClosedGross []*big.Rat
	var allClosedRows []Row

	for _, r := range rows {
		key := bucketKey{r.Symbol, r.Direction, r.Timeframe, r.POIType}
		b, ok := buckets[key]
		if !ok {
			b = &Bucket{
				Instrument: r.Symbol, Direction: r.Direction, Timeframe: r.Timeframe, POIType: r.POIType,
				laterCounts: map[int]int{}, laterSums: map[int]*big.Rat{},
			}
			buckets[key] = b
			order = append(order, key)
		}

		b.Total++
		rpt.TotalRows++
		switch r.Outcome.Status {
		case outcomes.StatusNeverFilled:
			b.NeverFilled++
			rpt.TotalNeverFilled++
		case outcomes.StatusNeverFilledInvalidated:
			b.NeverFilledInvalidated++
			rpt.TotalNeverFilledInvalidated++
		case outcomes.StatusOpen:
			b.Open++
			rpt.TotalOpen++
		case outcomes.StatusStopped:
			b.Stopped++
		case outcomes.StatusTargetHit:
			b.TargetHit++
		}
		if r.Outcome.AmbiguousResolvedAsStop {
			b.AmbiguousResolvedAsStop++
			rpt.TotalAmbiguousResolvedAsStop++
		}

		if gross, counts := r.grossR(); counts {
			b.closedGrossR = append(b.closedGrossR, gross)
			allClosedGross = append(allClosedGross, gross)
			allClosedRows = append(allClosedRows, r)

			for _, lt := range r.Outcome.LaterTargets {
				b.laterCounts[lt.Index]++
				if b.laterSums[lt.Index] == nil {
					b.laterSums[lt.Index] = new(big.Rat)
				}
				if lt.Index < len(r.TargetRRs) && r.TargetRRs[lt.Index] != nil {
					b.laterSums[lt.Index].Add(b.laterSums[lt.Index], r.TargetRRs[lt.Index])
				}
			}
		}
	}

	for _, key := range order {
		b := buckets[key]
		b.SmallSample = b.Total < smallSampleThreshold
		rpt.Buckets = append(rpt.Buckets, *b)
	}

	for _, bps := range costSensitivityBps {
		if len(allClosedRows) == 0 {
			continue
		}
		sum := new(big.Rat)
		for i, gross := range allClosedGross {
			cost := allClosedRows[i].costInR(bps)
			sum.Add(sum, new(big.Rat).Sub(gross, cost))
		}
		rpt.CostAdjusted[bps] = sum.Quo(sum, big.NewRat(int64(len(allClosedGross)), 1))
	}

	return rpt
}

// formatRat renders r to decimals fractional digits, rounded half away
// from zero (spec section 16: "R multiples... shown to two decimals,
// rounded half away from zero"), using quant.RoundHalfUp -- the
// package's own name for that exact mode -- against r scaled by
// 10^decimals. nil prints as "n/a" (no closed trade to compute from).
func formatRat(r *big.Rat, decimals int) string {
	if r == nil {
		return "n/a"
	}
	neg := r.Sign() < 0
	abs := new(big.Rat).Abs(r)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Rat).Mul(abs, new(big.Rat).SetInt(scale))
	num, den := scaled.Num(), scaled.Denom()
	quotient, remainder := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Lsh(remainder, 1)
	if twice.Cmp(den) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	s := quotient.String()
	for len(s) <= decimals {
		s = "0" + s
	}
	intPart, fracPart := s[:len(s)-decimals], s[len(s)-decimals:]
	out := intPart
	if decimals > 0 {
		out += "." + fracPart
	}
	if neg && quotient.Sign() != 0 {
		out = "-" + out
	}
	return out
}

func formatPercent(r *big.Rat) string {
	if r == nil {
		return "n/a"
	}
	pct := new(big.Rat).Mul(r, big.NewRat(100, 1))
	return formatRat(pct, 1) + "%"
}

// RenderMarkdown renders a human-readable report following spec section
// 16's "Exclusions and reporting" exactly, plus brief Part 2's own
// additions (the cost sensitivity table, the honesty statement).
func (rpt Report) RenderMarkdown() string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Backtest report\n\n")
	fmt.Fprintf(&sb, "**Dataset:** %s. Fixture data is not market evidence: this report proves the plumbing works, it is not evidence about the model.\n\n", rpt.Dataset)
	fmt.Fprintf(&sb, "**Period:** %s to %s\n\n", rpt.PeriodStart.Format(time.RFC3339), rpt.PeriodEnd.Format(time.RFC3339))
	fmt.Fprintf(&sb, "**Instruments:** %s\n\n", strings.Join(rpt.Instruments, ", "))
	fmt.Fprintf(&sb, "**Rules version:** %s  \n**Algorithm version:** %s\n\n", rpt.RulesVersion, rpt.AlgorithmVersion)

	fmt.Fprintf(&sb, "**Totals:** %d setups evaluated. never filled: %d, never filled (invalidated): %d, open: %d, ambiguous_resolved_as_stop: %d.\n\n",
		rpt.TotalRows, rpt.TotalNeverFilled, rpt.TotalNeverFilledInvalidated, rpt.TotalOpen, rpt.TotalAmbiguousResolvedAsStop)

	fmt.Fprintf(&sb, "## Cost sensitivity (illustrative, not measured)\n\n")
	fmt.Fprintf(&sb, "Gross average R (before costs) across every closed trade, and the same figure after deducting an illustrative round trip cost from each trade:\n\n")
	fmt.Fprintf(&sb, "| | gross | cost adjusted |\n|---|---|---|\n")
	for _, bps := range costSensitivityBps {
		adj := rpt.CostAdjusted[bps]
		fmt.Fprintf(&sb, "| %d bps | %s | %s |\n", bps, formatRat(overallGrossAverageR(rpt), 2), formatRat(adj, 2))
	}
	sb.WriteString("\n")

	fmt.Fprintf(&sb, "## Buckets (by instrument, direction, timeframe, POI type)\n\n")
	for _, b := range rpt.Buckets {
		fmt.Fprintf(&sb, "### %s / %s / %s / %s\n\n", b.Instrument, b.Direction, b.Timeframe, b.POIType)
		if b.SmallSample {
			sb.WriteString("_small sample -- treat this bucket's figures as illustrative only._\n\n")
		}
		fmt.Fprintf(&sb, "Sample size: %d (closed: %d, never filled: %d, never filled (invalidated): %d, open: %d, ambiguous_resolved_as_stop: %d)\n\n",
			b.Total, b.ClosedCount(), b.NeverFilled, b.NeverFilledInvalidated, b.Open, b.AmbiguousResolvedAsStop)
		fmt.Fprintf(&sb, "Hit rate (first target): %s  \nAverage R, gross (first target): %s\n\n", formatPercent(b.HitRate()), formatRat(b.AverageR(), 2))

		for _, idx := range b.sortedLaterTargetIndices() {
			lt, ok := b.LaterTarget(idx)
			if !ok {
				continue
			}
			fmt.Fprintf(&sb, "Later target %d: reached rate %s, average R if held to it %s\n\n", idx+1, formatPercent(lt.Rate), formatRat(lt.AverageR, 2))
		}
	}

	sb.WriteString("No result in this report is presented as a forecast.\n")

	return sb.String()
}

// overallGrossAverageR recomputes the all-bucket gross average R for the
// cost-sensitivity table's own "gross" column, from the same closed
// population BuildReport already walked -- not stored on Report itself,
// since nothing else needs it as a field.
func overallGrossAverageR(rpt Report) *big.Rat {
	var sum big.Rat
	var n int64
	for _, b := range rpt.Buckets {
		for _, r := range b.closedGrossR {
			sum.Add(&sum, r)
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return sum.Quo(&sum, big.NewRat(n, 1))
}
