//go:build integration

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/setups"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// TestIntegration_SetupsEndpoints_RealStore proves the full route ->
// apiHandler -> *setups.Store -> Postgres path, including the
// PlanSnapshot JSONB round-trip -- the one thing the handwritten-fake
// unit tests in setups_test.go cannot verify, since a fake never
// serializes anything through a real column.
// Run with: go test -tags=integration ./internal/httpapi/...
// against a Postgres with all migrations applied (make migrate-up).
func TestIntegration_SetupsEndpoints_RealStore(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := setups.NewStore(db)
	ctx := context.Background()

	candleID := oneCandle(t, db, instrumentID, 1, 85)
	plan := analysis.TradePlan{
		Status: analysis.PlanActionable, Direction: analysis.DirectionBullish,
		Entry: quant.Price(90), Stop: quant.Price(70),
		POI: analysis.POI{
			Type: analysis.POIOrderBlock, Direction: analysis.DirectionBullish,
			Low: quant.Price(80), High: quant.Price(90),
			OrderBlockCandleID: candleID, OrderBlockCandleRevision: 1, InDiscount: true,
		},
	}
	created, _, err := setups.Reconcile(ctx, store, instrumentID, "1m", plan, "swing-structure-v1-test", time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	deps, users, sessions := testAuthDeps(db)
	deps.Logger, deps.DB = slog.Default(), db
	r := NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()
	client := authenticatedClient(t, srv.URL, users, sessions)

	t.Run("list", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/setups?instrument_id=%d", srv.URL, instrumentID))
		if err != nil {
			t.Fatalf("GET /setups: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got []Setup
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got[0].Id != created.ID {
			t.Fatalf("got %+v, want one setup with id %d", got, created.ID)
		}
		// Decimal strings built from the test instrument's own
		// price_scale (0 here, see testInstrument) -- "90"/"70", not the
		// bare scaled integers 90/70 the API sent before Part 3.
		snap := got[0].PlanSnapshot
		if snap.Status != "actionable" || snap.Entry != "90" || snap.Stop != "70" {
			t.Fatalf("plan_snapshot round-trip = %+v, want status=actionable entry=90 stop=70", snap)
		}
		if snap.Poi.Low != "80" || snap.Poi.High != "90" {
			t.Fatalf("plan_snapshot.poi round-trip = %+v, want low=80 high=90", snap.Poi)
		}
	})

	t.Run("get by id", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/setups/%d", srv.URL, created.ID))
		if err != nil {
			t.Fatalf("GET /setups/{id}: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got Setup
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Id != created.ID || got.PoiSourceCandleId != candleID {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("get by id not found", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/setups/%d", srv.URL, created.ID+999999))
		if err != nil {
			t.Fatalf("GET /setups/{id}: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})

	t.Run("events", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/setups/%d/events", srv.URL, created.ID))
		if err != nil {
			t.Fatalf("GET /setups/{id}/events: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got []SetupEvent
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// Creation state (spec v0.12.0 section 15.2, batch 8): plan's
		// own Status is Actionable from the first evaluation, so
		// Reconcile records detected then, in the same call, actionable
		// -- two events, not one.
		wantTypes := []SetupEventEventType{"detected", "actionable"}
		if len(got) != len(wantTypes) {
			t.Fatalf("got %+v, want %d events %v", got, len(wantTypes), wantTypes)
		}
		for i, want := range wantTypes {
			if got[i].EventType != want {
				t.Fatalf("got[%d].EventType = %v, want %v", i, got[i].EventType, want)
			}
		}
	})
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration test database unreachable at %s: %v", dsn, err)
	}
	return db
}

func testInstrument(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)
	symbol := fmt.Sprintf("HTTPAPI-%s-%d", t.Name(), time.Now().UnixNano())

	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "test-provider", NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
	})
	if err != nil {
		t.Fatalf("create test instrument: %v", err)
	}
	return inst.ID
}

func oneCandle(t *testing.T, db *sql.DB, instrumentID int64, minute int, price int64) int64 {
	t.Helper()
	mdStore := marketdata.NewStore(db, clock.Real{})
	openTime := clock.NewEventTime(time.Date(2026, 11, 6, 12, minute, 0, 0, time.UTC))
	tick := marketdata.Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: quant.Price(price), High: quant.Price(price), Low: quant.Price(price), Close: quant.Price(price),
		Volume: 1, Provider: "test", IngestedAt: clock.NewIngestionTime(time.Now()),
	}
	if _, err := mdStore.UpsertForming(context.Background(), tick); err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}
	closed, err := mdStore.Close(context.Background(), instrumentID, openTime)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return closed.ID
}
