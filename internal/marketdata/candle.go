// Package marketdata is a feature module boundary (see ADR-003).
// Market-data ingestion, normalization and canonical candle storage
// (ADR-006).
package marketdata

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Candle is the domain view of one row of the canonical 1-minute series.
// Open/High/Low/Close and Volume are scaled by the owning instrument's
// PriceScale and QtyScale respectively (see instruments.Instrument); this
// type does not carry the scale itself, the same way quant.Price and
// quant.Qty do not.
//
// Lifecycle fields mirror 000003's trg_candles_enforce_lifecycle exactly:
// IsClosed distinguishes forming (overwritable) from frozen, and
// SupersededAt/SupersededBy distinguish current from terminal. See
// Store.SupersedeCandle for the only legal way to move a closed candle
// into the superseded state.
type Candle struct {
	ID           int64
	InstrumentID int64
	OpenTime     clock.EventTime
	CloseTime    clock.EventTime
	Open         quant.Price
	High         quant.Price
	Low          quant.Price
	Close        quant.Price
	Volume       quant.Qty
	Revision     int32
	IsClosed     bool
	TickAligned  bool
	Provider     string
	IngestedAt   clock.IngestionTime
	// SupersededAt is when this row stopped being current (handshake step
	// 1). It is a ProcessingTime, not an EventTime: it marks when ARGUS
	// acted, not anything the exchange reported.
	SupersededAt *clock.ProcessingTime
	SupersededBy *int64
}

func fromCandleRow(row store.Candle) Candle {
	c := Candle{
		ID:           row.ID,
		InstrumentID: row.InstrumentID,
		OpenTime:     clock.NewEventTime(row.OpenTime),
		CloseTime:    clock.NewEventTime(row.CloseTime),
		Open:         quant.Price(row.Open),
		High:         quant.Price(row.High),
		Low:          quant.Price(row.Low),
		Close:        quant.Price(row.Close),
		Volume:       quant.Qty(row.Volume),
		Revision:     row.Revision,
		IsClosed:     row.IsClosed,
		TickAligned:  row.TickAligned,
		Provider:     row.Provider,
		IngestedAt:   clock.NewIngestionTime(row.IngestedAt),
	}
	if row.SupersededAt.Valid {
		t := clock.NewProcessingTime(row.SupersededAt.Time)
		c.SupersededAt = &t
	}
	if row.SupersededBy.Valid {
		id := row.SupersededBy.Int64
		c.SupersededBy = &id
	}
	return c
}

// Tick is one observation of a forming (not yet closed) 1-minute candle.
// CloseTime is not a field here: it is always OpenTime+1 minute, computed
// by the store rather than taken on faith from a caller (000003's
// candles_duration_one_minute CHECK enforces this regardless, but
// computing it here means a caller cannot even construct an inconsistent
// value).
type Tick struct {
	InstrumentID int64
	OpenTime     clock.EventTime
	Open         quant.Price
	High         quant.Price
	Low          quant.Price
	Close        quant.Price
	Volume       quant.Qty
	TickAligned  bool
	Provider     string
	IngestedAt   clock.IngestionTime
}

func (t Tick) validate() error {
	if t.InstrumentID == 0 {
		return ErrInstrumentIDRequired
	}
	if t.Provider == "" {
		return ErrProviderRequired
	}
	if !t.OpenTime.Time().Equal(t.OpenTime.Time().Truncate(time.Minute)) {
		return fmt.Errorf("marketdata: open_time %s is not minute-aligned: %w", t.OpenTime, ErrNotMinuteAligned)
	}
	if t.Open < 0 || t.High < 0 || t.Low < 0 || t.Close < 0 {
		return ErrOHLCNegative
	}
	if t.Volume < 0 {
		return ErrVolumeNegative
	}
	if t.High < t.Low || t.High < t.Open || t.High < t.Close || t.Low > t.Open || t.Low > t.Close {
		return ErrOHLCInconsistent
	}
	return nil
}

func (t Tick) closeTime() time.Time { return t.OpenTime.Time().Add(time.Minute) }

// Correction describes a corrected value for an already-closed candle.
// There is no OpenTime/InstrumentID field here: those identify which
// current candle is being corrected and are passed separately to
// SupersedeCandle, matching 000003's rule that identity never changes
// in place.
type Correction struct {
	Open        quant.Price
	High        quant.Price
	Low         quant.Price
	Close       quant.Price
	Volume      quant.Qty
	TickAligned bool
	Provider    string
	IngestedAt  clock.IngestionTime
}

func (c Correction) validate() error {
	if c.Provider == "" {
		return ErrProviderRequired
	}
	if c.Open < 0 || c.High < 0 || c.Low < 0 || c.Close < 0 {
		return ErrOHLCNegative
	}
	if c.Volume < 0 {
		return ErrVolumeNegative
	}
	if c.High < c.Low || c.High < c.Open || c.High < c.Close || c.Low > c.Open || c.Low > c.Close {
		return ErrOHLCInconsistent
	}
	return nil
}

func nullTimeFrom(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }
