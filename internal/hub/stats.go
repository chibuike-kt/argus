package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// Live `stats` streaming (product overhaul brief, Phase 3, D3): one
// multiplexed subscription per connection for a whole watchlist
// (`subscribe_stats` with up to MaxStatsInstrumentsPerConnection ids),
// and one `stats` message per instrument at most once per
// StatsMinInterval, only when a figure changed.
//
// Cost model. The figures are computed ONCE per instrument per flush,
// not once per connection: every subscriber of an instrument gets the
// same payload. Within one minute, a forming tick costs no query at all:
// the hub keeps the instrument's marketdata.LiveBase (the change
// reference and the closed part of the volume window, read once from
// the database) and applies the tick's own close and volume to it with
// the same LiveBase.Apply GET /instruments/stats uses. Anything that can
// change the base -- a candle closing (the next minute becomes the
// anchor), a correction or backfill, a stream loss -- makes the next
// flush re-read it with one GetCandleStatsInputs statement. So the
// database sees about one statement per instrument per minute, not one
// per tick.

// StatsReader is the boundary the stats stream needs from
// marketdata.Store -- the same Stats GET /instruments/stats serves, so
// the socket and the REST endpoint cannot compute different figures.
type StatsReader interface {
	Stats(ctx context.Context, instrumentID int64) (marketdata.Stats, error)
}

// ErrStatsUnavailable is returned by SubscribeStats when the hub was
// wired without a StatsReader (EnableStats never called).
var ErrStatsUnavailable = errors.New("hub: stats stream not enabled")

// ErrTooManyStatsInstruments is returned by SubscribeStats for a list
// longer than config.LiveFeed.MaxStatsInstrumentsPerConnection.
var ErrTooManyStatsInstruments = errors.New("hub: too many instruments in subscribe_stats")

// EnableStats turns on the `stats` stream, reading figures through r.
// Optional and additive (CLAUDE.md rule 4: New's signature is unchanged);
// call it once, before the hub serves connections.
func (h *Hub) EnableStats(r StatsReader) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statsReader = r
}

// StatsSub is the `sub` value of every `stats` message (and of an error
// rejecting one instrument of a subscribe_stats) for instrumentID:
// "stats:<id>", so a client routes stats per instrument exactly the way
// it routes candle subscriptions.
func StatsSub(instrumentID int64) string {
	return "stats:" + strconv.FormatInt(instrumentID, 10)
}

// StatsSubAll is the `sub` of an error that concerns the whole
// subscribe_stats message (for example, a list over the limit).
const StatsSubAll = "stats"

// instrumentStats is one instrument's shared stats state, created when
// its first stats subscriber arrives and dropped with its last.
type instrumentStats struct {
	instrumentID int64
	priceScale   quant.Scale
	qtyScale     quant.Scale

	// flushMu serializes flushes, so a slow database read in one flush
	// can never land after (and overwrite) a newer one.
	flushMu sync.Mutex

	mu sync.Mutex
	// base is the LiveBase the last database read returned; nil until
	// the first one.
	base *marketdata.LiveBase
	// pending is the newest candle event since the last flush.
	pending *CandleEvent
	// rebase is set when the next flush must re-read the base.
	rebase bool
	// last is the payload last sent to subscribers; nil before the first.
	last *StatsPayload
	// lastFlushAt and scheduled implement the StatsMinInterval coalescing.
	lastFlushAt time.Time
	scheduled   bool
	// closed is set when the last subscriber left; a flush already in
	// flight then sends nothing.
	closed bool
}

