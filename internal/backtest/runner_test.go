package backtest

import (
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
