package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

var errOverflow = errors.New("fake sink overflow")

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- fakes -----------------------------------------------------------

type fakeInstruments struct {
	byID map[int64]instruments.Instrument
}

func (f *fakeInstruments) GetByID(_ context.Context, id int64) (instruments.Instrument, error) {
	inst, ok := f.byID[id]
	if !ok {
		return instruments.Instrument{}, instruments.ErrNotFound
	}
	return inst, nil
}

// fakeCandles is keyed by instrumentID alone, not by the exact window
// requested: marketdata's own window arithmetic is already exhaustively
// tested in internal/marketdata; these hub tests only need to control
// what AggregateReader hands back so the hub's own routing, seq and
// revision-fanout logic can be verified against a known input.
type fakeCandles struct {
	mu            sync.Mutex
	rangeResult   map[int64][]marketdata.AggregateOHLCV
	partialResult map[int64]marketdata.PartialAggregate
	latestResult  map[int64]*marketdata.AggregateOHLCV
}

func newFakeCandles() *fakeCandles {
	return &fakeCandles{
		rangeResult:   make(map[int64][]marketdata.AggregateOHLCV),
		partialResult: make(map[int64]marketdata.PartialAggregate),
		latestResult:  make(map[int64]*marketdata.AggregateOHLCV),
	}
}

func (f *fakeCandles) AggregateRange(_ context.Context, instrumentID int64, _ []marketdata.Window) ([]marketdata.AggregateOHLCV, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rangeResult[instrumentID], nil
}

func (f *fakeCandles) LatestCompleteWindow(_ context.Context, instrumentID int64, _ time.Duration) (*marketdata.AggregateOHLCV, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latestResult[instrumentID], nil
}

func (f *fakeCandles) AggregatePartial(_ context.Context, instrumentID int64, _ marketdata.Window) (marketdata.PartialAggregate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.partialResult[instrumentID], nil
}

// fakeSubscriber is an in-process stand-in for Redis pub/sub: Subscribe
// registers a channel per instrument; publish lets a test drive events
// without a real Redis connection, auto-assigning the next sequence
// number the same way RedisPublisher's own Redis INCR would.
// publishWithSeq bypasses that to simulate a gap or repeat
// deterministically, in place of what used to be a separate reconnect
// signal (review item 7, revised -- see hub.go's own handleCandleStreamLoss).
type fakeSubscriber struct {
	mu   sync.Mutex
	subs map[int64]*fakeSub
}

type fakeSub struct {
	events    chan []byte
	cancelled bool
	nextSeq   uint64
}

func newFakeSubscriber() *fakeSubscriber {
	return &fakeSubscriber{subs: make(map[int64]*fakeSub)}
}

func (f *fakeSubscriber) Subscribe(_ context.Context, instrumentID int64) (<-chan []byte, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeSub{events: make(chan []byte, 64)}
	f.subs[instrumentID] = s
	cancel := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		s.cancelled = true
	}
	return s.events, cancel, nil
}

func (f *fakeSubscriber) publish(t *testing.T, instrumentID int64, ev CandleEvent) {
	t.Helper()
	f.mu.Lock()
	s, ok := f.subs[instrumentID]
	if ok {
		s.nextSeq++
		ev.Seq = s.nextSeq
	}
	f.mu.Unlock()
	f.publishRaw(t, instrumentID, ev)
}

// publishWithSeq publishes ev with an explicit seq, bypassing
// auto-increment, so a test can simulate a gap (seq jumps ahead) or a
// repeat (seq goes backward or stays the same) deterministically.
func (f *fakeSubscriber) publishWithSeq(t *testing.T, instrumentID int64, ev CandleEvent, seq uint64) {
	t.Helper()
	ev.Seq = seq
	f.mu.Lock()
	if s, ok := f.subs[instrumentID]; ok {
		s.nextSeq = seq
	}
	f.mu.Unlock()
	f.publishRaw(t, instrumentID, ev)
}

