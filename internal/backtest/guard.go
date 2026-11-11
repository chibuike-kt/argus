package backtest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/chibuike-kt/argus/internal/instruments"
)

// FixtureProviderName is the provider string cmd/argus-worker's own dev
// fixture ingestion path writes onto every instrument it creates
// (devFixtureProviderName in cmd/argus-worker/devfixture.go, not
// importable here -- that file is package main). It is the only
// provider this backtest runner is safe to run against today: ADR-007
// (provider selection) is still open, so there is no vetted real
// provider yet.
const FixtureProviderName = "fixture"

// CheckFixtureOnly refuses to proceed if db already holds a setup for
// any instrument whose own provider is not FixtureProviderName --
// database wide, not scoped to whichever instruments a particular run
// asked to replay: a backtest database that has ever recorded a
// non-fixture setup is not safe to replay the real engine against
// (CLAUDE.md rule 3, stop and flag rather than risk writing alongside
// real or unknown data).
func CheckFixtureOnly(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT instrument_id FROM setups`)
	if err != nil {
		return fmt.Errorf("backtest: list instruments with setups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("backtest: scan instrument id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backtest: list instruments with setups: %w", err)
	}

	instStore := instruments.NewStore(db)
	for _, id := range ids {
		inst, err := instStore.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("backtest: get instrument %d: %w", id, err)
		}
		if inst.Provider != FixtureProviderName {
			return fmt.Errorf("%w: instrument %d (%s), provider %q", ErrNonFixtureInstrument, inst.ID, inst.NativeSymbol, inst.Provider)
		}
	}
	return nil
}
