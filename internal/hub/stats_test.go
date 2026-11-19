package hub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// fakeStats returns a forming-anchor Stats whose base is fixed per
// instrument, and counts reads, so a test can tell a per-tick Apply from
// a database re-read.
type fakeStats struct {
	mu     sync.Mutex
	reads  map[int64]int
	anchor time.Time
	close  quant.Price
	err    error
}

func newFakeStats(anchor time.Time) *fakeStats {
	return &fakeStats{reads: make(map[int64]int), anchor: anchor, close: 8000000}
}

func (f *fakeStats) base() marketdata.LiveBase {
	return marketdata.LiveBase{
		AnchorOpenTime:    clock.NewEventTime(f.anchor),
		ReferenceOpenTime: clock.NewEventTime(f.anchor.Add(-24*time.Hour - time.Minute)),
		ReferenceFound:    true,
		ReferenceClose:    8000000, // 80000.00
		PriorCount:        1439,
		PriorVolume:       10000, // 1.0000 at qty scale 4
	}
}

func (f *fakeStats) Stats(_ context.Context, id int64) (marketdata.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[id]++
	if f.err != nil {
		return marketdata.Stats{}, f.err
	}
	b := f.base()
	live, err := b.Apply(f.close, 5, clock.NewIngestionTime(f.anchor.Add(time.Second)))
	if err != nil {
		return marketdata.Stats{}, err
	}
	return marketdata.Stats{InstrumentID: id, Live: &live, Base: &b}, nil
}

func (f *fakeStats) readCount(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[id]
}

func statsCfg(minInterval time.Duration) config.LiveFeed {
	cfg := testCfg()
	cfg.StatsMinInterval = minInterval
	cfg.MaxStatsInstrumentsPerConnection = 50
	return cfg
}

func statsPayloads(t *testing.T, s *fakeSink) []StatsPayload {
	t.Helper()
	var out []StatsPayload
	for _, env := range s.byType(TypeStats) {
		var p StatsPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if env.Seq != 0 {
			t.Fatalf("stats envelope carries seq %d, want none", env.Seq)
		}
		if env.Sub != StatsSub(p.InstrumentID) {
			t.Fatalf("stats envelope sub %q for instrument %d", env.Sub, p.InstrumentID)
		}
		out = append(out, p)
	}
	return out
}

type statsRig struct {
	h     *Hub
	sub   *fakeSubscriber
	stats *fakeStats
	insts *fakeInstruments
	t0    time.Time
}

func newStatsRig(t *testing.T, minInterval time.Duration, ids ...int64) statsRig {
	t.Helper()
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{}}
	for _, id := range ids {
		insts.byID[id] = enabledInstrument(id)
	}
	t0 := time.Date(2026, 11, 18, 12, 1, 0, 0, time.UTC)
	sub := newFakeSubscriber()
	st := newFakeStats(t0)
	h := newTestHub(insts, newFakeCandles(), sub, clock.Real{}, statsCfg(minInterval))
	h.EnableStats(st)
	return statsRig{h: h, sub: sub, stats: st, insts: insts, t0: t0}
}

func (r statsRig) connect(t *testing.T) (*Connection, *fakeSink) {
	t.Helper()
	sink := &fakeSink{}
	c := NewConnection(sink, 1, "sess", "1.2.3.4", statsCfg(r.h.cfg.StatsMinInterval), clock.Real{})
	if err := r.h.Register(c); err != nil {
		t.Fatal(err)
	}
	return c, sink
}

func (r statsRig) forming(id int64, price quant.Price, volume quant.Qty, at time.Duration) CandleEvent {
	return CandleEvent{
		InstrumentID: id, Kind: EventForming,
		OpenTime: r.t0, CloseTime: r.t0.Add(time.Minute),
		Open: price, High: price, Low: price, Close: price, Volume: volume, Revision: 1,
		IngestedAt: r.t0.Add(at),
	}
}

func TestSubscribeStats_InitialFiguresAndRejections(t *testing.T) {
	r := newStatsRig(t, 50*time.Millisecond, 1, 2)
	r.insts.byID[3] = instruments.Instrument{ID: 3, Enabled: false}
	c, sink := r.connect(t)

	rejected, err := r.h.SubscribeStats(context.Background(), c, []int64{1, 2, 3, 99, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 2 || rejected[0] != 3 || rejected[1] != 99 {
		t.Fatalf("rejected = %v, want [3 99] (disabled and unknown alike)", rejected)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 2, time.Second)
	ps := statsPayloads(t, sink)
	if ps[0].LastPrice == nil || *ps[0].LastPrice != "80000.00" || *ps[0].Change24h != "0.00" || *ps[0].Volume24h != "1.0005" {
		t.Fatalf("initial figures = %+v", ps[0])
	}
	if c.StatsSubscriptionCount() != 2 {
		t.Fatalf("stats subscriptions = %d, want 2", c.StatsSubscriptionCount())
	}
}

func TestSubscribeStats_OverLimitChangesNothing(t *testing.T) {
	r := newStatsRig(t, 50*time.Millisecond, 1)
	r.h.cfg.MaxStatsInstrumentsPerConnection = 3
	c, _ := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1, 2, 3, 4}); !errors.Is(err, ErrTooManyStatsInstruments) {
		t.Fatalf("err = %v, want ErrTooManyStatsInstruments", err)
	}
	if c.StatsSubscriptionCount() != 0 {
		t.Fatalf("stats subscriptions = %d after a rejected list, want 0", c.StatsSubscriptionCount())
	}
}