func (f *fakeSubscriber) publishRaw(t *testing.T, instrumentID int64, ev CandleEvent) {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	f.mu.Lock()
	s, ok := f.subs[instrumentID]
	f.mu.Unlock()
	if !ok {
		t.Fatalf("publish: instrument %d has no active subscription", instrumentID)
	}
	s.events <- data
}

func (f *fakeSubscriber) isCancelled(instrumentID int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.subs[instrumentID]
	return ok && s.cancelled
}

// fakeSink records every envelope sent to it, and simulates a slow or
// disconnected client when sendErr is set.
type fakeSink struct {
	mu          sync.Mutex
	sent        []Envelope
	sendErr     error
	closed      bool
	closeCode   int
	closeReason string
}

func (s *fakeSink) Send(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendErr != nil {
		return s.sendErr
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	s.sent = append(s.sent, env)
	return nil
}

func (s *fakeSink) Close(code int, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.closeCode = code
	s.closeReason = reason
	return nil
}

func (s *fakeSink) all() []Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Envelope, len(s.sent))
	copy(out, s.sent)
	return out
}

func (s *fakeSink) byType(mt MessageType) []Envelope {
	var out []Envelope
	for _, e := range s.all() {
		if e.Type == mt {
			out = append(out, e)
		}
	}
	return out
}

// ---- test setup helpers -----------------------------------------------

func enabledInstrument(id int64) instruments.Instrument {
	return instruments.Instrument{ID: id, Enabled: true, PriceScale: 2, QtyScale: 4}
}

func newTestHub(insts *fakeInstruments, candles *fakeCandles, sub *fakeSubscriber, clk clock.Clock, cfg config.LiveFeed) *Hub {
	return New(sub, candles, insts, clk, cfg, testLogger())
}

func testCfg() config.LiveFeed {
	return config.LiveFeed{
		MaxUpdateRatePerSecond:        1000, // high, so tests don't need to wait on real coalescing timers unless testing that explicitly
		MaxSubscriptionsPerConnection: 10,
		MaxConnectionsPerUser:         5,
		MaxConnectionsPerIP:           20,
		MaxSubscribeMessagesPerSecond: 100,
	}
}

func awaitLen(t *testing.T, get func() int, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if get() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for length >= %d, got %d", want, get())
}

// ---- tests --------------------------------------------------------------

func TestSubscribe_UnknownAndDisabled_SameError(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{
		2: {ID: 2, Enabled: false},
	}}
	h := newTestHub(insts, newFakeCandles(), newFakeSubscriber(), clock.NewFixed(time.Now()), testCfg())
	c := NewConnection(&fakeSink{}, 1, "sess", "1.2.3.4", testCfg(), clock.NewFixed(time.Now()))

	err := h.Subscribe(context.Background(), c, 1, Interval1m) // unknown
	if err != ErrNotFound {
		t.Errorf("unknown instrument: err = %v, want ErrNotFound", err)
	}
	err = h.Subscribe(context.Background(), c, 2, Interval1m) // disabled
	if err != ErrNotEnabled {
		t.Errorf("disabled instrument: err = %v, want ErrNotEnabled", err)
	}
}