// SubscribeStats handles one `subscribe_stats` message: the connection's
// stats set becomes exactly ids (replace, not add: the client sends its
// whole watchlist each time, and an empty list ends every stats
// subscription). Every id is checked to be enabled, every time; an
// unknown or disabled id is reported back in rejected (the caller sends
// one not_subscribable error per id, identical for both, so ids cannot
// be enumerated) and left out. Each accepted instrument's current
// figures are sent to c right away, or as soon as the first read
// completes.
func (h *Hub) SubscribeStats(ctx context.Context, c *Connection, ids []int64) (rejected []int64, err error) {
	h.mu.Lock()
	reader := h.statsReader
	h.mu.Unlock()
	if reader == nil {
		return nil, ErrStatsUnavailable
	}

	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) > h.cfg.MaxStatsInstrumentsPerConnection {
		return nil, fmt.Errorf("%w: %d ids, at most %d", ErrTooManyStatsInstruments, len(unique), h.cfg.MaxStatsInstrumentsPerConnection)
	}

	type accepted struct {
		id                   int64
		priceScale, qtyScale quant.Scale
	}
	keep := make([]accepted, 0, len(unique))
	for _, id := range unique {
		inst, err := h.instruments.GetByID(ctx, id)
		if errors.Is(err, instruments.ErrNotFound) || (err == nil && !inst.Enabled) {
			rejected = append(rejected, id)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("hub: get instrument %d for stats: %w", id, err)
		}
		keep = append(keep, accepted{id: id, priceScale: inst.PriceScale, qtyScale: inst.QtyScale})
	}

	want := make(map[int64]struct{}, len(keep))
	for _, a := range keep {
		want[a.id] = struct{}{}
	}

	h.mu.Lock()
	for _, id := range c.statsIDs() {
		if _, ok := want[id]; !ok {
			c.removeStatsID(id)
			h.removeStatsLocked(c, id)
		}
	}
	states := make([]*instrumentStats, 0, len(keep))
	for _, a := range keep {
		if c.addStatsID(a.id) {
			h.addStatsLocked(c, a.id, a.priceScale, a.qtyScale)
		}
		states = append(states, h.statsState[a.id])
	}
	h.mu.Unlock()

	// The current figures, now: the cached payload when one exists, else
	// a flush (which broadcasts to every subscriber, c included, since
	// nothing was sent yet). A duplicate with a concurrent broadcast is
	// harmless: a stats message is the full state, and the client keeps
	// the newer of two by as_of and last_tick_at.
	for _, st := range states {
		if st == nil {
			continue
		}
		st.mu.Lock()
		last := st.last
		if last == nil {
			st.rebase = true
			h.scheduleStatsFlushLocked(st, true)
		}
		st.mu.Unlock()
		if last != nil {
			c.sendStats(*last, h.clk.Now())
		}
	}
	return rejected, nil
}

// UnsubscribeStats ends every stats subscription c holds. A connection
// holding none is a no-op.
func (h *Hub) UnsubscribeStats(c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range c.statsIDs() {
		c.removeStatsID(id)
		h.removeStatsLocked(c, id)
	}
}

// addStatsLocked wires c into id's stats fan-out, creating the shared
// state and taking a Redis reference for it. Caller holds h.mu.
func (h *Hub) addStatsLocked(c *Connection, id int64, priceScale, qtyScale quant.Scale) {
	conns, ok := h.statsConns[id]
	if !ok {
		conns = make(map[*Connection]struct{})
		h.statsConns[id] = conns
	}
	conns[c] = struct{}{}
	if _, ok := h.statsState[id]; !ok {
		h.statsState[id] = &instrumentStats{instrumentID: id, priceScale: priceScale, qtyScale: qtyScale}
	}

	h.redisRefCount[id]++
	if h.redisRefCount[id] == 1 {
		h.startRedisSubscriptionLocked(id)
	}
}

// removeStatsLocked is addStatsLocked's inverse. Caller holds h.mu.
func (h *Hub) removeStatsLocked(c *Connection, id int64) {
	conns, ok := h.statsConns[id]
	if !ok {
		return
	}
	if _, ok := conns[c]; !ok {
		return
	}
	delete(conns, c)
	if len(conns) == 0 {
		delete(h.statsConns, id)
		if st, ok := h.statsState[id]; ok {
			st.mu.Lock()
			st.closed = true
			st.mu.Unlock()
			delete(h.statsState, id)
		}
	}

	h.redisRefCount[id]--
	if h.redisRefCount[id] <= 0 {
		delete(h.redisRefCount, id)
		if cancel, ok := h.redisCancel[id]; ok {
			cancel()
			delete(h.redisCancel, id)
		}
	}
}

// onStatsEvent records ev for id's next stats flush. Every event kind
// other than a forming tick (a close, a correction, a backfilled minute)
// can change the base, so it forces a re-read.
func (h *Hub) onStatsEvent(ev CandleEvent) {
	h.mu.Lock()
	st := h.statsState[ev.InstrumentID]
	h.mu.Unlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	e := ev
	st.pending = &e
	if ev.Kind != EventForming {
		st.rebase = true
	}
	h.scheduleStatsFlushLocked(st, false)
}

