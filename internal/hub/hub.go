package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// scaleFor looks up instrumentID's own price/qty scale, needed to
// render any candle payload as the decimal strings rule 3 requires.
// Re-fetched per call rather than cached: simpler, and
// instruments.Store's own GetByID is already a cheap, frequently-hit
// read; revisit with a cache only if profiling ever shows this matters
// at real subscriber volumes.
func (h *Hub) scaleFor(ctx context.Context, instrumentID int64) (quant.Scale, quant.Scale, error) {
	inst, err := h.instruments.GetByID(ctx, instrumentID)
	if err != nil {
		return 0, 0, fmt.Errorf("hub: get instrument %d scale: %w", instrumentID, err)
	}
	return inst.PriceScale, inst.QtyScale, nil
}

// AggregateReader is the candle-read boundary Hub needs -- satisfied
// directly by *marketdata.Store, a consumer-defined interface (guide 4,
// 10) so a test can fake it without a real Postgres connection.
type AggregateReader interface {
	AggregateRange(ctx context.Context, instrumentID int64, windows []marketdata.Window) ([]marketdata.AggregateOHLCV, error)
	LatestCompleteWindow(ctx context.Context, instrumentID int64, d time.Duration) (*marketdata.AggregateOHLCV, error)
	AggregatePartial(ctx context.Context, instrumentID int64, w marketdata.Window) (marketdata.PartialAggregate, error)
}

// InstrumentChecker is the enabled-check boundary (brief: "Only
// instruments the user may see (enabled) can be subscribed. Check it on
// every subscribe") -- satisfied directly by *instruments.Store.
type InstrumentChecker interface {
	GetByID(ctx context.Context, id int64) (instruments.Instrument, error)
}

// Subscriber is the hub's own boundary onto Redis pub/sub for one
// instrument's channel -- a consumer-defined interface so a test can
// fake it without a real Redis connection. events never closes on its
// own while ctx is live. Deliberately minimal: loss detection
// (adr-004-ws-contract-review.md review item 7) is driven entirely by
// CandleEvent.Seq, not by anything this interface's own implementation
// does or does not observe about the underlying connection -- an
// earlier version added a second channel for exactly that, and a real
// repro (CLIENT KILL TYPE pubsub against a connected, subscribed
// client) showed it never fired.
type Subscriber interface {
	Subscribe(ctx context.Context, instrumentID int64) (events <-chan []byte, cancel func(), err error)
}

// ErrNotEnabled and ErrNotFound are both reported to the client as the
// same generic error code (review's own "smaller corrections": an
// unknown instrument and a disabled one must not be distinguishable,
// or a client could enumerate which ids exist by probing).
var (
	ErrNotFound   = errors.New("hub: instrument not found")
	ErrNotEnabled = errors.New("hub: instrument not enabled")
)

// subscriptionKey identifies one (instrument, interval) subscription.
type subscriptionKey struct {
	InstrumentID int64
	Interval     Interval
}

func (k subscriptionKey) subString() string {
	return fmt.Sprintf("%d:%s", k.InstrumentID, k.Interval)
}

// Hub fans out candle changes (published by the worker on a per-
// instrument Redis channel) to every WebSocket connection with a
// matching subscription. One Hub per API process.
type Hub struct {
	subscriber  Subscriber
	candles     AggregateReader
	instruments InstrumentChecker
	clk         clock.Clock
	cfg         config.LiveFeed
	logger      *slog.Logger

	mu sync.Mutex
	// conns is every registered connection, by pointer identity.
	conns map[*Connection]struct{}
	// instrumentConns maps instrumentID -> the set of connections
	// subscribed to ANY interval of it, and which intervals each one
	// holds -- an incoming 1-minute CandleEvent for instrumentID is
	// routed to every one of these, recomputing per interval as needed.
	instrumentConns map[int64]map[*Connection]map[Interval]struct{}
	// redisCancel/redisRefCount: subscribe to an instrument's own Redis
	// channel only while at least one connection cares about it.
	redisCancel   map[int64]func()
	redisRefCount map[int64]int

	connsByUser map[int64]int
	connsByIP   map[string]int
}