func TestSubscribe_Enabled_SnapshotReflectsJoinPoint(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	candles := newFakeCandles()
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	candles.latestResult[1] = &marketdata.AggregateOHLCV{
		Window: marketdata.Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(time.Minute))},
		Open:   100, High: 110, Low: 95, Close: 105, Volume: 10, Revision: 1,
	}
	h := newTestHub(insts, candles, newFakeSubscriber(), clock.NewFixed(base), testCfg())
	sink := &fakeSink{}
	c := NewConnection(sink, 1, "sess", "1.2.3.4", testCfg(), clock.NewFixed(base))

	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	snapshots := sink.byType(TypeSnapshot)
	if len(snapshots) != 1 {
		t.Fatalf("got %d snapshot messages, want 1", len(snapshots))
	}
	if snapshots[0].Seq != 1 {
		t.Errorf("snapshot seq = %d, want 1 (adr-004-ws-contract-draft.md: \"snapshot is seq 1\")", snapshots[0].Seq)
	}
	var snap SnapshotPayload
	if err := json.Unmarshal(snapshots[0].Payload, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snap.LastClosedOpenTime == nil || !snap.LastClosedOpenTime.Equal(base) {
		t.Errorf("LastClosedOpenTime = %v, want %v", snap.LastClosedOpenTime, base)
	}
	if snap.LastClosedRevision == nil || *snap.LastClosedRevision != 1 {
		t.Errorf("LastClosedRevision = %v, want 1", snap.LastClosedRevision)
	}
}

func TestRouteEvent_OneMinute_FormingThenClosed_NeverDropped(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	sub := newFakeSubscriber()
	clk := clock.NewFixed(time.Now())
	h := newTestHub(insts, newFakeCandles(), sub, clk, testCfg())
	sink := &fakeSink{}
	c := NewConnection(sink, 1, "sess", "1.2.3.4", testCfg(), clk)

	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	sub.publish(t, 1, CandleEvent{InstrumentID: 1, Kind: EventForming, OpenTime: base, CloseTime: base.Add(time.Minute), Open: 100, High: 105, Low: 100, Close: 103, Volume: 5, Revision: 1})
	sub.publish(t, 1, CandleEvent{InstrumentID: 1, Kind: EventClosed, OpenTime: base, CloseTime: base.Add(time.Minute), Open: 100, High: 110, Low: 100, Close: 108, Volume: 10, Revision: 1})

	awaitLen(t, func() int { return len(sink.byType(TypeCandleClosed)) }, 1, 2*time.Second)

	forming := sink.byType(TypeCandle)
	closed := sink.byType(TypeCandleClosed)
	if len(forming) != 1 {
		t.Fatalf("got %d forming messages, want 1", len(forming))
	}
	if len(closed) != 1 {
		t.Fatalf("got %d closed messages, want 1", len(closed))
	}
	// Seq 1 was already consumed by the subscribe-time snapshot itself
	// (adr-004-ws-contract-draft.md: "snapshot is seq 1"); the forming
	// update is the second message actually written to this
	// subscription, not the first.
	if forming[0].Seq != 2 {
		t.Errorf("forming seq = %d, want 2 (seq 1 is the subscribe-time snapshot)", forming[0].Seq)
	}
	if closed[0].Seq != 3 {
		t.Errorf("closed seq = %d, want 3 (consecutive after the snapshot and the forming send)", closed[0].Seq)
	}
}

