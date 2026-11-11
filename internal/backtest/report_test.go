package backtest

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/outcomes"
	"github.com/chibuike-kt/argus/internal/quant"
)

func rr(n, d int64) *big.Rat { return big.NewRat(n, d) }

func row(instrument, direction, poiType string, status outcomes.Status, rAtFirst *big.Rat, laterIdx ...int) Row {
	o := outcomes.Outcome{Status: status, RulesVersion: outcomes.RulesVersion, RAtFirstTarget: rAtFirst}
	for _, i := range laterIdx {
		o.LaterTargets = append(o.LaterTargets, outcomes.TargetFact{Index: i})
	}
	return Row{
		InstrumentID: 1, Symbol: instrument, Direction: direction, Timeframe: "1m", POIType: poiType,
		Entry: quant.Price(10000), Stop: quant.Price(9900), PriceScale: 2,
		TargetRRs: []*big.Rat{rr(1, 2), rr(1, 1), rr(2, 1)},
		Outcome:   o,
	}
}

func TestBuildReport_BucketCountsAndHitRate(t *testing.T) {
	rows := []Row{
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusTargetHit, rr(1, 2)),
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusStopped, nil),
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusNeverFilled, nil),
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusOpen, nil),
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusNeverFilledInvalidated, nil),
	}
	rpt := BuildReport(rows, Meta{
		Dataset: "dev fixture dataset", RulesVersion: outcomes.RulesVersion,
		PeriodStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	})

	if len(rpt.Buckets) != 1 {
		t.Fatalf("Buckets = %d, want 1", len(rpt.Buckets))
	}
	b := rpt.Buckets[0]

	if b.Total != 5 {
		t.Errorf("Total = %d, want 5", b.Total)
	}
	if b.NeverFilled != 1 || b.NeverFilledInvalidated != 1 || b.Open != 1 {
		t.Errorf("counts = %+v, want never_filled=1 never_filled_invalidated=1 open=1", b)
	}
	// Hit rate denominator excludes never_filled, never_filled_invalidated
	// and open (spec section 16: "excluded from win rate"): only the
	// target_hit and stopped rows count, so 1 win out of 2.
	if b.ClosedCount() != 2 {
		t.Fatalf("ClosedCount = %d, want 2", b.ClosedCount())
	}
	if got, want := b.HitRate(), big.NewRat(1, 2); got.Cmp(want) != 0 {
		t.Errorf("HitRate = %v, want %v", got, want)
	}
}

func TestBuildReport_AverageR(t *testing.T) {
	rows := []Row{
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusTargetHit, rr(1, 1)), // win at R=1
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusStopped, nil),        // loss at R=-1
	}
	rpt := BuildReport(rows, Meta{Dataset: "d", RulesVersion: outcomes.RulesVersion})
	b := rpt.Buckets[0]

	// Average of +1 and -1 is 0.
	if got := b.AverageR(); got.Cmp(big.NewRat(0, 1)) != 0 {
		t.Errorf("AverageR = %v, want 0", got)
	}
}

func TestBuildReport_LaterTargetStats(t *testing.T) {
	rows := []Row{
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusTargetHit, rr(1, 2), 1), // reached target 2 as well
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusTargetHit, rr(1, 2)),    // target 1 only
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusStopped, nil),
	}
	rpt := BuildReport(rows, Meta{Dataset: "d", RulesVersion: outcomes.RulesVersion})
	b := rpt.Buckets[0]

	lt, ok := b.LaterTarget(1)
	if !ok {
		t.Fatalf("LaterTarget(1) not found")
	}
	// 1 of the 3 closed rows reached target index 1.
	if got, want := lt.Rate, big.NewRat(1, 3); got.Cmp(want) != 0 {
		t.Errorf("later target 1 rate = %v, want %v", got, want)
	}
	if got, want := lt.AverageR, big.NewRat(1, 1); got.Cmp(want) != 0 {
		t.Errorf("later target 1 average R = %v, want %v", got, want)
	}
}

func TestBuildReport_SmallSampleLabel(t *testing.T) {
	rows := []Row{row("BTC-PERP", "bullish", "order_block", outcomes.StatusTargetHit, rr(1, 2))}
	rpt := BuildReport(rows, Meta{Dataset: "d", RulesVersion: outcomes.RulesVersion})
	if !rpt.Buckets[0].SmallSample {
		t.Errorf("SmallSample = false, want true for a 1-row bucket")
	}

	md := rpt.RenderMarkdown()
	if !strings.Contains(md, "small sample") {
		t.Errorf("Markdown report does not label the small sample:\n%s", md)
	}
}

func TestBuildReport_CostSensitivity(t *testing.T) {
	// entry=10000, stop=9900 (distance 100), one win at R=1 (gross),
	// one loss at R=-1 (gross). At 10 bps of entry price (10 price
	// units), cost in R = 10/100 = 0.1 per trade.
	rows := []Row{
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusTargetHit, rr(1, 1)),
		row("BTC-PERP", "bullish", "order_block", outcomes.StatusStopped, nil),
	}
	rpt := BuildReport(rows, Meta{Dataset: "d", RulesVersion: outcomes.RulesVersion})

	adj, ok := rpt.CostAdjusted[10]
	if !ok {
		t.Fatalf("CostAdjusted[10] missing; have %v", rpt.CostAdjusted)
	}
	// (1-0.1 + -1-0.1) / 2 = (0.9 - 1.1)/2 = -0.1
	want := big.NewRat(-1, 10)
	if adj.Cmp(want) != 0 {
		t.Errorf("CostAdjusted[10] = %v, want %v", adj, want)
	}
}

func TestBuildReport_Markdown_RequiredContent(t *testing.T) {
	rows := []Row{row("BTC-PERP", "bullish", "order_block", outcomes.StatusTargetHit, rr(1, 2))}
	meta := Meta{
		Dataset: "dev fixture dataset", RulesVersion: outcomes.RulesVersion,
		PeriodStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	rpt := BuildReport(rows, meta)
	md := rpt.RenderMarkdown()

	for _, want := range []string{
		"dev fixture dataset",
		"not market evidence",
		outcomes.RulesVersion,
		"gross",
		"cost adjusted",
		"ambiguous_resolved_as_stop",
		"never filled",
		"open",
		"illustrative",
		"is presented as a forecast",
		"5 bps", "10 bps", "20 bps",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown report missing %q:\n%s", want, md)
		}
	}
}
