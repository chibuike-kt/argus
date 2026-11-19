package hub

import (
	"time"

	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// StatsPayload is the `stats` message's own payload (product overhaul
// brief, Phase 3, D3): one instrument's live figures, every price and
// volume a decimal string at the instrument's own scale, every absent
// figure an explicit null (never "0"). GET /instruments/stats renders
// the same fields through the same function (NewStatsPayload), so the
// panel's first load and every live update can never disagree on
// formatting.
type StatsPayload struct {
	InstrumentID int64 `json:"instrument_id"`
	// LastPrice is the newest current candle's close: the forming
	// candle's latest close, or the latest closed candle's when nothing
	// is forming (LastPriceClosed says which).
	LastPrice       *string `json:"last_price"`
	LastPriceClosed *bool   `json:"last_price_closed"`
	Change24h       *string `json:"change_24h"`
	Change24hPct    *string `json:"change_24h_pct"`
	// Change24hReferenceOpenTime is the exact minute the change is
	// measured against, found or not, so an absent change can say which
	// minute is missing.
	Change24hReferenceOpenTime *time.Time `json:"change_24h_reference_open_time"`
	Volume24h                  *string    `json:"volume_24h"`
	// Volume24hMinutes is how many of the 1440 window minutes are
	// stored, so an absent volume can say why.
	Volume24hMinutes *int64 `json:"volume_24h_minutes"`
	// AsOf is the exchange event time the price belongs to: its candle's
	// open time (minute-grained; the candle feed has no per-trade time).
	AsOf *time.Time `json:"as_of"`
	// LastTickAt is when ARGUS stored that candle's latest value (its
	// ingestion time), the basis for "no new ticks for N seconds".
	LastTickAt *time.Time `json:"last_tick_at"`
}

// NewStatsPayload renders live at the instrument's scales. A nil live
// (no candle stored at all) renders every field as null.
func NewStatsPayload(instrumentID int64, live *marketdata.LiveFigures, priceScale, qtyScale quant.Scale) StatsPayload {
	out := StatsPayload{InstrumentID: instrumentID}
	if live == nil {
		return out
	}
	last := live.LastPrice.String(priceScale)
	out.LastPrice = &last
	closed := live.AnchorClosed
	out.LastPriceClosed = &closed
	asOf := live.AsOf.Time().UTC()
	out.AsOf = &asOf
	tick := live.LastTickAt.Time().UTC()
	out.LastTickAt = &tick
	ref := live.ReferenceOpenTime.Time().UTC()
	out.Change24hReferenceOpenTime = &ref
	minutes := live.VolumeMinutes
	out.Volume24hMinutes = &minutes
	if live.Change24h != nil {
		abs := live.Change24h.Abs.String(priceScale)
		out.Change24h = &abs
		if live.Change24h.Pct != nil {
			pct := live.Change24h.Pct.String()
			out.Change24hPct = &pct
		}
	}
	if live.Volume24h != nil {
		v := live.Volume24h.String(qtyScale)
		out.Volume24h = &v
	}
	return out
}

// samePayload reports whether a and b carry the same values, field by
// field. LastTickAt counts: a new tick at an unchanged price still moves
// "no new ticks for N seconds" back to zero on the client, so it is a
// change worth sending (still at most once per StatsMinInterval).
func samePayload(a, b StatsPayload) bool {
	return a.InstrumentID == b.InstrumentID &&
		eqStr(a.LastPrice, b.LastPrice) &&
		eqBool(a.LastPriceClosed, b.LastPriceClosed) &&
		eqStr(a.Change24h, b.Change24h) &&
		eqStr(a.Change24hPct, b.Change24hPct) &&
		eqTime(a.Change24hReferenceOpenTime, b.Change24hReferenceOpenTime) &&
		eqStr(a.Volume24h, b.Volume24h) &&
		eqInt(a.Volume24hMinutes, b.Volume24hMinutes) &&
		eqTime(a.AsOf, b.AsOf) &&
		eqTime(a.LastTickAt, b.LastTickAt)
}

func eqStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqBool(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