func TestCoalescing_RapidFormingUpdates_OnlyLatestSent(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	sub := newFakeSubscriber()
	clk := clock.NewFixed(time.Now())
	cfg := testCfg()
	cfg.MaxUpdateRatePerSecond = 2 // 500ms gap between sends
	h := newTestHub(insts, newFakeCandles(), sub, clk, cfg)
	sink := &fakeSink{}
	c := NewConnection(sink, 1, "sess", "1.2.3.4", cfg, clk)

	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	for i, closePrice := range []quant.Price{101, 102, 103, 104, 105} {
		sub.publish(t, 1, CandleEvent{InstrumentID: 1, Kind: EventForming, OpenTime: base, CloseTime: base.Add(time.Minute), Open: 100, High: closePrice, Low: 100, Close: closePrice, Volume: quant.Qty(i), Revision: 1})
	}

	// The first one sends immediately (no prior send yet); the rest
	// coalesce into one pending slot and flush together once the rate
	// window elapses.
	awaitLen(t, func() int { return len(sink.byType(TypeCandle)) }, 2, 2*time.Second)
	time.Sleep(50 * time.Millisecond) // let any extra, unwanted sends surface
	forming := sink.byType(TypeCandle)
	if len(forming) != 2 {
		t.Fatalf("got %d forming messages, want exactly 2 (one immediate, one coalesced flush), got seqs: %+v", len(forming), forming)
	}
	var payload CandlePayload
	if err := json.Unmarshal(forming[len(forming)-1].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Close != quant.Price(105).String(2) {
		t.Errorf("flushed Close = %q, want the LATEST value %q (coalescing keeps the last value)", payload.Close, quant.Price(105).String(2))
	}
	// Seq 1 is the subscribe-time snapshot (adr-004-ws-contract-draft.md:
	// "snapshot is seq 1"); the two forming sends are seq 2 and 3.
	if forming[0].Seq != 2 || forming[1].Seq != 3 {
		t.Errorf("seqs = %d,%d, want 2,3 (consecutive: a coalesced-away update never consumes a seq)", forming[0].Seq, forming[1].Seq)
	}
}

func TestDerivedInterval_CorrectionFansOutWithRealRevision(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	candles := newFakeCandles()
	sub := newFakeSubscriber()
	clk := clock.NewFixed(time.Now())
	h := newTestHub(insts, candles, sub, clk, testCfg())
	sink := &fakeSink{}
	c := NewConnection(sink, 1, "sess", "1.2.3.4", testCfg(), clk)

	if err := h.Subscribe(context.Background(), c, 1, Interval5m); err != nil {
		t.Fatalf("Subscribe 5m: %v", err)
	}

	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	candles.rangeResult[1] = []marketdata.AggregateOHLCV{{
		Window: marketdata.Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(5 * time.Minute))},
		Open:   100, High: 120, Low: 95, Close: 115, Volume: 50, Revision: 2, // the derived revision the fake stands in for AggregateRange computing
	}}

	sub.publish(t, 1, CandleEvent{InstrumentID: 1, Kind: EventCorrected, OpenTime: base.Add(2 * time.Minute), CloseTime: base.Add(3 * time.Minute), Open: 100, High: 120, Low: 95, Close: 115, Volume: 20, Revision: 2})

	awaitLen(t, func() int { return len(sink.byType(TypeCandleCorrected)) }, 1, 2*time.Second)
	corrected := sink.byType(TypeCandleCorrected)
	var payload CandlePayload
	if err := json.Unmarshal(corrected[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Revision != 2 {
		t.Errorf("derived candle_corrected revision = %d, want 2 (the real AggregateRange-computed value, never a fixed 1)", payload.Revision)
	}
}

// TestSequenceGap_SendsDegradedThenFreshSnapshot is review item 7,
// revised: recovery is driven entirely by a gap in CandleEvent.Seq, not
// by any signal from the Subscriber about its own connection (a real
// repro against Redis showed that signal is not reliable -- see
// hub.go's own handleCandleStreamLoss and redis.go's RedisSubscriber
// doc comment). publishWithSeq simulates exactly what a dropped-and-
// recovered connection looks like from the hub's side: the next event
// it sees does not continue the sequence it last saw.
func TestSequenceGap_SendsDegradedThenFreshSnapshot(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	candles := newFakeCandles()
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	candles.latestResult[1] = &marketdata.AggregateOHLCV{
		Window: marketdata.Window{Start: clock.NewEventTime(base), End: clock.NewEventTime(base.Add(time.Minute))},
		Open:   100, High: 110, Low: 95, Close: 105, Volume: 10, Revision: 1,
	}
	sub := newFakeSubscriber()
	clk := clock.NewFixed(base)
	h := newTestHub(insts, candles, sub, clk, testCfg())
	sink := &fakeSink{}
	c := NewConnection(sink, 1, "sess", "1.2.3.4", testCfg(), clk)

	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	awaitLen(t, func() int { return len(sink.byType(TypeSnapshot)) }, 1, 2*time.Second) // the normal subscribe-time snapshot, seq 1

	// seq 1 arrives normally.
	sub.publish(t, 1, CandleEvent{InstrumentID: 1, Kind: EventForming, OpenTime: base, CloseTime: base.Add(time.Minute), Open: 100, High: 105, Low: 100, Close: 103, Volume: 5, Revision: 1})
	awaitLen(t, func() int { return len(sink.byType(TypeCandle)) }, 1, 2*time.Second)

	// seq jumps from 1 to 5: three events (2, 3, 4) were lost between
	// the worker's publish and the hub's own subscription -- exactly
	// what killing and restarting the underlying connection, or any
	// other transit loss, looks like from here.
	sub.publishWithSeq(t, 1, CandleEvent{InstrumentID: 1, Kind: EventClosed, OpenTime: base, CloseTime: base.Add(time.Minute), Open: 100, High: 110, Low: 100, Close: 108, Volume: 10, Revision: 1}, 5)

	awaitLen(t, func() int { return len(sink.byType(TypeSnapshot)) }, 2, 2*time.Second) // the subscribe-time one, plus the recovery one
	degraded := sink.byType(TypeStatus)
	if len(degraded) != 1 {
		t.Fatalf("got %d status messages, want 1 degraded notice", len(degraded))
	}
	var statusPayload StatusPayload
	if err := json.Unmarshal(degraded[0].Payload, &statusPayload); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if statusPayload.State != "degraded" {
		t.Errorf("status state = %q, want degraded", statusPayload.State)
	}

	snapshots := sink.byType(TypeSnapshot)
	recovery := snapshots[len(snapshots)-1]
	if recovery.Seq != 1 {
		t.Errorf("recovery snapshot seq = %d, want 1 (resetSubscription starts a fresh sequence, and the snapshot itself is always seq 1)", recovery.Seq)
	}

	// The event that caused the gap (seq 5) is not separately
	// delivered on top of the recovery snapshot -- the snapshot's own
	// database read already reflects it (see handleCandleStreamLoss's
	// own doc comment) -- so the next NORMAL event continues from the
	// recovery snapshot's own seq 1, landing on seq 2.
	sub.publish(t, 1, CandleEvent{InstrumentID: 1, Kind: EventClosed, OpenTime: base.Add(time.Minute), CloseTime: base.Add(2 * time.Minute), Open: 100, High: 110, Low: 100, Close: 108, Volume: 10, Revision: 1})
	awaitLen(t, func() int { return len(sink.byType(TypeCandleClosed)) }, 1, 2*time.Second)
	closed := sink.byType(TypeCandleClosed)
	if len(closed) != 1 {
		t.Fatalf("got %d candle_closed messages, want exactly 1 (the seq=5 event that caused the gap must not be separately delivered)", len(closed))
	}
	if closed[0].Seq != 2 {
		t.Errorf("seq after recovery = %d, want 2 (consecutive after the recovery snapshot's own seq 1)", closed[0].Seq)
	}
}

func TestSubscribe_PerConnectionLimitEnforced(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{
		1: enabledInstrument(1), 2: enabledInstrument(2),
	}}
	cfg := testCfg()
	cfg.MaxSubscriptionsPerConnection = 1
	h := newTestHub(insts, newFakeCandles(), newFakeSubscriber(), clock.NewFixed(time.Now()), cfg)
	c := NewConnection(&fakeSink{}, 1, "sess", "1.2.3.4", cfg, clock.NewFixed(time.Now()))

	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatalf("first subscribe: %v", err)
	}
	// Re-subscribing to the SAME key must not be blocked by the limit.
	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Errorf("re-subscribe to an already-held key: %v", err)
	}
	// A genuinely new subscription beyond the limit must be refused.
	if err := h.Subscribe(context.Background(), c, 2, Interval1m); !errors.Is(err, ErrTooManySubscriptions) {
		t.Errorf("subscribe beyond the limit: err = %v, want ErrTooManySubscriptions", err)
	}
}

