package backtest

import (
	"encoding/json"
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
		Dataset: "dev fixture dataset", HonestyNote: "fixture data is not market evidence: this report proves the plumbing works, it is not evidence about the model.",
		RulesVersion: outcomes.RulesVersion,
		PeriodStart:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:    time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
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

func TestCheckNoKeyCollisions(t *testing.T) {
	key := SetupKey{
		Symbol: "BTC-PERP", Timeframe: "1m", POIType: "qmr", Direction: "bullish",
		DetectedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), AlgorithmVersion: "v1",
	}

	t.Run("two different setups sharing a key is an error naming both", func(t *testing.T) {
		rows := []Row{
			{Key: key, rawSetupID: 101},
			{Key: key, rawSetupID: 202},
		}
		err := checkNoKeyCollisions(rows)
		if err == nil {
			t.Fatal("checkNoKeyCollisions: want an error, got nil")
		}
		for _, id := range []string{"101", "202"} {
			if !strings.Contains(err.Error(), id) {
				t.Errorf("collision error does not name setup %s: %v", id, err)
			}
		}
	})

	t.Run("distinct keys are not a collision", func(t *testing.T) {
		other := key
		other.Direction = "bearish"
		rows := []Row{
			{Key: key, rawSetupID: 101},
			{Key: other, rawSetupID: 202},
		}
		if err := checkNoKeyCollisions(rows); err != nil {
			t.Fatalf("checkNoKeyCollisions: want no error for distinct keys, got %v", err)
		}
	})
}

// fullRow builds a Row carrying database-assigned ids in every place
// Report ever exposes one (InstrumentID, and each CandleRef.ID reachable
// through Outcome), so TestBuildReport_JSONIndependentOfDatabaseIDs can
// vary all of them at once and still expect byte-identical JSON.
func fullRow(instrumentID int64, fillCandleID, targetCandleID int64) Row {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fill := outcomes.CandleRef{ID: fillCandleID, OpenTime: t0, CloseTime: t0.Add(time.Minute), Revision: 1}
	targetCandle := outcomes.CandleRef{ID: targetCandleID, OpenTime: t0.Add(time.Minute), CloseTime: t0.Add(2 * time.Minute), Revision: 1}
	o := outcomes.Outcome{
		Status: outcomes.StatusTargetHit, RulesVersion: outcomes.RulesVersion,
		Fill:                 &fill,
		FirstTarget:          &outcomes.TargetFact{Index: 0, Candle: targetCandle},
		RAtFirstTarget:       rr(3, 8),
		LastConsideredCandle: &targetCandle,
	}
	return Row{
		InstrumentID: instrumentID, Symbol: "BTC-PERP", Direction: "bullish", Timeframe: "1m", POIType: "qmr",
		Key: SetupKey{
			Symbol: "BTC-PERP", Timeframe: "1m", POIType: "qmr", Direction: "bullish",
			DetectedAt: t0, AlgorithmVersion: "swing-structure-v1",
		},
		rawSetupID: instrumentID*1000 + 1, // deliberately also varies with instrumentID
		Entry:      110, Stop: 70, PriceScale: 0,
		TargetRRs: []*big.Rat{rr(3, 8)},
		Outcome:   o,
	}
}

func TestBuildReport_JSONIndependentOfDatabaseIDs(t *testing.T) {
	meta := Meta{
		Dataset: "fixture dataset", HonestyNote: "fixture data is not market evidence: this report proves the plumbing works, it is not evidence about the model.",
		RulesVersion: outcomes.RulesVersion, AlgorithmVersion: "swing-structure-v1",
		Instruments: []string{"BTC-PERP"},
	}

	// "Database A": small ids. "Database B": large, unrelated ids --
	// the same business content either way.
	reportA := BuildReport([]Row{fullRow(1, 10, 11)}, meta)
	reportB := BuildReport([]Row{fullRow(9001, 88888, 88889)}, meta)

	jsonA, err := json.Marshal(reportA)
	if err != nil {
		t.Fatalf("marshal report A: %v", err)
	}
	jsonB, err := json.Marshal(reportB)
	if err != nil {
		t.Fatalf("marshal report B: %v", err)
	}

	if string(jsonA) != string(jsonB) {
		t.Fatalf("report JSON depends on database-assigned ids:\n--- A ---\n%s\n--- B ---\n%s", jsonA, jsonB)
	}
	// Sanity: the two reports really did carry different raw ids, so
	// this is testing something real, not two already-identical inputs.
	if reportA.Rows[0].InstrumentID == reportB.Rows[0].InstrumentID {
		t.Fatal("test setup error: reportA and reportB use the same InstrumentID")
	}
}
