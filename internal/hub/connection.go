package hub

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// Sink is a connection's own boundary onto its real transport -- a
// consumer-defined interface (guide 4, 10) so the hub's own routing,
// coalescing and seq logic can be unit-tested with a handwritten fake,
// never a real *websocket.Conn. Send must not block past ctx; Close is
// idempotent.
type Sink interface {
	Send(ctx context.Context, data []byte) error
	Close(code int, reason string) error
}

// CloseReason codes, translated to the real transport's own close code
// at the httpapi edge (ws.go) -- kept independent of
// github.com/coder/websocket so this package never imports it.
const (
	CloseNormal          = 1000
	CloseGoingAway       = 1001
	CloseSlowConsumer    = 1008
	CloseSessionInvalid  = 1008
	CloseSubscribeFlood  = 1008
	CloseMessageTooLarge = 1009
)

// subState is one subscription's own sequence counter and pending
// coalesced forming update.
type subState struct {
	seq            uint64
	pendingForming *Envelope
	lastSentAt     time.Time
	flushScheduled bool
}

// Connection is one live WebSocket client's server-side state: its
// subscriptions, their sequence counters, and the bounded outbound path
// coalescing and overflow both go through. Transport-agnostic: Register
// it with a Hub, feed it a real Sink, and the hub's own fan-out, auth
// and rate-limit rules apply identically whether the sink is a real
// socket or a test fake.
type Connection struct {
	UserID    int64
	SessionID string
	RemoteIP  string

	sink Sink
	cfg  config.LiveFeed
	clk  clock.Clock

	mu   sync.Mutex
	subs map[subscriptionKey]*subState

	closeOnce sync.Once
	closed    chan struct{}

	// subscribeMsgTimes is the flood-detection window for subscribe and
	// unsubscribe messages (review item 8). Guarded by mu.
	subscribeMsgTimes []time.Time
}

// NewConnection wires a Connection. sink must not be nil.
func NewConnection(sink Sink, userID int64, sessionID, remoteIP string, cfg config.LiveFeed, clk clock.Clock) *Connection {
	return &Connection{
		UserID:    userID,
		SessionID: sessionID,
		RemoteIP:  remoteIP,
		sink:      sink,
		cfg:       cfg,
		clk:       clk,
		subs:      make(map[subscriptionKey]*subState),
		closed:    make(chan struct{}),
	}
}

// Closed reports whether the connection has already been torn down.
func (c *Connection) Closed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// Close closes the sink exactly once with code/reason, and marks the
// connection closed so no further send is attempted.
func (c *Connection) Close(code int, reason string) {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.sink.Close(code, reason)
	})
}

func (c *Connection) hasSubscription(key subscriptionKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.subs[key]
	return ok
}

// SubscriptionCount reports how many (instrument, interval)
// subscriptions this connection currently holds.
func (c *Connection) SubscriptionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subs)
}

// resetSubscription (re)initializes key's own seq counter to zero --
// called on every (re)subscribe, including the first one, so a
// resubscribe after unsubscribe or after a reconnect starts a fresh
// sequence the client is told to expect via the snapshot it receives
// in the same breath.
func (c *Connection) resetSubscription(key subscriptionKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs[key] = &subState{}
}

func (c *Connection) clearSubscription(key subscriptionKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subs, key)
}

// CheckSubscribeRate enforces MaxSubscribeMessagesPerSecond (review item
// 8). Call once per subscribe/unsubscribe message received; returns
// false once the connection is flooding.
func (c *Connection) CheckSubscribeRate() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clk.Now()
	cutoff := now.Add(-time.Second)
	kept := c.subscribeMsgTimes[:0]
	for _, t := range c.subscribeMsgTimes {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	c.subscribeMsgTimes = kept
	return float64(len(kept)) <= c.cfg.MaxSubscribeMessagesPerSecond
}

// send marshals and writes env to the sink, never blocking past what
// the sink itself allows; a write error closes the connection with
// CloseSlowConsumer, matching the brief's own "on overflow close the
// connection... never block the hub" (the sink's own Send is expected
// to enforce the bound, e.g. via a buffered channel that errors instead
// of blocking when full -- see ws.go's own Sink implementation).
func (c *Connection) send(env Envelope) {
	if c.Closed() {
		return
	}
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.sink.Send(ctx, data); err != nil {
		c.Close(CloseSlowConsumer, "slow consumer: send failed")
	}
}

// nextSeq assigns the next sequence number for key, after coalescing
// (review item 3): called only from the place that is actually about to
// write to the wire, never when a forming update is merely queued.
func (c *Connection) nextSeq(key subscriptionKey) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.subs[key]
	if !ok {
		return 0
	}
	st.seq++
	return st.seq
}