// New wires a Hub. clk/cfg/logger must not be nil; cfg is read fresh on
// every access (no snapshot at construction), so a config reload (not
// built in this phase) would take effect without restarting the hub --
// not relied upon today, just not precluded.
func New(subscriber Subscriber, candles AggregateReader, insts InstrumentChecker, clk clock.Clock, cfg config.LiveFeed, logger *slog.Logger) *Hub {
	return &Hub{
		subscriber:      subscriber,
		candles:         candles,
		instruments:     insts,
		clk:             clk,
		cfg:             cfg,
		logger:          logger,
		conns:           make(map[*Connection]struct{}),
		instrumentConns: make(map[int64]map[*Connection]map[Interval]struct{}),
		redisCancel:     make(map[int64]func()),
		redisRefCount:   make(map[int64]int),
		connsByUser:     make(map[int64]int),
		connsByIP:       make(map[string]int),
	}
}

// ErrTooManyConnections is returned by Register when a connection limit
// (review item 8, config.LiveFeed.MaxConnectionsPer{User,IP}) is already
// at capacity -- the caller (the /ws handler) must refuse the upgrade or
// close the freshly accepted connection immediately, never silently
// admit it over the limit.
var ErrTooManyConnections = errors.New("hub: too many connections")

// ErrTooManySubscriptions is returned by Subscribe when adding a new
// (not already held) subscription would exceed
// config.LiveFeed.MaxSubscriptionsPerConnection.
var ErrTooManySubscriptions = errors.New("hub: too many subscriptions on this connection")

// Register admits a new connection, enforcing the per-user/per-IP
// connection limits (review item 8). Call Unregister exactly once, when
// the connection ends, regardless of why.
func (h *Hub) Register(c *Connection) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connsByUser[c.UserID] >= h.cfg.MaxConnectionsPerUser {
		return fmt.Errorf("%w: user %d already has %d connections", ErrTooManyConnections, c.UserID, h.connsByUser[c.UserID])
	}
	if h.connsByIP[c.RemoteIP] >= h.cfg.MaxConnectionsPerIP {
		return fmt.Errorf("%w: ip %s already has %d connections", ErrTooManyConnections, c.RemoteIP, h.connsByIP[c.RemoteIP])
	}
	h.conns[c] = struct{}{}
	h.connsByUser[c.UserID]++
	h.connsByIP[c.RemoteIP]++
	return nil
}

// Unregister removes c and every subscription it held, releasing the
// per-instrument Redis subscription once nothing references it anymore.
func (h *Hub) Unregister(c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.conns[c]; !ok {
		return
	}
	delete(h.conns, c)
	h.connsByUser[c.UserID]--
	if h.connsByUser[c.UserID] <= 0 {
		delete(h.connsByUser, c.UserID)
	}
	h.connsByIP[c.RemoteIP]--
	if h.connsByIP[c.RemoteIP] <= 0 {
		delete(h.connsByIP, c.RemoteIP)
	}

	c.mu.Lock()
	keys := make([]subscriptionKey, 0, len(c.subs))
	for k := range c.subs {
		keys = append(keys, k)
	}
	c.mu.Unlock()
	for _, k := range keys {
		h.removeSubscriptionLocked(c, k)
	}
}