func TestUnsubscribe_UnheldKey_IsNoOp(_ *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	h := newTestHub(insts, newFakeCandles(), newFakeSubscriber(), clock.NewFixed(time.Now()), testCfg())
	c := NewConnection(&fakeSink{}, 1, "sess", "1.2.3.4", testCfg(), clock.NewFixed(time.Now()))

	h.Unsubscribe(c, 1, Interval1m) // never subscribed -- must not panic or error
}

func TestUnsubscribe_ReleasesRedisSubscriptionWhenLastGone(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	sub := newFakeSubscriber()
	h := newTestHub(insts, newFakeCandles(), sub, clock.NewFixed(time.Now()), testCfg())
	c := NewConnection(&fakeSink{}, 1, "sess", "1.2.3.4", testCfg(), clock.NewFixed(time.Now()))

	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub.isCancelled(1) {
		t.Fatal("redis subscription cancelled while a connection still holds it")
	}
	h.Unsubscribe(c, 1, Interval1m)
	if !sub.isCancelled(1) {
		t.Error("redis subscription not released after the last subscriber unsubscribed")
	}
}

func TestRegister_ConnectionLimitsEnforced(t *testing.T) {
	insts := &fakeInstruments{}
	cfg := testCfg()
	cfg.MaxConnectionsPerUser = 1
	h := newTestHub(insts, newFakeCandles(), newFakeSubscriber(), clock.NewFixed(time.Now()), cfg)

	c1 := NewConnection(&fakeSink{}, 7, "sess1", "1.2.3.4", cfg, clock.NewFixed(time.Now()))
	c2 := NewConnection(&fakeSink{}, 7, "sess2", "9.9.9.9", cfg, clock.NewFixed(time.Now()))

	if err := h.Register(c1); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := h.Register(c2); err == nil {
		t.Error("second connection for the same user over MaxConnectionsPerUser=1 should be refused")
	}

	h.Unregister(c1)
	if err := h.Register(c2); err != nil {
		t.Errorf("registration after releasing the slot: %v", err)
	}
}