func TestSubscribeStats_NotEnabled(t *testing.T) {
	h := newTestHub(&fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}, newFakeCandles(), newFakeSubscriber(), clock.Real{}, statsCfg(time.Second))
	c := NewConnection(&fakeSink{}, 1, "s", "ip", statsCfg(time.Second), clock.Real{})
	if _, err := h.SubscribeStats(context.Background(), c, []int64{1}); !errors.Is(err, ErrStatsUnavailable) {
		t.Fatalf("err = %v, want ErrStatsUnavailable", err)
	}
}

// Forming ticks of the anchor minute are applied to the cached base: no
// database read per tick, and the figures follow the tick.
func TestStats_FormingTicksUseCachedBase(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1)
	c, sink := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
		t.Fatal(err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 1, time.Second)

	// 80120.00, volume 0.0025: +120.00, 120/80000 = 0.15%, 1.0000+0.0025.
	r.sub.publish(t, 1, r.forming(1, 8012000, 25, 2*time.Second))
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 2, time.Second)
	got := statsPayloads(t, sink)[1]
	if *got.LastPrice != "80120.00" || *got.Change24h != "120.00" || *got.Change24hPct != "0.15" || *got.Volume24h != "1.0025" {
		t.Fatalf("after tick = %+v", got)
	}
	if !got.LastTickAt.Equal(r.t0.Add(2 * time.Second)) {
		t.Fatalf("last_tick_at = %v, want the event's ingestion time", got.LastTickAt)
	}
	if n := r.stats.readCount(1); n != 1 {
		t.Fatalf("database reads = %d, want 1 (the initial base only)", n)
	}
}

// A close (or correction, or backfilled minute) can change the base: the
// next flush re-reads it.
func TestStats_ClosedEventRebases(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1)
	c, sink := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
		t.Fatal(err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 1, time.Second)
	ev := r.forming(1, 8012000, 25, 2*time.Second)
	ev.Kind = EventClosed
	r.sub.publish(t, 1, ev)
	awaitLen(t, func() int { return r.stats.readCount(1) }, 2, time.Second)
}

// A tick that changes nothing (same price, volume and tick time) sends
// nothing.
func TestStats_UnchangedSendsNothing(t *testing.T) {
	r := newStatsRig(t, 10*time.Millisecond, 1)
	c, sink := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
		t.Fatal(err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 1, time.Second)
	// Exactly the base's own values: 80000.00, volume 5, tick at t0+1s.
	r.sub.publish(t, 1, r.forming(1, 8000000, 5, time.Second))
	time.Sleep(100 * time.Millisecond)
	if n := len(sink.byType(TypeStats)); n != 1 {
		t.Fatalf("stats messages = %d, want 1 (the unchanged tick sends nothing)", n)
	}
}

// A burst of ticks coalesces: at most one message per StatsMinInterval,
// and the last one carries the newest tick.
func TestStats_BurstCoalesces(t *testing.T) {
	const interval = 100 * time.Millisecond
	r := newStatsRig(t, interval, 1)
	c, sink := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
		t.Fatal(err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 1, time.Second)

	start := time.Now()
	const ticks = 200
	for i := range ticks {
		r.sub.publish(t, 1, r.forming(1, quant.Price(8000001+i), quant.Qty(6+i), time.Duration(2+i)*time.Millisecond))
		time.Sleep(time.Millisecond)
	}
	elapsed := time.Since(start)
	time.Sleep(3 * interval)

	ps := statsPayloads(t, sink)[1:]
	maxAllowed := int(elapsed/interval) + 2
	if len(ps) == 0 || len(ps) > maxAllowed {
		t.Fatalf("%d ticks over %v produced %d messages, want 1..%d", ticks, elapsed, len(ps), maxAllowed)
	}
	last := ps[len(ps)-1]
	if *last.LastPrice != quant.Price(8000001+ticks-1).String(2) {
		t.Fatalf("last message price %s, want the newest tick's", *last.LastPrice)
	}
	t.Logf("%d ticks over %v -> %d stats messages", ticks, elapsed, len(ps))
}