// Subscribe handles one `subscribe` message: the enabled check (every
// time, brief rule), idempotent re-subscribe (a fresh snapshot, seq
// reset), and the Redis reference-count bookkeeping. Sends the
// snapshot itself, through c (see the doc comment on why: this used to
// be the caller's job and that was the bug behind the snapshot-is-seq-0
// report). Returns an error (ErrNotFound/ErrNotEnabled, both reported
// identically to the client -- see the caller) without sending
// anything.
func (h *Hub) Subscribe(ctx context.Context, c *Connection, instrumentID int64, interval Interval) error {
	key := subscriptionKey{InstrumentID: instrumentID, Interval: interval}
	if !c.hasSubscription(key) && c.SubscriptionCount() >= h.cfg.MaxSubscriptionsPerConnection {
		return fmt.Errorf("%w: connection already holds %d subscriptions", ErrTooManySubscriptions, c.SubscriptionCount())
	}

	inst, err := h.instruments.GetByID(ctx, instrumentID)
	if errors.Is(err, instruments.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("hub: get instrument %d: %w", instrumentID, err)
	}
	if !inst.Enabled {
		return ErrNotEnabled
	}

	d, ok := interval.Duration()
	if !ok {
		return fmt.Errorf("hub: interval %q has no known duration", interval)
	}

	snapshot, err := h.buildSnapshot(ctx, instrumentID, interval, d)
	if err != nil {
		return err
	}

	h.mu.Lock()
	alreadyHeld := c.hasSubscription(key)
	c.resetSubscription(key)
	if !alreadyHeld {
		h.addSubscriptionLocked(c, key)
	}
	h.mu.Unlock()

	// resetSubscription above zeroed this subscription's own seq
	// counter; sending the snapshot through c.sendSnapshot (not a
	// hand-built Envelope) is what actually assigns it seq 1, matching
	// adr-004-ws-contract-draft.md's own "snapshot is seq 1" -- the
	// first live update that follows is then seq 2, never seq 1 again.
	c.sendSnapshot(key, snapshot, h.clk.Now())
	h.sendLiveStatus(c, key, inst.Provider, snapshot, d, h.clk.Now())
	return nil
}

// sendLiveStatus is this subscription's own "live" status, sent right
// after its snapshot -- never a time-based staleness judgment, which
// nothing in this project has a decided numeric threshold for
// (appendix-c-unresolved-decisions.md: "Freshness and latency
// thresholds... per-market SLOs" is explicitly still open; ADR-006:
// "Freshness targets are market-specific and must be defined
// numerically before release"). "live" here means exactly what is
// observable without inventing one: this subscription just got a real
// snapshot back from the store, and nothing is currently known to be
// degraded. LastEventAt is the most recent candle boundary this hub
// actually knows about (the snapshot's own join point, one interval
// later -- the forming candle's own open boundary), not a guess.
//
// Deliberately outside the subscription's own candle/snapshot seq
// space (no c.nextSeq call): status is advisory, read by a client's own
// badge, never part of the replace/append candle state machine
// (live-candles-reducer.ts's own decideApplyAction) -- giving it a real
// seq would shift every subsequent candle/snapshot seq by one per
// status sent, breaking the existing, already-decided "snapshot is
// seq 1" wire contract's own numbering for no benefit, since
// live-socket.ts's own gap check already exempts "status" from the
// seq-contiguity rule.
func (h *Hub) sendLiveStatus(c *Connection, key subscriptionKey, provider string, snap SnapshotPayload, d time.Duration, now time.Time) {
	var lastEventAt *time.Time
	if snap.LastClosedOpenTime != nil {
		t := snap.LastClosedOpenTime.Add(d)
		lastEventAt = &t
	}
	payload, err := json.Marshal(StatusPayload{State: "live", Provider: provider, LastEventAt: lastEventAt})
	if err != nil {
		return
	}
	c.send(Envelope{V: envelopeVersion, Type: TypeStatus, ID: NewEventID(), TS: now, Sub: c.subString(key), Payload: payload})
}

// Unsubscribe handles one `unsubscribe` message. A key the connection
// does not hold is a no-op (smaller correction), not an error.
func (h *Hub) Unsubscribe(c *Connection, instrumentID int64, interval Interval) {
	key := subscriptionKey{InstrumentID: instrumentID, Interval: interval}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !c.hasSubscription(key) {
		return
	}
	c.clearSubscription(key)
	h.removeSubscriptionLocked(c, key)
}

// addSubscriptionLocked wires c into instrumentConns and starts the
// instrument's own Redis subscription if this is the first connection
// to ever care about it. Caller holds h.mu.
func (h *Hub) addSubscriptionLocked(c *Connection, key subscriptionKey) {
	conns, ok := h.instrumentConns[key.InstrumentID]
	if !ok {
		conns = make(map[*Connection]map[Interval]struct{})
		h.instrumentConns[key.InstrumentID] = conns
	}
	intervals, ok := conns[c]
	if !ok {
		intervals = make(map[Interval]struct{})
		conns[c] = intervals
	}
	intervals[key.Interval] = struct{}{}

	h.redisRefCount[key.InstrumentID]++
	if h.redisRefCount[key.InstrumentID] == 1 {
		h.startRedisSubscriptionLocked(key.InstrumentID)
	}
}

