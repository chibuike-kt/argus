//go:build integration

package backtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"
)

// TestIntegration_CheckFixtureOnly_RefusesNonFixtureInstrument seeds a
// setup for an instrument whose own provider is deliberately not
// FixtureProviderName, and confirms CheckFixtureOnly refuses. It does
// not assert that the error names THIS test's own instrument
// specifically: CheckFixtureOnly scans the whole database and reports
// the first non-fixture instrument it finds, and this shared argus_test
// database already accumulates "test-provider" setups from every other
// integration test in this repo (including this package's own, and
// confirmed by a real run of this very test) -- whichever one happens
// to have the lowest instrument_id is reported, not necessarily this
// one. The behavior under test either way is the same: refuse, and name
// *some* concrete, non-fixture instrument rather than a generic error.
// The converse (an all-fixture-provider database passes) is not
// independently asserted here for the same reason: this shared database
// can never be all-fixture without wiping it, which this test does not
// do.
func TestIntegration_CheckFixtureOnly_RefusesNonFixtureInstrument(t *testing.T) {
	db := testDB(t)

	instStore := instruments.NewStore(db)
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)
	inst, err := instStore.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider:           "not-fixture",
		NativeSymbol:       fmt.Sprintf("GUARD-%s-%d", t.Name(), time.Now().UnixNano()),
		AssetClass:         "crypto_perp",
		PriceScale:         &priceScale,
		QtyScale:           &qtyScale,
		TickSize:           1,
		QtyStepSize:        1,
		SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("create non-fixture instrument: %v", err)
	}

	setupsStore := setups.NewStore(db)
	plan := analysis.TradePlan{
		Status: analysis.PlanActionable, Direction: analysis.DirectionBullish,
		Entry: 110, Stop: 70,
		POI: analysis.POI{Type: analysis.POIOrderBlock, Direction: analysis.DirectionBullish, OrderBlockCandleID: 1},
	}
	if _, _, err := setups.Reconcile(context.Background(), setupsStore, inst.ID, "1m", plan, "v1", et(0), clock.Real{}.Now()); err != nil {
		t.Fatalf("seed a setup for the non-fixture instrument: %v", err)
	}

	err = CheckFixtureOnly(context.Background(), db)
	if err == nil {
		t.Fatal("CheckFixtureOnly: want an error, got nil")
	}
	if !errors.Is(err, ErrNonFixtureInstrument) {
		t.Errorf("CheckFixtureOnly error = %v, want errors.Is ErrNonFixtureInstrument", err)
	}
	// Names a concrete, non-fixture provider value -- not necessarily
	// this test's own instrument (see the doc comment above).
	if !strings.Contains(err.Error(), "provider \"") {
		t.Errorf("CheckFixtureOnly error does not name a concrete provider: %v", err)
	}
}

// TestIntegration_CheckAllowedProviders_AdditivelyAllowsAnExtraProvider
// confirms --allow-provider's own mechanism (internal/backtest.
// CheckAllowedProviders) actually relaxes the guard for a provider
// explicitly named, without needing the whole shared argus_test
// database to be free of every OTHER integration test's own
// "test-provider"/"not-fixture" pollution (this package's sibling
// test above explains why that pollution exists and cannot be
// avoided): this test allows every distinct provider already present
// PLUS its own new one, so a pre-existing unrelated non-fixture row
// from another test cannot make this assertion flaky.
func TestIntegration_CheckAllowedProviders_AdditivelyAllowsAnExtraProvider(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	existing, err := distinctProvidersInSetups(ctx, db)
	if err != nil {
		t.Fatalf("list existing providers: %v", err)
	}

	instStore := instruments.NewStore(db)
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)
	newProvider := fmt.Sprintf("allow-test-%d", time.Now().UnixNano())
	inst, err := instStore.Insert(ctx, instruments.NewInstrumentParams{
		Provider:           newProvider,
		NativeSymbol:       fmt.Sprintf("ALLOW-%s-%d", t.Name(), time.Now().UnixNano()),
		AssetClass:         "crypto_perp",
		PriceScale:         &priceScale,
		QtyScale:           &qtyScale,
		TickSize:           1,
		QtyStepSize:        1,
		SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("create instrument for new provider: %v", err)
	}

	setupsStore := setups.NewStore(db)
	plan := analysis.TradePlan{
		Status: analysis.PlanActionable, Direction: analysis.DirectionBullish,
		Entry: 110, Stop: 70,
		POI: analysis.POI{Type: analysis.POIOrderBlock, Direction: analysis.DirectionBullish, OrderBlockCandleID: 1},
	}
	if _, _, err := setups.Reconcile(ctx, setupsStore, inst.ID, "1m", plan, "v1", et(0), clock.Real{}.Now()); err != nil {
		t.Fatalf("seed a setup for the new provider's instrument: %v", err)
	}

	// Fixture-only (the default, no --allow-provider) must still
	// refuse: the new provider is genuinely not on that list.
	if err := CheckAllowedProviders(ctx, db, []string{FixtureProviderName}); err == nil {
		t.Fatal("CheckAllowedProviders(fixture-only) = nil, want an error for the new, unallowed provider")
	}

	// Allowing it explicitly (what --allow-provider does) must pass,
	// alongside every other provider already present so unrelated
	// pollution from other tests cannot fail this assertion.
	allowed := append([]string{FixtureProviderName, newProvider}, existing...)
	if err := CheckAllowedProviders(ctx, db, allowed); err != nil {
		t.Errorf("CheckAllowedProviders with the new provider explicitly allowed = %v, want nil", err)
	}
}

func distinctProvidersInSetups(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT i.provider FROM instruments i JOIN setups s ON s.instrument_id = i.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
