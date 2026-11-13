//go:build integration

package backtest

import (
	"context"
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
