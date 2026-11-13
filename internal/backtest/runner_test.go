package backtest

import (
	"strings"
	"testing"
	"time"
)

func TestSetupKeyLess_OrdersBySymbolThenDetectedAt(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := SetupKey{Symbol: "AAA", DetectedAt: t0.Add(time.Hour)}
	b := SetupKey{Symbol: "BBB", DetectedAt: t0}

	if !setupKeyLess(a, b) {
		t.Errorf("setupKeyLess(%+v, %+v) = false, want true (symbol AAA < BBB)", a, b)
	}
	if setupKeyLess(b, a) {
		t.Errorf("setupKeyLess(%+v, %+v) = true, want false", b, a)
	}

	// Same symbol: ordered by when each identity was first detected.
	c := SetupKey{Symbol: "AAA", DetectedAt: t0}
	d := SetupKey{Symbol: "AAA", DetectedAt: t0.Add(time.Minute)}
	if !setupKeyLess(c, d) {
		t.Errorf("setupKeyLess(%+v, %+v) = false, want true (earlier DetectedAt)", c, d)
	}
}

func TestDistinctProviders_SortsAndDedupes(t *testing.T) {
	got := distinctProviders([]string{"fixture", "test-provider", "fixture", "binance"})
	want := []string{"binance", "fixture", "test-provider"}
	if len(got) != len(want) {
		t.Fatalf("distinctProviders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("distinctProviders = %v, want %v", got, want)
		}
	}
}

func TestDatasetLabel_DerivedFromProvider(t *testing.T) {
	dataset, honesty := datasetLabel([]string{"fixture"})
	if dataset != "fixture dataset" {
		t.Errorf("dataset = %q, want %q", dataset, "fixture dataset")
	}
	if honesty != "fixture data is not market evidence: this report proves the plumbing works, it is not evidence about the model." {
		t.Errorf("honesty note = %q, does not match the fixture-derived sentence", honesty)
	}

	// A different provider must change the label and the sentence's own
	// subject, not just be ignored in favor of a hardcoded "fixture".
	dataset, honesty = datasetLabel([]string{"binance"})
	if dataset != "binance dataset" {
		t.Errorf("dataset = %q, want %q", dataset, "binance dataset")
	}
	if honesty != "binance data is not market evidence: this report proves the plumbing works, it is not evidence about the model." {
		t.Errorf("honesty note = %q, does not match the binance-derived sentence", honesty)
	}

	// Multiple distinct providers in one run: both named, sorted.
	dataset, _ = datasetLabel([]string{"test-provider", "fixture"})
	if dataset != "fixture, test-provider dataset" {
		t.Errorf("dataset = %q, want %q", dataset, "fixture, test-provider dataset")
	}
}

func TestDatasetLabel_HyperliquidGetsTheOwnerApprovedWording(t *testing.T) {
	const wantDataset = "Hyperliquid perpetuals last price candles, private research use, rights not yet confirmed (ADR-007)"
	dataset, honesty := datasetLabel([]string{"hyperliquid"})
	if dataset != wantDataset {
		t.Errorf("dataset = %q, want the owner's own exact wording %q", dataset, wantDataset)
	}
	if !strings.Contains(honesty, "ADR-007") {
		t.Errorf("honesty note = %q, want it to mention ADR-007", honesty)
	}

	// Multiple instrument rows all under the same provider still
	// collapse to one hyperliquid entry (distinctProviders' own job) --
	// confirms this is not accidentally gated on len(providers) == 1
	// rather than len(distinct names) == 1.
	dataset, _ = datasetLabel([]string{"hyperliquid", "hyperliquid"})
	if dataset != wantDataset {
		t.Errorf("dataset = %q, want %q even with duplicate entries", dataset, wantDataset)
	}

	// A mix of providers, even including hyperliquid, must NOT get the
	// specific wording -- that would overstate what a mixed-provider
	// report actually ran on.
	dataset, _ = datasetLabel([]string{"hyperliquid", "fixture"})
	if dataset == wantDataset {
		t.Errorf("a mixed-provider run got the hyperliquid-only label %q", dataset)
	}
}