// removeSubscriptionLocked is addSubscriptionLocked's own inverse.
// Caller holds h.mu.
func (h *Hub) removeSubscriptionLocked(c *Connection, key subscriptionKey) {
	conns, ok := h.instrumentConns[key.InstrumentID]
	if !ok {
		return
	}
	intervals, ok := conns[c]
	if !ok {
		return
	}
	if _, ok := intervals[key.Interval]; !ok {
		return
	}
	delete(intervals, key.Interval)
	if len(intervals) == 0 {
		delete(conns, c)
	}
	if len(conns) == 0 {
		delete(h.instrumentConns, key.InstrumentID)
	}

	h.redisRefCount[key.InstrumentID]--
	if h.redisRefCount[key.InstrumentID] <= 0 {
		delete(h.redisRefCount, key.InstrumentID)
		if cancel, ok := h.redisCancel[key.InstrumentID]; ok {
			cancel()
			delete(h.redisCancel, key.InstrumentID)
		}
	}
}

// startRedisSubscriptionLocked starts consuming instrumentID's own
// Redis channel in a background goroutine, routing every event to
// routeEvent. Caller holds h.mu.
//
// Loss detection (review item 7) happens here, against the event
// stream's own content, not against anything the Subscriber reports
// about its connection: lastSeq is this goroutine's own private state
// (safe without a lock -- nothing else ever touches it, since this is
// the only goroutine that ever processes instrumentID's own events).
// Any event whose Seq is not exactly lastSeq+1 -- a gap (events were
// missed) or a repeat (the same or an older one arrived again) -- means
// something was lost between the worker's last successful publish this
// goroutine saw and now, for whatever reason (a dropped connection, a
// message lost in transit, anything else): handleCandleStreamLoss
// resyncs every affected subscription from the database, which has the
// one copy of the truth Redis's own pub/sub never promised to keep.
func (h *Hub) startRedisSubscriptionLocked(instrumentID int64) {
	ctx, cancel := context.WithCancel(context.Background())
	events, subCancel, err := h.subscriber.Subscribe(ctx, instrumentID)
	if err != nil {
		h.logger.Error("hub: subscribe to redis", "instrument_id", instrumentID, "error", err)
		cancel()
		return
	}
	h.redisCancel[instrumentID] = func() {
		subCancel()
		cancel()
	}

	go func() {
		var lastSeq uint64
		for {
			select {
			case <-ctx.Done():
				return
			case raw, ok := <-events:
				if !ok {
					return
				}
				var ev CandleEvent
				if err := json.Unmarshal(raw, &ev); err != nil {
					h.logger.Error("hub: decode candle event", "instrument_id", instrumentID, "error", err)
					continue
				}
				if lastSeq != 0 && ev.Seq != lastSeq+1 {
					h.logger.Warn("hub: candle stream sequence loss, resyncing",
						"instrument_id", instrumentID, "last_seq", lastSeq, "got_seq", ev.Seq)
					h.handleCandleStreamLoss(instrumentID)
					lastSeq = ev.Seq
					// The resync above reads current state from the
					// database, which already reflects ev's own
					// write (publish only ever happens after that
					// write succeeds) -- delivering ev again on top
					// would be redundant, not incorrect, but skipped
					// for simplicity.
					continue
				}
				lastSeq = ev.Seq
				h.routeEvent(context.Background(), ev)
			}
		}
	}()
}

