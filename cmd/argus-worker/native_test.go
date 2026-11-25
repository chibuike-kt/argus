package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// L1.1 decision 6: the windows a closed minute completes.
func TestClosedWindows(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.UTC) }
	type w struct {
		iv   marketdata.Interval
		open time.Time
	}
	cases := []struct {
		minute time.Time
		want   []w
	}{
		{at(13, 13), nil},
		{at(13, 14), []w{{marketdata.Interval15m, at(13, 0)}}},
		{at(13, 59), []w{{marketdata.Interval15m, at(13, 45)}, {marketdata.Interval1h, at(13, 0)}}},
		{at(15, 59), []w{{marketdata.Interval15m, at(15, 45)}, {marketdata.Interval1h, at(15, 0)}, {marketdata.Interval4h, at(12, 0)}}},
		{at(23, 59), []w{{marketdata.Interval15m, at(23, 45)}, {marketdata.Interval1h, at(23, 0)}, {marketdata.Interval4h, at(20, 0)}}},
	}
	for _, c := range cases {
		got := closedWindows(c.minute)
		if len(got) != len(c.want) {
			t.Fatalf("closedWindows(%s) = %+v, want %+v", c.minute, got, c.want)
		}
		for i := range got {
			if got[i].interval != c.want[i].iv || !got[i].open.Equal(c.want[i].open) {
				t.Fatalf("closedWindows(%s)[%d] = %s %s, want %s %s", c.minute, i, got[i].interval, got[i].open, c.want[i].iv, c.want[i].open)
			}
		}
	}
	// The same instants in a non UTC location give the same windows.
	ny, _ := time.LoadLocation("America/New_York")
	if got := closedWindows(at(15, 59).In(ny)); len(got) != 3 || !got[2].open.Equal(at(12, 0)) {
		t.Fatalf("closedWindows in New York = %+v, want the 12:00 UTC 4h window", got)
	}
}

// L1.1 decision 4: the first run reads the whole retention; later runs
// start at the newest stored candle, never before the retention edge.
func TestNativeBackfillStart(t *testing.T) {
	now := time.Date(2026, 11, 20, 13, 37, 0, 0, time.UTC)
	edge := nativeRetentionStart(now, marketdata.Interval1h)
	if want := time.Date(2026, 11, 20, 13, 0, 0, 0, time.UTC).Add(-4999 * time.Hour); !edge.Equal(want) {
		t.Fatalf("1h retention start = %s, want %s (5000 candles back from the current window, plus one)", edge, want)
	}
	if got := nativeBackfillStart(now, nil, marketdata.Interval1h); !got.Equal(edge) {
		t.Fatalf("first run starts %s, want the retention edge %s", got, edge)
	}
	recent := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	if got := nativeBackfillStart(now, &recent, marketdata.Interval1h); !got.Equal(recent) {
		t.Fatalf("later run starts %s, want the newest stored candle %s", got, recent)
	}
	ancient := edge.Add(-time.Hour)
	if got := nativeBackfillStart(now, &ancient, marketdata.Interval1h); !got.Equal(edge) {
		t.Fatalf("a newest candle older than retention starts %s, want the edge %s", got, edge)
	}
}

type nopLookup struct{}

func (nopLookup) GetByProviderAndSymbol(context.Context, string, string) (instruments.Instrument, error) {
	return instruments.Instrument{}, instruments.ErrNotFound
}

// slotProbeProvider fails its first native call; while each call runs it
// records whether the backfill slot is held, and after the failure a
// competitor waits for the slot.
type slotProbeProvider struct {
	gate                   chan struct{}
	mu                     sync.Mutex
	calls                  int
	heldInCall             []bool
	competitor             chan struct{} // closed once the competitor got the slot
	sawCompeteBeforeSecond bool
}

func (p *slotProbeProvider) Instruments(context.Context) ([]ingestion.ProviderInstrument, error) {
	return nil, nil
}
func (p *slotProbeProvider) HistoricalCandles(context.Context, string, time.Time, time.Time) ([]ingestion.ProviderCandle, error) {
	return nil, nil
}
func (p *slotProbeProvider) StreamCandles(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderCandle) error) error {
	<-ctx.Done()
	return nil
}
func (p *slotProbeProvider) StreamQuotes(ctx context.Context, _ string, _ func(context.Context, ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}
func (p *slotProbeProvider) HistoricalIntervalCandles(context.Context, string, marketdata.Interval, time.Time, time.Time) ([]ingestion.ProviderCandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.heldInCall = append(p.heldInCall, len(p.gate) == 1)
	if p.calls == 2 {
		select {
		case <-p.competitor:
			p.sawCompeteBeforeSecond = true
		default:
		}
	}
	if p.calls == 1 {
		// The competitor waits for the slot. A send on a full buffered
		// channel completes the moment a receive frees room, so if the
		// backfill gives the slot back before its retry delay, the
		// competitor has it before the second attempt starts.
		ready := make(chan struct{})
		go func() {
			close(ready)
			p.gate <- struct{}{}
			close(p.competitor)
			time.Sleep(20 * time.Millisecond)
			<-p.gate
		}()
		// The competitor must already be waiting on the slot when this
		// attempt returns, or the backfill can release and take it again
		// before the competitor ever runs (seen once under -race in L1.2's
		// full run: a scheduling race in this test, not in the code).
		<-ready
		time.Sleep(10 * time.Millisecond)
		return nil, errors.New("provider unavailable")
	}
	return nil, nil
}

type nilNativeState struct{}

func (nilNativeState) newestClosedAt(context.Context, marketdata.Interval, string) (int64, *time.Time, error) {
	return 1, nil, nil
}
func (nilNativeState) missingRangesAt(context.Context, marketdata.Interval, int64, time.Time, time.Time, time.Time) ([]marketdata.MissingRange, error) {
	return nil, nil
}
func (nilNativeState) recordLostGapAt(context.Context, marketdata.Interval, int64, time.Time, time.Time) error {
	return nil
}

// L1.1 decision 4: the native backfill uses the 1 minute backfill's slot
// rules: the slot is held while an attempt runs and given back before the
// retry delay (architect review of 4.1a, item 1).
func TestBackfillNative_HoldsTheSlotPerAttemptOnly(t *testing.T) {
	gate := make(chan struct{}, 1)
	p := &slotProbeProvider{gate: gate, competitor: make(chan struct{})}
	cache := ingestion.NewInstrumentCache(nopLookup{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := ingestion.NewService("hyperliquid", p, ingestion.NewNormalizer(cache, clock.Real{}), cache, nil, marketdata.ContinuousCalendar{}, logger)
	cfg := config.Config{Live: config.LiveFeed{ReconnectBackoffBase: time.Millisecond, ReconnectBackoffCap: time.Millisecond}}
	ok := backfillNativeUntilCaughtUp(context.Background(), "BTC", svc, nilNativeState{}, cfg, logger, clock.Real{}, backfillOptions{slot: gate, retryCap: time.Millisecond})
	if !ok {
		t.Fatal("backfill did not finish")
	}
	if p.calls != 4 { // 15m fails once then succeeds, 1h, 4h
		t.Fatalf("calls = %d, want 4", p.calls)
	}
	for i, held := range p.heldInCall {
		if !held {
			t.Fatalf("call %d ran without the backfill slot", i+1)
		}
	}
	if !p.sawCompeteBeforeSecond {
		t.Fatal("the slot was not given back between the failed attempt and the retry: a waiting symbol got it only afterwards")
	}
	if len(gate) != 0 {
		t.Fatal("the slot is still held after the backfill finished")
	}
}
