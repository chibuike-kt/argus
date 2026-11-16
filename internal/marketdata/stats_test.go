package marketdata

import (
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// Golden values below are worked by hand, not computed with the code
// under test. Percent = (latest - ref) / ref * 100, cut to 2 dp,
// half-even (owner decision 16 November 2026).
func TestPercentChange_Golden(t *testing.T) {
	tests := []struct {
		name        string
		latest, ref quant.Price
		want        string
		wantOK      bool
	}{
		// 100.50 vs 100.00 (scale 2): +0.50 / 100.00 = +0.5%.
		{"up half percent", 10050, 10000, "0.50", true},
		// 98.76 vs 100.00: -1.24 / 100 = -1.24%.
		{"down", 9876, 10000, "-1.24", true},
		// 4/3: +1/3 = 33.333...% -> 33.33.
		{"thirds round down", 4, 3, "33.33", true},
		// 5/3: +2/3 = 66.666...% -> 66.67.
		{"thirds round up", 5, 3, "66.67", true},
		// 80012/80000: 12/80000*100 = 0.015% -> exact half of 0.01,
		// half-even rounds 1.5 hundredths to 2 -> 0.02.
		{"half rounds to even (up)", 80012, 80000, "0.02", true},
		// 80004/80000: 0.005% -> 0.5 hundredths -> 0 (even) -> 0.00.
		{"half rounds to even (down to zero)", 80004, 80000, "0.00", true},
		// Negative half: -0.015% -> -0.02 (magnitude rounded half-even).
		{"negative half to even", 79988, 80000, "-0.02", true},
		// -0.005% -> magnitude 0 -> "0.00", never "-0.00".
		{"negative rounds to unsigned zero", 79996, 80000, "0.00", true},
		{"unchanged", 12345, 12345, "0.00", true},
		// Doubling: +100%.
		{"doubled", 200, 100, "100.00", true},
		// Reference close of zero: ratio undefined, absent.
		{"zero reference is absent", 100, 0, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := percentChange(tc.latest, tc.ref)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.String() != tc.want {
				t.Fatalf("percentChange(%d, %d) = %q, want %q", tc.latest, tc.ref, got.String(), tc.want)
			}
		})
	}
}

func TestPriceChange_String(t *testing.T) {
	tests := []struct {
		c     PriceChange
		scale quant.Scale
		want  string
	}{
		{1250, 2, "12.50"},
		{-1250, 2, "-12.50"},
		{-5, 2, "-0.05"},
		{0, 2, "0.00"},
		{-7, 0, "-7"},
	}
	for _, tc := range tests {
		if got := tc.c.String(tc.scale); got != tc.want {
			t.Errorf("PriceChange(%d).String(%d) = %q, want %q", tc.c, tc.scale, got, tc.want)
		}
	}
}

func TestPerformancePeriods_ApprovedTable(t *testing.T) {
	want := []Period{
		{"1D", 24 * time.Hour},
		{"1W", 168 * time.Hour},
		{"1M", 720 * time.Hour},
		{"3M", 2160 * time.Hour},
		{"6M", 4320 * time.Hour},
		{"1Y", 8760 * time.Hour},
	}
	got := PerformancePeriods()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("period %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// inputsWith builds statsInputs with every reference absent; callers
// switch on the ones a case needs. Index 0 is the 24h change reference,
// 1..6 the performance periods in PerformancePeriods order.
func inputsWith(latest quant.Price) statsInputs {
	n := 1 + len(performancePeriods)
	return statsInputs{
		latestOpenTime:  time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC),
		latestClose:     latest,
		referenceFound:  make([]bool, n),
		referenceCloses: make([]quant.Price, n),
	}
}

func TestComputeStats_FullHistory(t *testing.T) {
	in := inputsWith(10050)                                   // 100.50
	in.referenceFound[0], in.referenceCloses[0] = true, 10000 // 24h ago 100.00
	in.referenceFound[1], in.referenceCloses[1] = true, 10000 // 1D
	in.referenceFound[2], in.referenceCloses[2] = true, 12000 // 1W 120.00
	in.volumeCount, in.volumeSum = 1440, 987654

	s, err := computeStats(7, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Latest == nil || s.Latest.Close != 10050 {
		t.Fatalf("Latest = %+v", s.Latest)
	}
	if s.Change24h == nil {
		t.Fatal("Change24h absent, want present")
	}
	// 100.50 - 100.00 = +0.50, +0.50%.
	if got := s.Change24h.Abs.String(2); got != "0.50" {
		t.Errorf("change abs = %q, want 0.50", got)
	}
	if s.Change24h.Pct == nil || s.Change24h.Pct.String() != "0.50" {
		t.Errorf("change pct = %v, want 0.50", s.Change24h.Pct)
	}
	if s.Volume24h == nil || *s.Volume24h != 987654 {
		t.Errorf("Volume24h = %v, want 987654", s.Volume24h)
	}
	// 1W: (100.50 - 120.00)/120.00 = -16.25% exactly.
	if p := s.Performance["1W"]; p == nil || p.String() != "-16.25" {
		t.Errorf("1W = %v, want -16.25", p)
	}
	for _, k := range []string{"1M", "3M", "6M", "1Y"} {
		if v, ok := s.Performance[k]; !ok || v != nil {
			t.Errorf("Performance[%s] = %v (present key %v), want present key with nil (absent)", k, v, ok)
		}
	}
}

func TestComputeStats_MissingReferenceIsAbsent(t *testing.T) {
	in := inputsWith(10050)
	in.volumeCount, in.volumeSum = 1440, 5
	s, err := computeStats(1, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Change24h != nil {
		t.Errorf("Change24h = %+v, want absent", s.Change24h)
	}
	if s.Performance["1D"] != nil {
		t.Errorf("1D = %v, want absent", s.Performance["1D"])
	}
}

func TestComputeStats_ShortVolumeWindowIsAbsent(t *testing.T) {
	in := inputsWith(10050)
	in.volumeCount, in.volumeSum = 1439, 5
	s, err := computeStats(1, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Volume24h != nil {
		t.Errorf("Volume24h = %v with 1439 candles, want absent", *s.Volume24h)
	}
}

func TestComputeStats_ZeroVolumeFullWindowIsPresentZero(t *testing.T) {
	// A full window whose candles all have zero volume is a real zero,
	// not absent.
	in := inputsWith(10050)
	in.volumeCount, in.volumeSum = 1440, 0
	s, err := computeStats(1, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Volume24h == nil || *s.Volume24h != 0 {
		t.Errorf("Volume24h = %v, want present 0", s.Volume24h)
	}
}

func TestComputeStats_MismatchedArraysIsError(t *testing.T) {
	in := inputsWith(1)
	in.referenceFound = in.referenceFound[:2]
	if _, err := computeStats(1, in); err == nil {
		t.Fatal("want error for mismatched reference arrays")
	}
}