// Every subscriber of an instrument gets the same figures from ONE
// computation: reads do not grow with the number of connections.
func TestStats_FanOutComputesOncePerInstrument(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1)
	// Twenty connections from one user and IP: raise testCfg's limits.
	r.h.cfg.MaxConnectionsPerUser = 100
	r.h.cfg.MaxConnectionsPerIP = 100
	conns := make([]*Connection, 0, 20)
	sinks := make([]*fakeSink, 0, 20)
	for range 20 {
		c, sink := r.connect(t)
		conns = append(conns, c)
		sinks = append(sinks, sink)
	}
	for _, c := range conns {
		if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range sinks {
		awaitLen(t, func() int { return len(s.byType(TypeStats)) }, 1, time.Second)
	}
	readsBefore := r.stats.readCount(1)
	r.sub.publish(t, 1, r.forming(1, 8012000, 25, 2*time.Second))
	for _, s := range sinks {
		awaitLen(t, func() int { return len(s.byType(TypeStats)) }, 2, time.Second)
	}
	if n := r.stats.readCount(1); n != readsBefore {
		t.Fatalf("database reads grew from %d to %d for one forming tick across 20 connections, want no read", readsBefore, n)
	}
}

// Replace semantics: a later subscribe_stats drops ids not in it, and
// the instrument's Redis channel is released with its last reference.
func TestSubscribeStats_ReplaceReleasesDroppedIDs(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1, 2)
	c, sink := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 2, time.Second)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{2}); err != nil {
		t.Fatal(err)
	}
	if c.StatsSubscriptionCount() != 1 || !r.sub.isCancelled(1) || r.sub.isCancelled(2) {
		t.Fatalf("after replace: count %d, redis 1 cancelled %v, redis 2 cancelled %v", c.StatsSubscriptionCount(), r.sub.isCancelled(1), r.sub.isCancelled(2))
	}
	r.h.UnsubscribeStats(c)
	if c.StatsSubscriptionCount() != 0 || !r.sub.isCancelled(2) {
		t.Fatalf("after unsubscribe_stats: count %d, redis 2 cancelled %v", c.StatsSubscriptionCount(), r.sub.isCancelled(2))
	}
}

// A stats subscription and a candle subscription share the instrument's
// Redis channel; dropping one keeps it open for the other.
func TestStats_SharesRedisReferenceWithCandles(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1)
	c, _ := r.connect(t)
	if err := r.h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatal(err)
	}
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
		t.Fatal(err)
	}
	r.h.UnsubscribeStats(c)
	if r.sub.isCancelled(1) {
		t.Fatal("redis channel cancelled while a candle subscription still needs it")
	}
	r.h.Unregister(c)
	if !r.sub.isCancelled(1) {
		t.Fatal("redis channel still open after the connection left")
	}
}

// A client that cannot take a message is dropped, never buffered, and
// the other subscribers keep receiving.
func TestStats_SlowConsumerIsDropped(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1)
	slow, slowSink := r.connect(t)
	fast, fastSink := r.connect(t)
	for _, c := range []*Connection{slow, fast} {
		if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
			t.Fatal(err)
		}
	}
	awaitLen(t, func() int { return len(fastSink.byType(TypeStats)) }, 1, time.Second)
	slowSink.mu.Lock()
	slowSink.sendErr = errOverflow
	slowSink.mu.Unlock()

	r.sub.publish(t, 1, r.forming(1, 8012000, 25, 2*time.Second))
	awaitLen(t, func() int { return len(fastSink.byType(TypeStats)) }, 2, time.Second)
	// Fan-out order is map order: the slow sink may be written after
	// the fast one, so wait for its close rather than check once.
	awaitLen(t, func() int {
		if slow.Closed() {
			return 1
		}
		return 0
	}, 1, time.Second)
	slowSink.mu.Lock()
	code := slowSink.closeCode
	slowSink.mu.Unlock()
	if code != CloseSlowConsumer {
		t.Fatalf("close code = %d, want %d", code, CloseSlowConsumer)
	}
}

// A sequence gap means events were lost: the next flush re-reads the
// base from the database.
func TestStats_SequenceGapRebases(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1)
	c, sink := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
		t.Fatal(err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 1, time.Second)
	r.sub.publish(t, 1, r.forming(1, 8012000, 25, 2*time.Second))
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 2, time.Second)
	reads := r.stats.readCount(1)
	r.sub.publishWithSeq(t, 1, r.forming(1, 8013000, 26, 3*time.Second), 10)
	awaitLen(t, func() int { return r.stats.readCount(1) }, reads+1, time.Second)
}

// A worker that predates CandleEvent.IngestedAt sends a zero time: the
// hub reads the figures from the database rather than show a zero time.
func TestStats_ZeroIngestedAtReadsDatabase(t *testing.T) {
	r := newStatsRig(t, 20*time.Millisecond, 1)
	c, sink := r.connect(t)
	if _, err := r.h.SubscribeStats(context.Background(), c, []int64{1}); err != nil {
		t.Fatal(err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeStats)) }, 1, time.Second)
	ev := r.forming(1, 8012000, 25, 0)
	ev.IngestedAt = time.Time{}
	r.sub.publish(t, 1, ev)
	awaitLen(t, func() int { return r.stats.readCount(1) }, 2, time.Second)
}
