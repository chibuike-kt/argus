package hub

import (
	"time"

	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// CandlePayload is the wire-format candle shared by `candle`,
// `candle_closed`, `candle_corrected`, and the forming candle inside
// `snapshot`. Prices and volume are decimal strings end-to-end (brief
// rule 3): no float64 anywhere on this path.
type CandlePayload struct {
	OpenTime  time.Time `json:"open_time"`
	CloseTime time.Time `json:"close_time"`
	Open      string    `json:"open"`
	High      string    `json:"high"`
	Low       string    `json:"low"`
	Close     string    `json:"close"`
	Volume    string    `json:"volume"`
	Revision  int32     `json:"revision"`
}

func partialToPayload(p marketdata.PartialAggregate, priceScale, qtyScale quant.Scale) CandlePayload {
	return CandlePayload{
		OpenTime:  p.Window.Start.Time(),
		CloseTime: p.Window.End.Time(),
		Open:      p.Open.String(priceScale),
		High:      p.High.String(priceScale),
		Low:       p.Low.String(priceScale),
		Close:     p.Close.String(priceScale),
		Volume:    p.Volume.String(qtyScale),
		Revision:  p.Revision,
	}
}

func aggToPayload(agg marketdata.AggregateOHLCV, priceScale, qtyScale quant.Scale) CandlePayload {
	return CandlePayload{
		OpenTime:  agg.Window.Start.Time(),
		CloseTime: agg.Window.End.Time(),
		Open:      agg.Open.String(priceScale),
		High:      agg.High.String(priceScale),
		Low:       agg.Low.String(priceScale),
		Close:     agg.Close.String(priceScale),
		Volume:    agg.Volume.String(qtyScale),
		Revision:  agg.Revision,
	}
}

// SnapshotPayload is the `snapshot` message's own payload: the per-
// interval join point (review item 5) plus the current forming candle,
// if any exists yet. LastClosedOpenTime/LastClosedRevision are nil when
// no closed candle of this interval exists yet (never guessed).
type SnapshotPayload struct {
	LastClosedOpenTime *time.Time     `json:"last_closed_open_time"`
	LastClosedRevision *int32         `json:"last_closed_revision"`
	Candle             *CandlePayload `json:"candle,omitempty"`
}

// StatusPayload is the `status` message's own payload. LastEventAt is
// the smaller correction the review asks for ("status should carry
// last_event_at so the badge can show how old the last price is").
type StatusPayload struct {
	State       string     `json:"state"` // live, stale, degraded
	Provider    string     `json:"provider,omitempty"`
	LastEventAt *time.Time `json:"last_event_at,omitempty"`
}

// ErrorPayload is the `error` message's own payload, sent instead of a
// snapshot when Subscribe fails -- the same code for an unknown and a
// disabled instrument (smaller correction: "a subscribe for an unknown
// instrument and for a disabled one returns the same error code, so a
// user cannot learn which ids exist").
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorCodeNotSubscribable is the one error code a subscribe failure
// ever reports, for both an unknown and a disabled instrument alike
// (smaller correction: distinguishing them would let a client
// enumerate which ids exist).
const ErrorCodeNotSubscribable = "not_subscribable"