func (c *Connection) subString(key subscriptionKey) string {
	return key.subString()
}

// sendForming delivers a `candle` message for key, coalesced at
// cfg.MaxUpdateRatePerSecond: if the last one sent for this subscription
// was recent enough, this update replaces any already-pending one and
// returns without sending (and without consuming a seq); otherwise it is
// sent immediately. A deferred flush is scheduled, at most once per
// pending value, so a coalesced update is never silently lost.
func (c *Connection) sendForming(key subscriptionKey, p marketdata.PartialAggregate, priceScale, qtyScale quant.Scale, now time.Time) {
	payload, err := json.Marshal(partialToPayload(p, priceScale, qtyScale))
	if err != nil {
		return
	}
	env := Envelope{V: envelopeVersion, Type: TypeCandle, ID: NewEventID(), TS: now, Sub: c.subString(key), Payload: payload}

	c.mu.Lock()
	st, ok := c.subs[key]
	if !ok {
		c.mu.Unlock()
		return
	}
	minInterval := rateToInterval(c.cfg.MaxUpdateRatePerSecond)
	if st.lastSentAt.IsZero() || !now.Before(st.lastSentAt.Add(minInterval)) {
		st.seq++
		seq := st.seq
		st.lastSentAt = now
		st.pendingForming = nil
		c.mu.Unlock()
		env.Seq = seq
		c.send(env)
		return
	}
	st.pendingForming = &env
	alreadyScheduled := st.flushScheduled
	st.flushScheduled = true
	wait := st.lastSentAt.Add(minInterval).Sub(now)
	c.mu.Unlock()

	if !alreadyScheduled {
		time.AfterFunc(wait, func() { c.flushForming(key) })
	}
}

func (c *Connection) flushForming(key subscriptionKey) {
	c.mu.Lock()
	st, ok := c.subs[key]
	if !ok {
		c.mu.Unlock()
		return
	}
	st.flushScheduled = false
	pending := st.pendingForming
	st.pendingForming = nil
	if pending == nil {
		c.mu.Unlock()
		return
	}
	st.seq++
	pending.Seq = st.seq
	st.lastSentAt = c.clk.Now()
	c.mu.Unlock()

	c.send(*pending)
}

// rateToInterval converts a messages-per-second rate to the minimum gap
// between sends. A non-positive rate means "do not coalesce" (send
// every update immediately) -- defensive, since the config's own
// validation (not written in this phase) does not yet reject <= 0.
func rateToInterval(rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / rate)
}

// sendClosed delivers `candle_closed` for key -- never coalesced or
// dropped, per the brief's own rule.
func (c *Connection) sendClosed(key subscriptionKey, p marketdata.PartialAggregate, priceScale, qtyScale quant.Scale, now time.Time) {
	payload, err := json.Marshal(partialToPayload(p, priceScale, qtyScale))
	if err != nil {
		return
	}
	seq := c.nextSeq(key)
	c.send(Envelope{V: envelopeVersion, Type: TypeCandleClosed, ID: NewEventID(), TS: now, Sub: c.subString(key), Seq: seq, Payload: payload})
}

// sendCorrected delivers `candle_corrected` for key -- never coalesced
// or dropped. agg carries the real derived-revision value (review item
// 4), already computed by the caller via AggregateRange/Aggregate.
func (c *Connection) sendCorrected(key subscriptionKey, agg marketdata.AggregateOHLCV, priceScale, qtyScale quant.Scale, now time.Time) {
	payload, err := json.Marshal(aggToPayload(agg, priceScale, qtyScale))
	if err != nil {
		return
	}
	seq := c.nextSeq(key)
	c.send(Envelope{V: envelopeVersion, Type: TypeCandleCorrected, ID: NewEventID(), TS: now, Sub: c.subString(key), Seq: seq, Payload: payload})
}

// sendSnapshot delivers `snapshot` for key -- always sent immediately,
// never coalesced (it is sent once per (re)subscribe, including the
// post-Redis-reconnect recovery path, not on every tick).
func (c *Connection) sendSnapshot(key subscriptionKey, snap SnapshotPayload, now time.Time) {
	payload, err := json.Marshal(snap)
	if err != nil {
		return
	}
	seq := c.nextSeq(key)
	c.send(Envelope{V: envelopeVersion, Type: TypeSnapshot, ID: NewEventID(), TS: now, Sub: c.subString(key), Seq: seq, Payload: payload})
}