// handleCandleStreamLoss is review item 7: degraded first, then
// immediately a fresh snapshot per affected subscription -- the same
// recovery path an ordinary client resubscribe already uses, just
// triggered by a sequence gap/repeat instead of a client action. Both
// the degraded notice and the snapshot that follows are sent per
// SUBSCRIPTION (its own Sub), not once per connection with no Sub at
// all -- a status message with no Sub cannot be routed to any one
// subscription's own UI, so it would otherwise be silently unattributed
// on arrival (live-socket.ts's own handleMessage only ever routes a
// message by its Sub field). The degraded notice itself carries no seq
// (see sendLiveStatus's own doc comment on why status stays outside the
// candle/snapshot seq space entirely).
func (h *Hub) handleCandleStreamLoss(instrumentID int64) {
	h.mu.Lock()
	conns := h.instrumentConns[instrumentID]
	targets := make(map[*Connection][]Interval, len(conns))
	for c, intervals := range conns {
		for iv := range intervals {
			targets[c] = append(targets[c], iv)
		}
	}
	h.mu.Unlock()

	ctx := context.Background()
	inst, err := h.instruments.GetByID(ctx, instrumentID)
	provider := "" // best-effort: a provider name missing from this one notice is not worth failing recovery over
	if err == nil {
		provider = inst.Provider
	}

	degradedPayload, _ := json.Marshal(StatusPayload{State: "degraded", Provider: provider})
	for c, ivs := range targets {
		for _, iv := range ivs {
			key := subscriptionKey{InstrumentID: instrumentID, Interval: iv}
			c.send(Envelope{V: envelopeVersion, Type: TypeStatus, ID: NewEventID(), TS: h.clk.Now(), Sub: c.subString(key), Payload: degradedPayload})
		}
	}

	for c, ivs := range targets {
		for _, iv := range ivs {
			d, ok := iv.Duration()
			if !ok {
				continue
			}
			snap, err := h.buildSnapshot(ctx, instrumentID, iv, d)
			if err != nil {
				h.logger.Error("hub: rebuild snapshot after redis reconnect", "instrument_id", instrumentID, "interval", iv, "error", err)
				continue
			}
			key := subscriptionKey{InstrumentID: instrumentID, Interval: iv}
			c.resetSubscription(key)
			c.sendSnapshot(key, snap, h.clk.Now())
			h.sendLiveStatus(c, key, provider, snap, d, h.clk.Now())
		}
	}
}

// routeEvent is called once per CandleEvent the worker published for
// ev.InstrumentID, for every connection subscribed to ANY interval of
// that instrument -- the 1m subscription (if any) gets ev directly; any
// other subscribed interval is recomputed from ev's own window.
func (h *Hub) routeEvent(ctx context.Context, ev CandleEvent) {
	h.mu.Lock()
	conns := h.instrumentConns[ev.InstrumentID]
	targets := make(map[*Connection][]Interval, len(conns))
	for c, intervals := range conns {
		for iv := range intervals {
			targets[c] = append(targets[c], iv)
		}
	}
	h.mu.Unlock()

	for c, ivs := range targets {
		for _, iv := range ivs {
			h.deliverToSubscription(ctx, c, ev, iv)
		}
	}
}

func (h *Hub) deliverToSubscription(ctx context.Context, c *Connection, ev CandleEvent, iv Interval) {
	key := subscriptionKey{InstrumentID: ev.InstrumentID, Interval: iv}
	d, ok := iv.Duration()
	if !ok {
		return
	}
	priceScale, qtyScale, err := h.scaleFor(ctx, ev.InstrumentID)
	if err != nil {
		h.logger.Error("hub: scale for delivery", "instrument_id", ev.InstrumentID, "error", err)
		return
	}

	if iv == Interval1m {
		h.deliverOneMinute(c, key, ev, priceScale, qtyScale)
		return
	}

	w := marketdata.UTCWindow(clock.NewEventTime(ev.OpenTime), d)
	switch ev.Kind {
	case EventCorrected:
		// review item 4: a correction to a source minute must be able
		// to change every derived window that contains it, with the
		// derived-revision rule, never a fixed value. A correction only
		// ever applies to an already-closed candle, so the window it
		// falls in must already be complete -- AggregateRange (never
		// Aggregate's forgiving cousin) is the right tool here.
		agg, err := h.candles.AggregateRange(ctx, ev.InstrumentID, []marketdata.Window{w})
		if err != nil {
			h.logger.Error("hub: recompute corrected derived window", "instrument_id", ev.InstrumentID, "interval", iv, "error", err)
			return
		}
		if len(agg) == 1 {
			c.sendCorrected(key, agg[0], priceScale, qtyScale, h.clk.Now())
		}
	default:
		// Try the authoritative, complete-only aggregate first: if the
		// window just became complete (this event was its last
		// minute's own close), this is the derived candle's own close.
		// Otherwise it is still forming, and the best-effort partial
		// aggregate is what the live display shows.
		agg, err := h.candles.AggregateRange(ctx, ev.InstrumentID, []marketdata.Window{w})
		if err != nil {
			h.logger.Error("hub: recompute derived window", "instrument_id", ev.InstrumentID, "interval", iv, "error", err)
			return
		}
		if len(agg) == 1 {
			c.sendClosed(key, aggToPartial(agg[0]), priceScale, qtyScale, h.clk.Now())
			return
		}
		partial, err := h.candles.AggregatePartial(ctx, ev.InstrumentID, w)
		if err != nil {
			h.logger.Error("hub: recompute partial derived window", "instrument_id", ev.InstrumentID, "interval", iv, "error", err)
			return
		}
		if !partial.HasData {
			return
		}
		c.sendForming(key, partial, priceScale, qtyScale, h.clk.Now())
	}
}