func TestSlowConsumer_SendFailureClosesConnection(t *testing.T) {
	insts := &fakeInstruments{byID: map[int64]instruments.Instrument{1: enabledInstrument(1)}}
	sub := newFakeSubscriber()
	h := newTestHub(insts, newFakeCandles(), sub, clock.NewFixed(time.Now()), testCfg())
	sink := &fakeSink{sendErr: errOverflow}
	c := NewConnection(sink, 1, "sess", "1.2.3.4", testCfg(), clock.NewFixed(time.Now()))

	if err := h.Subscribe(context.Background(), c, 1, Interval1m); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	sub.publish(t, 1, CandleEvent{InstrumentID: 1, Kind: EventClosed, OpenTime: base, CloseTime: base.Add(time.Minute), Open: 100, High: 110, Low: 100, Close: 108, Volume: 10, Revision: 1})

	awaitLen(t, func() int {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		if sink.closed {
			return 1
		}
		return 0
	}, 1, 2*time.Second)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.closeCode != CloseSlowConsumer {
		t.Errorf("close code = %d, want %d", sink.closeCode, CloseSlowConsumer)
	}
}

func TestConnection_SubscribeRateLimit(t *testing.T) {
	cfg := testCfg()
	cfg.MaxSubscribeMessagesPerSecond = 2
	clk := clock.NewMutable(time.Now())
	c := NewConnection(&fakeSink{}, 1, "sess", "1.2.3.4", cfg, clk)

	if !c.CheckSubscribeRate() {
		t.Fatal("1st message should be within the rate limit")
	}
	if !c.CheckSubscribeRate() {
		t.Fatal("2nd message should be within the rate limit")
	}
	if c.CheckSubscribeRate() {
		t.Fatal("3rd message within the same second should exceed the rate limit")
	}

	clk.Advance(2 * time.Second)
	if !c.CheckSubscribeRate() {
		t.Error("after the window elapses, the rate limit should reset")
	}
}
