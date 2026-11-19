package marketdata

import (
	"math"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
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

// inputsWith builds statsInputs for a forming anchor with every
// reference absent and an empty volume window; callers switch on what a
// case needs. referenceFound/Closes are the performance periods in
// PerformancePeriods order.
func inputsWith(anchorClose quant.Price) statsInputs {
	n := len(performancePeriods)
	anchor := time.Date(2026, 11, 16, 12, 1, 0, 0, time.UTC)
	return statsInputs{
		base: LiveBase{
			AnchorOpenTime:    clock.NewEventTime(anchor),
			ReferenceOpenTime: clock.NewEventTime(anchor.Add(-formingReferenceOffset)),
		},
		anchorClose:     anchorClose,
		anchorTickAt:    clock.NewIngestionTime(anchor.Add(17 * time.Second)),
		latestFound:     true,
		latestOpenTime:  anchor.Add(-time.Minute),
		latestClose:     10000,
		referenceFound:  make([]bool, n),
		referenceCloses: make([]quant.Price, n),
	}
}

func TestComputeStats_FullHistory(t *testing.T) {
	in := inputsWith(10050) // forming close 100.50
	in.base.ReferenceFound, in.base.ReferenceClose = true, 10000
	in.latestClose = 10040                                    // latest closed 100.40
	in.referenceFound[0], in.referenceCloses[0] = true, 10000 // 1D 100.00
	in.referenceFound[1], in.referenceCloses[1] = true, 12000 // 1W 120.00
	in.base.PriorCount, in.base.PriorVolume = 1439, 987000
	in.anchorVolume = 654

	s, err := computeStats(7, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live == nil || s.Live.LastPrice != 10050 {
		t.Fatalf("Live = %+v, want last price 10050 (the forming close)", s.Live)
	}
	if s.Latest == nil || s.Latest.Close != 10040 {
		t.Fatalf("Latest = %+v, want the closed 10040", s.Latest)
	}
	// D3: forming 100.50 - reference 100.00 = +0.50, +0.50%.
	if s.Live.Change24h == nil {
		t.Fatal("Change24h absent, want present")
	}
	if got := s.Live.Change24h.Abs.String(2); got != "0.50" {
		t.Errorf("change abs = %q, want 0.50", got)
	}
	if s.Live.Change24h.Pct == nil || s.Live.Change24h.Pct.String() != "0.50" {
		t.Errorf("change pct = %v, want 0.50", s.Live.Change24h.Pct)
	}
	// D3: 1439 closed minutes before the anchor plus the forming one.
	if s.Live.VolumeMinutes != 1440 || s.Live.Volume24h == nil || *s.Live.Volume24h != 987654 {
		t.Errorf("Volume24h = %v over %d minutes, want 987654 over 1440", s.Live.Volume24h, s.Live.VolumeMinutes)
	}
	// Rule 4 is still anchored to the latest CLOSED candle:
	// 1D (100.40 - 100.00)/100.00 = +0.40%; 1W (100.40 - 120.00)/120.00
	// = -16.333...% -> -16.33.
	if p := s.Performance["1D"]; p == nil || p.String() != "0.40" {
		t.Errorf("1D = %v, want 0.40", p)
	}
	if p := s.Performance["1W"]; p == nil || p.String() != "-16.33" {
		t.Errorf("1W = %v, want -16.33", p)
	}
	for _, k := range []string{"1M", "3M", "6M", "1Y"} {
		if v, ok := s.Performance[k]; !ok || v != nil {
			t.Errorf("Performance[%s] = %v (present key %v), want present key with nil (absent)", k, v, ok)
		}
	}
	if !s.Live.AsOf.Time().Equal(in.base.AnchorOpenTime.Time()) || !s.Live.LastTickAt.Time().Equal(in.anchorTickAt.Time()) {
		t.Errorf("as_of %v last tick %v, want the anchor's open time and ingestion time", s.Live.AsOf, s.Live.LastTickAt)
	}
}

func TestComputeStats_MissingReferenceIsAbsent(t *testing.T) {
	in := inputsWith(10050)
	in.base.PriorCount, in.base.PriorVolume = 1439, 5
	s, err := computeStats(1, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live.Change24h != nil {
		t.Errorf("Change24h = %+v, want absent", s.Live.Change24h)
	}
	if !s.Live.ReferenceOpenTime.Time().Equal(in.base.ReferenceOpenTime.Time()) {
		t.Errorf("ReferenceOpenTime = %v, want the minute that was looked up", s.Live.ReferenceOpenTime)
	}
	if s.Performance["1D"] != nil {
		t.Errorf("1D = %v, want absent", s.Performance["1D"])
	}
}

func TestComputeStats_ShortVolumeWindowIsAbsent(t *testing.T) {
	in := inputsWith(10050)
	in.base.PriorCount, in.base.PriorVolume = 1438, 5
	s, err := computeStats(1, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live.Volume24h != nil {
		t.Errorf("Volume24h = %v with 1439 candles, want absent", *s.Live.Volume24h)
	}
	if s.Live.VolumeMinutes != 1439 {
		t.Errorf("VolumeMinutes = %d, want 1439", s.Live.VolumeMinutes)
	}
}

func TestComputeStats_ZeroVolumeFullWindowIsPresentZero(t *testing.T) {
	// A full window whose candles all have zero volume is a real zero,
	// not absent.
	in := inputsWith(10050)
	in.base.PriorCount = 1439
	s, err := computeStats(1, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live.Volume24h == nil || *s.Live.Volume24h != 0 {
		t.Errorf("Volume24h = %v, want present 0", s.Live.Volume24h)
	}
}

func TestComputeStats_NoClosedCandleYet(t *testing.T) {
	// Only a forming candle exists: the live price is present, the
	// closed-anchored figures are absent.
	in := inputsWith(10050)
	in.latestFound = false
	s, err := computeStats(1, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live == nil || s.Live.LastPrice != 10050 || s.Latest != nil {
		t.Fatalf("Live = %+v Latest = %+v, want live present and latest absent", s.Live, s.Latest)
	}
	for k, v := range s.Performance {
		if v != nil {
			t.Errorf("Performance[%s] = %v, want absent", k, v)
		}
	}
}

func TestComputeStats_MismatchedArraysIsError(t *testing.T) {
	in := inputsWith(1)
	in.referenceFound = in.referenceFound[:2]
	if _, err := computeStats(1, in); err == nil {
		t.Fatal("want error for mismatched reference arrays")
	}
}

// TestLiveBaseApply_Golden is the per-tick path the hub uses: hand worked
// values, one base, several forming ticks of the same minute.
func TestLiveBaseApply_Golden(t *testing.T) {
	base := LiveBase{
		AnchorOpenTime: clock.NewEventTime(time.Date(2026, 11, 16, 12, 1, 0, 0, time.UTC)),
		ReferenceFound: true,
		ReferenceClose: 8000000, // 80000.00 at scale 2
		PriorCount:     1439,
		PriorVolume:    1_000_000, // 1.000000 at qty scale 6
	}
	tests := []struct {
		close           quant.Price
		volume          quant.Qty
		wantAbs, wantPc string
		wantVol         quant.Qty
	}{
		// 80120.00 - 80000.00 = +120.00; 120/80000 = 0.15%.
		{8012000, 2500, "120.00", "0.15", 1_002_500},
		// 79960.00 - 80000.00 = -40.00; -40/80000 = -0.05%.
		{7996000, 4000, "-40.00", "-0.05", 1_004_000},
		// 80001.20: +1.20 / 80000 = 0.0015% -> 0.00 (half-even, 0.15 hundredths rounds down).
		{8000120, 4000, "1.20", "0.00", 1_004_000},
	}
	for _, tc := range tests {
		f, err := base.Apply(tc.close, tc.volume, clock.NewIngestionTime(time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		if f.Change24h == nil || f.Change24h.Abs.String(2) != tc.wantAbs || f.Change24h.Pct.String() != tc.wantPc {
			t.Errorf("close %d: change = %+v, want %s / %s%%", tc.close, f.Change24h, tc.wantAbs, tc.wantPc)
		}
		if f.Volume24h == nil || *f.Volume24h != tc.wantVol {
			t.Errorf("close %d: volume = %v, want %d", tc.close, f.Volume24h, tc.wantVol)
		}
	}
}

func TestLiveBaseApply_VolumeOverflowIsError(t *testing.T) {
	base := LiveBase{PriorCount: 1439, PriorVolume: quant.Qty(math.MaxInt64)}
	if _, err := base.Apply(1, 1, clock.IngestionTime{}); err == nil {
		t.Fatal("want an overflow error, never a wrapped sum")
	}
}