func (h *Hub) deliverOneMinute(c *Connection, key subscriptionKey, ev CandleEvent, priceScale, qtyScale quant.Scale) {
	p := marketdata.PartialAggregate{
		Window:   marketdata.Window{Start: clock.NewEventTime(ev.OpenTime), End: clock.NewEventTime(ev.CloseTime)},
		Open:     ev.Open,
		High:     ev.High,
		Low:      ev.Low,
		Close:    ev.Close,
		Volume:   ev.Volume,
		Revision: ev.Revision,
		HasData:  true,
	}
	switch ev.Kind {
	case EventForming:
		c.sendForming(key, p, priceScale, qtyScale, h.clk.Now())
	case EventClosed:
		c.sendClosed(key, p, priceScale, qtyScale, h.clk.Now())
	case EventCorrected:
		c.sendCorrected(key, marketdata.AggregateOHLCV{
			Window: p.Window, Open: ev.Open, High: ev.High, Low: ev.Low, Close: ev.Close, Volume: ev.Volume, Revision: ev.Revision,
		}, priceScale, qtyScale, h.clk.Now())
	}
}

// aggToPartial adapts a complete AggregateOHLCV to the PartialAggregate
// shape sendClosed takes -- both carry the same OHLCV fields, just with
// different provenance (one guaranteed complete, one best-effort).
func aggToPartial(agg marketdata.AggregateOHLCV) marketdata.PartialAggregate {
	return marketdata.PartialAggregate{
		Window: agg.Window, Open: agg.Open, High: agg.High, Low: agg.Low, Close: agg.Close, Volume: agg.Volume, Revision: agg.Revision, HasData: true,
	}
}

// buildSnapshot is the per-interval join point (review item 5): the
// latest complete window of interval, plus the current forming candle
// if any.
func (h *Hub) buildSnapshot(ctx context.Context, instrumentID int64, interval Interval, d time.Duration) (SnapshotPayload, error) {
	priceScale, qtyScale, err := h.scaleFor(ctx, instrumentID)
	if err != nil {
		return SnapshotPayload{}, err
	}

	join, err := h.candles.LatestCompleteWindow(ctx, instrumentID, d)
	if err != nil {
		return SnapshotPayload{}, fmt.Errorf("hub: latest complete window instrument=%d interval=%s: %w", instrumentID, interval, err)
	}

	var payload SnapshotPayload
	if join != nil {
		t := join.Window.Start.Time()
		payload.LastClosedOpenTime = &t
		rev := join.Revision
		payload.LastClosedRevision = &rev

		formingWindow := marketdata.Window{Start: join.Window.End, End: clock.NewEventTime(join.Window.End.Time().Add(d))}
		partial, err := h.candles.AggregatePartial(ctx, instrumentID, formingWindow)
		if err != nil {
			return SnapshotPayload{}, fmt.Errorf("hub: forming candle instrument=%d interval=%s: %w", instrumentID, interval, err)
		}
		if partial.HasData {
			p := partialToPayload(partial, priceScale, qtyScale)
			payload.Candle = &p
		}
	}
	return payload, nil
}
