// Package instruments is a feature module boundary (see ADR-003).
// Canonical instrument registry and enablement (ADR-006).
//
// Enablement cannot be set through this package: InsertInstrument always
// writes enabled = FALSE (db/queries/instruments.sql), and no query or
// method here ever sets it to TRUE. That stays true until ADR-007
// (provider selection and commercial data rights) resolves; there is no
// code path in this package to flip it, deliberately.
package instruments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// ErrNotFound is returned by Get methods when no matching row exists.
var ErrNotFound = errors.New("instruments: not found")

// ErrAlreadyExists is returned by Insert when (provider, native_symbol)
// already has a row (the UNIQUE constraint from 000001).
var ErrAlreadyExists = errors.New("instruments: provider and native symbol already registered")

const uniqueViolation = "23505"

// Store is the instrument registry's data-access layer.
type Store struct {
	q *store.Queries
}

// NewStore wraps db (a *sql.DB or, inside a transaction, a *sql.Tx) in the
// generated query layer.
func NewStore(db store.DBTX) *Store {
	return &Store{q: store.New(db)}
}

// Insert registers a new instrument. It fails if PriceScale or QtyScale is
// unset (see NewInstrumentParams), and fails with ErrAlreadyExists if the
// (provider, native_symbol) pair is already registered.
func (s *Store) Insert(ctx context.Context, p NewInstrumentParams) (Instrument, error) {
	if err := p.validate(); err != nil {
		return Instrument{}, err
	}

	metadata := p.Metadata
	if metadata == nil {
		metadata = json.RawMessage(`{}`)
	}
	intervals := p.SupportedIntervals
	if intervals == nil {
		intervals = []string{}
	}

	row, err := s.q.InsertInstrument(ctx, store.InsertInstrumentParams{
		Provider:           p.Provider,
		NativeSymbol:       p.NativeSymbol,
		AssetClass:         p.AssetClass,
		BaseAsset:          ptrToNullString(p.BaseAsset),
		QuoteAsset:         ptrToNullString(p.QuoteAsset),
		ContractType:       ptrToNullString(p.ContractType),
		PriceScale:         int32(*p.PriceScale),
		QtyScale:           int32(*p.QtyScale),
		TickSize:           int64(p.TickSize),
		QtyStepSize:        int64(p.QtyStepSize),
		ContractSize:       ptrQtyToNullInt64(p.ContractSize),
		PipSize:            ptrPriceToNullInt64(p.PipSize),
		SupportedIntervals: intervals,
		Metadata:           metadata,
	})
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == uniqueViolation {
			return Instrument{}, fmt.Errorf("instruments: insert %s/%s: %w", p.Provider, p.NativeSymbol, ErrAlreadyExists)
		}
		return Instrument{}, fmt.Errorf("instruments: insert %s/%s: %w", p.Provider, p.NativeSymbol, err)
	}
	return fromRow(row)
}

// GetByID returns the instrument with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (Instrument, error) {
	row, err := s.q.GetInstrumentByID(ctx, id)
	if err != nil {
		return Instrument{}, fmt.Errorf("instruments: get id=%d: %w", id, translateNotFound(err))
	}
	return fromRow(row)
}

// GetByProviderAndSymbol returns the instrument registered under provider
// and symbol, or ErrNotFound.
func (s *Store) GetByProviderAndSymbol(ctx context.Context, provider, symbol string) (Instrument, error) {
	row, err := s.q.GetInstrumentByProviderAndSymbol(ctx, store.GetInstrumentByProviderAndSymbolParams{
		Provider:     provider,
		NativeSymbol: symbol,
	})
	if err != nil {
		return Instrument{}, fmt.Errorf("instruments: get %s/%s: %w", provider, symbol, translateNotFound(err))
	}
	return fromRow(row)
}

// ListEnabled returns every instrument with enabled = TRUE. Since nothing
// in this package can set that flag, this returns an empty slice until
// enablement is driven some other way (ADR-007).
func (s *Store) ListEnabled(ctx context.Context) ([]Instrument, error) {
	rows, err := s.q.ListEnabledInstruments(ctx)
	if err != nil {
		return nil, fmt.Errorf("instruments: list enabled: %w", err)
	}
	out := make([]Instrument, 0, len(rows))
	for _, row := range rows {
		inst, err := fromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

func translateNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func ptrToNullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func nullStringToPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func ptrQtyToNullInt64(p *quant.Qty) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func ptrPriceToNullInt64(p *quant.Price) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}
