// Package engineview is a feature module boundary (see ADR-003): read-only
// views of what the analysis engine stored, for the chart layers, the
// engine status line and the activity feed (product overhaul brief, Phase
// 6.2 and 6.5, D7). It writes nothing and derives nothing the engine did
// not record: every value is a stored swing, structure event, sweep, setup
// event, processing cursor or resolved gap.
package engineview

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Store reads the engine's stored output.
type Store struct {
	q *store.Queries
}

// NewStore wraps db.
func NewStore(db store.DBTX) *Store {
	return &Store{q: store.New(db)}
}

// ErrInvalidCursor is an activity cursor that does not decode.
var ErrInvalidCursor = errors.New("engineview: invalid cursor")

// ---- Structure window (6.2) ----

// Level is a swing a structure row names: its id, price and pivot time.
type Level struct {
	SwingID int64
	Price   quant.Price
	Time    time.Time
}

// Swing is a current confirmed swing.
type Swing struct {
	ID          int64
	Type        string // "high" or "low"
	PivotTime   time.Time
	Price       quant.Price
	ConfirmedAt time.Time
}

// Break is a current structure event (BOS, CHOCH, direction established).
type Break struct {
	ID               int64
	EventType        string
	DirectionBefore  string
	DirectionAfter   string
	TriggerOpenTime  time.Time
	SweepValidated   bool
	SweepID          *int64
	ProtectedSwingID *int64
	// BrokenSwing is the swing a BOS or CHOCH broke; nil for
	// direction_established.
	BrokenSwing *Level
}

// Sweep is a current liquidity sweep.
type Sweep struct {
	ID                 int64
	Side               string // "buy" or "sell"
	TriggerOpenTime    time.Time
	PenetrationExtreme quant.Price
	ReclaimClose       quant.Price
	Swept              Level
}

// Window is one page of an instrument's stored structure.
type Window struct {
	From   time.Time
	To     time.Time
	Swings []Swing
	Breaks []Break
	Sweeps []Sweep
	// NextFrom is where the next page starts; nil when [From, To) was
	// returned whole.
	NextFrom *time.Time
}

// StructureWindow reads the current swings, structure events and sweeps of
// instrumentID and timeframe whose candle time is in [from, to), at most
// limit of each kind. When a kind has more, every kind is cut at the same
// time (cutTime), so a page never overlaps the next and never skips a row.
// Three indexed range reads, each O(limit) rows.
func (s *Store) StructureWindow(ctx context.Context, instrumentID int64, timeframe string, from, to time.Time, limit int32) (Window, error) {
	if limit < 1 || limit == math.MaxInt32 {
		return Window{}, fmt.Errorf("engineview: limit %d out of range", limit)
	}
	from, to = from.UTC(), to.UTC()
	// limit+1: the extra row says whether a kind was cut, and its time is
	// where that kind's complete prefix ends.
	fetch := limit + 1

	swRows, err := s.q.ListSwingsInWindow(ctx, store.ListSwingsInWindowParams{
		InstrumentID: instrumentID, Timeframe: timeframe, FromTime: from, ToTime: to, RowLimit: fetch,
	})
	if err != nil {
		return Window{}, fmt.Errorf("engineview: swings: %w", err)
	}
	brRows, err := s.q.ListStructureEventsInWindow(ctx, store.ListStructureEventsInWindowParams{
		InstrumentID: instrumentID, Timeframe: timeframe, FromTime: from, ToTime: to, RowLimit: fetch,
	})
	if err != nil {
		return Window{}, fmt.Errorf("engineview: structure events: %w", err)
	}
	spRows, err := s.q.ListSweepsInWindow(ctx, store.ListSweepsInWindowParams{
		InstrumentID: instrumentID, Timeframe: timeframe, FromTime: from, ToTime: to, RowLimit: fetch,
	})
	if err != nil {
		return Window{}, fmt.Errorf("engineview: sweeps: %w", err)
	}

	swTimes := make([]time.Time, len(swRows))
	for i, r := range swRows {
		swTimes[i] = r.PivotTime
	}
	brTimes := make([]time.Time, len(brRows))
	for i, r := range brRows {
		brTimes[i] = r.TriggerOpenTime
	}
	spTimes := make([]time.Time, len(spRows))
	for i, r := range spRows {
		spTimes[i] = r.TriggerOpenTime
	}
	cut, cutOK := cutTime(int(limit), swTimes, brTimes, spTimes)
	if cutOK && !cut.After(from) {
		// Cannot happen with limit >= 3: at most two current rows of a
		// kind share a candle time (a high and a low; a buy and a sell).
		return Window{}, fmt.Errorf("engineview: page would not advance past %s", from)
	}

	w := Window{From: from, To: to}
	keep := func(t time.Time) bool { return !cutOK || t.Before(cut) }
	for _, r := range swRows {
		if !keep(r.PivotTime) {
			break
		}
		w.Swings = append(w.Swings, Swing{
			ID: r.ID, Type: r.SwingType, PivotTime: r.PivotTime.UTC(), Price: quant.Price(r.Price), ConfirmedAt: r.ConfirmedAt.UTC(),
		})
	}
	for _, r := range brRows {
		if !keep(r.TriggerOpenTime) {
			break
		}
		b := Break{
			ID: r.ID, EventType: r.EventType, DirectionBefore: r.DirectionBefore, DirectionAfter: r.DirectionAfter,
			TriggerOpenTime: r.TriggerOpenTime.UTC(), SweepValidated: r.SweepValidated,
			SweepID: nullInt(r.SweepEventID), ProtectedSwingID: nullInt(r.ProtectedSwingID),
		}
		if r.BrokenSwingID.Valid && r.BrokenSwingPrice.Valid && r.BrokenSwingTime.Valid {
			b.BrokenSwing = &Level{SwingID: r.BrokenSwingID.Int64, Price: quant.Price(r.BrokenSwingPrice.Int64), Time: r.BrokenSwingTime.Time.UTC()}
		}
		w.Breaks = append(w.Breaks, b)
	}
	for _, r := range spRows {
		if !keep(r.TriggerOpenTime) {
			break
		}
		w.Sweeps = append(w.Sweeps, Sweep{
			ID: r.ID, Side: r.Side, TriggerOpenTime: r.TriggerOpenTime.UTC(),
			PenetrationExtreme: quant.Price(r.PenetrationExtreme), ReclaimClose: quant.Price(r.ReclaimClose),
			Swept: Level{SwingID: r.SweptSwingID, Price: quant.Price(r.SweptSwingPrice), Time: r.SweptSwingTime.UTC()},
		})
	}
	if cutOK {
		c := cut.UTC()
		w.To = c
		w.NextFrom = &c
	}
	return w, nil
}

