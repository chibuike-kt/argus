package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/instruments"
)

var bfNow = time.Date(2026, 11, 16, 18, 30, 20, 0, time.UTC)

func TestBackfillWindow_NothingStored_UsesBackfillDays(t *testing.T) {
	start, lost := backfillWindow(bfNow, nil, 3)
	if !start.Equal(bfNow.Add(-72*time.Hour)) || lost != nil {
		t.Fatalf("start=%s lost=%v, want 3 days back, nothing lost", start, lost)
	}
}

func TestBackfillWindow_NothingStored_ClampedToRetention(t *testing.T) {
	// 7 days back is beyond 5000 minutes (about 3.47 days).
	start, lost := backfillWindow(bfNow, nil, 7)
	want := time.Date(2026, 11, 16, 18, 30, 0, 0, time.UTC).Add(-5000 * time.Minute).Add(10 * time.Minute)
	if !start.Equal(want) || lost != nil {
		t.Fatalf("start=%s, want %s (retention edge), lost=%v", start, want, lost)
	}
}

func TestBackfillWindow_FromNewestStoredMinusOverlap(t *testing.T) {
	newest := time.Date(2026, 11, 16, 14, 25, 0, 0, time.UTC)
	start, lost := backfillWindow(bfNow, &newest, 3)
	if !start.Equal(time.Date(2026, 11, 16, 14, 20, 0, 0, time.UTC)) || lost != nil {
		t.Fatalf("start=%s lost=%v, want 14:20 (newest 14:25 minus 5m), nothing lost", start, lost)
	}
}

func TestBackfillWindow_NewestOlderThanRetention_ReportsLostRange(t *testing.T) {
	newest := bfNow.Truncate(time.Minute).Add(-5 * 24 * time.Hour) // 7200 minutes back
	start, lost := backfillWindow(bfNow, &newest, 3)
	edge := bfNow.Truncate(time.Minute).Add(-5000 * time.Minute).Add(10 * time.Minute)
	if !start.Equal(edge) {
		t.Fatalf("start=%s, want retention edge %s", start, edge)
	}
	if lost == nil || !lost[0].Equal(newest.Add(time.Minute)) || !lost[1].Equal(edge) {
		t.Fatalf("lost=%v, want [%s, %s)", lost, newest.Add(time.Minute), edge)
	}
	// 7200 - 1 - (5000 - 10) = 2209 minutes can never be fetched.
	if got := int(lost[1].Sub(lost[0]) / time.Minute); got != 2209 {
		t.Fatalf("lost minutes = %d, want 2209", got)
	}
}

func inst(provider, symbol string) instruments.Instrument {
	return instruments.Instrument{Provider: provider, NativeSymbol: symbol, Enabled: true}
}

// (5) Provider unset while Hyperliquid instruments are enabled: refuse.
func TestCheckIngestProvider_UnsetWithRealInstrumentsRefuses(t *testing.T) {
	_, err := checkIngestProvider(false, "fixture", []instruments.Instrument{inst("fixture", "BTC-PERP"), inst("hyperliquid", "ETH"), inst("hyperliquid", "BTC")})
	if err == nil {
		t.Fatal("want refusal when ARGUS_INGEST_PROVIDER is unset and hyperliquid instruments are enabled")
	}
	if !strings.Contains(err.Error(), "hyperliquid (BTC, ETH)") || !strings.Contains(err.Error(), "ARGUS_INGEST_PROVIDER") {
		t.Fatalf("error does not name the provider and instruments: %v", err)
	}
}

func TestCheckIngestProvider_UnsetFixtureOnlyStarts(t *testing.T) {
	if w, err := checkIngestProvider(false, "fixture", []instruments.Instrument{inst("fixture", "BTC-PERP")}); err != nil || w != "" {
		t.Fatalf("w=%q err=%v, want a clean start with only fixture instruments", w, err)
	}
}

func TestCheckIngestProvider_ExplicitFixtureWithRealInstrumentsWarns(t *testing.T) {
	w, err := checkIngestProvider(true, "fixture", []instruments.Instrument{inst("hyperliquid", "SOL")})
	if err != nil || !strings.Contains(w, "hyperliquid (SOL)") {
		t.Fatalf("w=%q err=%v, want a loud warning naming hyperliquid (SOL)", w, err)
	}
}

func TestCheckIngestProvider_MatchingProviderClean(t *testing.T) {
	if w, err := checkIngestProvider(true, "hyperliquid", []instruments.Instrument{inst("hyperliquid", "SOL"), inst("fixture", "BTC-PERP")}); err != nil || w != "" {
		t.Fatalf("w=%q err=%v, want clean", w, err)
	}
}

// (item 1.5) The tick log must stop looking healthy: an enabled
// non-fixture instrument with no new candle for longer than the
// threshold gets a WARN carrying its age; fixture instruments (old by
// design) do not; an instrument with no candle at all is reported.
func TestReportCandleAge_WarnsOnlyForQuietRealFeeds(t *testing.T) {
	var buf bytes.Buffer
	p := &processor{logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	now := time.Date(2026, 11, 16, 18, 30, 0, 0, time.UTC)
	var ages []any

	p.reportCandleAge(inst("hyperliquid", "ETH"), now.Add(-41*time.Minute), now, &ages)
	p.reportCandleAge(inst("hyperliquid", "BTC"), now.Add(-30*time.Second), now, &ages)
	p.reportCandleAge(inst("fixture", "BTC-PERP"), now.Add(-300*24*time.Hour), now, &ages)
	p.reportCandleAge(inst("hyperliquid", "SOL"), time.Time{}, now, &ages)

	out := buf.String()
	if !strings.Contains(out, `"symbol":"ETH"`) || !strings.Contains(out, "no new candle") {
		t.Fatalf("no WARN for ETH, 41 minutes quiet: %s", out)
	}
	if strings.Contains(out, `"symbol":"BTC"`) {
		t.Fatalf("WARN for BTC with a 30s-old candle: %s", out)
	}
	if strings.Contains(out, "BTC-PERP") {
		t.Fatalf("WARN for a fixture instrument: %s", out)
	}
	if !strings.Contains(out, `"symbol":"SOL"`) || !strings.Contains(out, "no closed candle at all") {
		t.Fatalf("no report for SOL with no candle: %s", out)
	}
	if len(ages) != 4 {
		t.Fatalf("ages has %d entries, want one per instrument", len(ages))
	}
}

// Seen in the real run: a candle stored after the tick started looked
// "newer than now" (a negative age). Age is measured against when the
// candle was read and never goes below zero.
func TestReportCandleAge_NeverNegative(t *testing.T) {
	var buf bytes.Buffer
	p := &processor{logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	now := time.Date(2026, 11, 16, 18, 30, 0, 0, time.UTC)
	var ages []any
	p.reportCandleAge(inst("hyperliquid", "ETH"), now.Add(2*time.Minute), now, &ages)
	if got := ages[0].(slog.Attr).Value.String(); got != "0s" {
		t.Fatalf("age = %q, want 0s (never negative)", got)
	}
	if strings.Contains(buf.String(), "no new candle") {
		t.Fatalf("WARN for a candle newer than now: %s", buf.String())
	}
}