// markStatsRebase forces id's next flush to re-read the base and
// schedules one -- the stream-loss path (whatever was missed, the
// database has it).
func (h *Hub) markStatsRebase(id int64) {
	h.mu.Lock()
	st := h.statsState[id]
	h.mu.Unlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.rebase = true
	h.scheduleStatsFlushLocked(st, false)
}

// scheduleStatsFlushLocked arranges one flush of st, no sooner than
// StatsMinInterval after the previous one (immediately when now is
// true: a new subscriber waiting for its first figures). Repeated calls
// before it runs coalesce into it. Caller holds st.mu.
func (h *Hub) scheduleStatsFlushLocked(st *instrumentStats, now bool) {
	if st.scheduled || st.closed {
		return
	}
	st.scheduled = true
	wait := time.Duration(0)
	if !now && !st.lastFlushAt.IsZero() {
		wait = st.lastFlushAt.Add(h.cfg.StatsMinInterval).Sub(h.clk.Now())
	}
	if wait < 0 {
		wait = 0
	}
	time.AfterFunc(wait, func() { h.flushStats(st) })
}

// flushStats computes st's figures and, when they differ from the last
// ones sent, sends them to every subscriber of the instrument.
func (h *Hub) flushStats(st *instrumentStats) {
	st.flushMu.Lock()
	defer st.flushMu.Unlock()

	st.mu.Lock()
	st.scheduled = false
	st.lastFlushAt = h.clk.Now()
	pending, rebase, base := st.pending, st.rebase, st.base
	st.pending, st.rebase = nil, false
	closed := st.closed
	st.mu.Unlock()
	if closed {
		return
	}

	var (
		live    *marketdata.LiveFigures
		newBase *marketdata.LiveBase
	)
	if !rebase && base != nil && pending != nil && pending.Kind == EventForming && !pending.IngestedAt.IsZero() &&
		pending.OpenTime.Equal(base.AnchorOpenTime.Time()) {
		f, err := base.Apply(pending.Close, pending.Volume, clock.NewIngestionTime(pending.IngestedAt))
		if err != nil {
			h.logger.Error("hub: apply stats tick", "instrument_id", st.instrumentID, "error", err)
			h.markStatsRebase(st.instrumentID)
			return
		}
		live, newBase = &f, base
	} else if !rebase && base != nil && pending == nil {
		// Nothing new since the last flush (a new subscriber's immediate
		// flush raced a regular one): nothing to send.
		return
	} else {
		h.mu.Lock()
		reader := h.statsReader
		h.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s, err := reader.Stats(ctx, st.instrumentID)
		cancel()
		if err != nil {
			h.logger.Error("hub: read stats", "instrument_id", st.instrumentID, "error", err)
			st.mu.Lock()
			st.rebase = true
			st.mu.Unlock()
			return
		}
		live, newBase = s.Live, s.Base
	}

	payload := NewStatsPayload(st.instrumentID, live, st.priceScale, st.qtyScale)
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	st.base = newBase
	if st.last != nil && samePayload(*st.last, payload) {
		st.mu.Unlock()
		return
	}
	st.last = &payload
	st.mu.Unlock()

	h.mu.Lock()
	conns := make([]*Connection, 0, len(h.statsConns[st.instrumentID]))
	for c := range h.statsConns[st.instrumentID] {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	now := h.clk.Now()
	for _, c := range conns {
		c.sendStats(payload, now)
	}
}

// sendStats writes one `stats` message. Outside every seq space (like
// `status`): a stats message is the instrument's full current figures,
// so a lost one is repaired by the next, never by replay.
func (c *Connection) sendStats(p StatsPayload, now time.Time) {
	payload, err := json.Marshal(p)
	if err != nil {
		return
	}
	c.send(Envelope{V: envelopeVersion, Type: TypeStats, ID: NewEventID(), TS: now, Sub: StatsSub(p.InstrumentID), Payload: payload})
}

// statsIDs returns the instrument ids c holds stats subscriptions for.
func (c *Connection) statsIDs() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int64, 0, len(c.stats))
	for id := range c.stats {
		out = append(out, id)
	}
	return out
}

// addStatsID records id; false when c already held it.
func (c *Connection) addStatsID(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.stats[id]; ok {
		return false
	}
	c.stats[id] = struct{}{}
	return true
}

func (c *Connection) removeStatsID(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.stats, id)
}

// StatsSubscriptionCount reports how many instruments c streams stats for.
func (c *Connection) StatsSubscriptionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.stats)
}