// cutTime decides where a page ends. Each kind's times are ascending and
// at most limit+1 long. A kind holding more than limit rows was cut: every
// row of it before its (limit+1)th row's time was read, so that time bounds
// its complete prefix. The page ends at the earliest such bound across
// kinds; ok is false when no kind was cut (the whole window was read).
func cutTime(limit int, kinds ...[]time.Time) (time.Time, bool) {
	var cut time.Time
	ok := false
	for _, ts := range kinds {
		if len(ts) <= limit {
			continue
		}
		t := ts[limit]
		if !ok || t.Before(cut) {
			cut, ok = t, true
		}
	}
	return cut, ok
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

// ---- Engine status (6.5) ----

// InstrumentStatus is what the engine has evaluated for one enabled
// instrument, from its processing cursor.
type InstrumentStatus struct {
	InstrumentID int64
	NativeSymbol string
	Provider     string
	// Analysed is false when the worker has never evaluated it (no
	// cursor); the cursor fields are then nil.
	Analysed         bool
	Timeframe        string
	EvaluatedThrough *time.Time
	LastEvaluatedAt  *time.Time
	// LatestClosedCloseTime is the newest stored closed candle's close
	// time; nil when none is stored.
	LatestClosedCloseTime *time.Time
	ClosedCandlesBehind   int64
}

// BehindSeconds is LatestClosedCloseTime minus EvaluatedThrough in whole
// seconds, never negative; ok is false when either is absent.
func (st InstrumentStatus) BehindSeconds() (int64, bool) {
	if st.EvaluatedThrough == nil || st.LatestClosedCloseTime == nil {
		return 0, false
	}
	d := st.LatestClosedCloseTime.Sub(*st.EvaluatedThrough)
	if d < 0 {
		d = 0
	}
	return int64(d / time.Second), true
}

// EngineStatus reads every enabled instrument's cursor (one statement).
func (s *Store) EngineStatus(ctx context.Context) ([]InstrumentStatus, error) {
	rows, err := s.q.ListEngineStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("engineview: engine status: %w", err)
	}
	out := make([]InstrumentStatus, 0, len(rows))
	for _, r := range rows {
		st := InstrumentStatus{
			InstrumentID: r.InstrumentID, NativeSymbol: r.NativeSymbol, Provider: r.Provider,
			Analysed: r.HasCursor, ClosedCandlesBehind: r.ClosedCandlesAfterCursor,
		}
		if r.HasCursor {
			et, le := r.EvaluatedThrough.UTC(), r.LastEvaluatedAt.UTC()
			st.Timeframe = r.Timeframe
			st.EvaluatedThrough = &et
			st.LastEvaluatedAt = &le
		}
		if r.HasClosedCandle {
			lc := r.LatestClosedCloseTime.UTC()
			st.LatestClosedCloseTime = &lc
		}
		out = append(out, st)
	}
	return out, nil
}

// ---- Activity feed (6.5) ----

// The activity kinds.
const (
	KindSetup     = "setup"
	KindStructure = "structure"
	KindGap       = "gap"
)

// Cursor is a position in the feed: (at, kind, id), strictly descending.
type Cursor struct {
	At   time.Time
	Kind string
	ID   int64
}

// firstPage sorts above every real row: a time far past any stored one.
var firstPage = Cursor{At: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), Kind: "~", ID: math.MaxInt64}

// Encode is the cursor as an opaque URL-safe string. Time is carried as
// UnixNano of the UTC instant (never a formatted local time).
func (c Cursor) Encode() string {
	raw := strconv.FormatInt(c.At.UTC().UnixNano(), 10) + ":" + c.Kind + ":" + strconv.FormatInt(c.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor reverses Encode.
func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	parts := strings.Split(string(b), ":")
	if len(parts) != 3 {
		return Cursor{}, ErrInvalidCursor
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id < 0 {
		return Cursor{}, ErrInvalidCursor
	}
	switch parts[1] {
	case KindSetup, KindStructure, KindGap:
	default:
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{At: time.Unix(0, ns).UTC(), Kind: parts[1], ID: id}, nil
}

// ActivityItem is one feed row.
type ActivityItem struct {
	Kind         string
	ID           int64
	InstrumentID int64
	Timeframe    string
	At           time.Time
	EventTime    time.Time
	Detail       string
	// Setup rows only.
	SetupID   *int64
	Direction string // setup and structure rows ("" for a gap)
	POIType   string
	FoundMode string
	// Gap rows only.
	GapEnd *time.Time
}

// ActivityQuery selects a page of the feed.
type ActivityQuery struct {
	InstrumentIDs           []int64
	Setups, Structure, Gaps bool
	Before                  *Cursor // nil: the first page
	Limit                   int32
}

// Activity reads one page, newest first, and the cursor of the next page
// (nil on the last). One statement; each branch is bounded by Limit+1.
func (s *Store) Activity(ctx context.Context, q ActivityQuery) ([]ActivityItem, *Cursor, error) {
	if q.Limit < 1 || q.Limit == math.MaxInt32 {
		return nil, nil, fmt.Errorf("engineview: limit %d out of range", q.Limit)
	}
	before := firstPage
	if q.Before != nil {
		before = *q.Before
	}
	rows, err := s.q.ListActivity(ctx, store.ListActivityParams{
		IncludeSetups:    q.Setups,
		IncludeStructure: q.Structure,
		IncludeGaps:      q.Gaps,
		InstrumentIds:    q.InstrumentIDs,
		BeforeAt:         before.At.UTC(),
		BeforeKind:       before.Kind,
		BeforeID:         before.ID,
		RowLimit:         q.Limit + 1,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("engineview: activity: %w", err)
	}
	var next *Cursor
	if len(rows) > int(q.Limit) {
		rows = rows[:q.Limit]
		last := rows[len(rows)-1]
		next = &Cursor{At: last.At.UTC(), Kind: last.Kind, ID: last.ID}
	}
	out := make([]ActivityItem, 0, len(rows))
	for _, r := range rows {
		it := ActivityItem{
			Kind: r.Kind, ID: r.ID, InstrumentID: r.InstrumentID, Timeframe: r.Timeframe,
			At: r.At.UTC(), EventTime: r.EventTime.UTC(), Detail: r.Detail,
			Direction: r.Direction, POIType: r.PoiType, FoundMode: r.FoundMode,
		}
		if r.Kind == KindSetup {
			id := r.SetupID
			it.SetupID = &id
		}
		if r.GapEnd.Valid {
			ge := r.GapEnd.Time.UTC()
			it.GapEnd = &ge
		}
		out = append(out, it)
	}
	return out, next, nil
}
