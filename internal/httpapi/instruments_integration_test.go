//go:build integration

package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	// lib/pq registers the "postgres" driver.
	_ "github.com/lib/pq"
)

// TestIntegration_InstrumentsAndCandlesEndpoints_RealStore proves the
// full route -> apiHandler -> instruments.Store/marketdata.Store ->
// Postgres path for GET /instruments and GET
// /instruments/{id}/candles (worker-hardening brief, Part C) -- in
// particular that prices and volumes really do cross the wire as
// decimal strings built from the instrument's own scale, which a
// handwritten fake (instruments_test.go) cannot verify since it never
// serializes anything through a real column.
func TestIntegration_InstrumentsAndCandlesEndpoints_RealStore(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	_ = oneCandle(t, db, instrumentID, 0, 85)
	_ = oneCandle(t, db, instrumentID, 1, 90)

	r := NewRouter(Deps{Logger: slog.Default(), DB: db})
	srv := httptest.NewServer(r)
	defer srv.Close()

	t.Run("list instruments includes the disabled test instrument", func(t *testing.T) {
		// after_id=instrumentID-1 pages starting exactly at the test
		// instrument -- GET /instruments now defaults to a page of 200
		// ordered by id (backend-guards-cleanup brief, item 2), and a
		// long-lived test database has far more than 200 rows ahead of
		// this freshly-inserted one, so an unparameterized call would
		// no longer reliably include it.
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/instruments?after_id=%d", srv.URL, instrumentID-1))
		if err != nil {
			t.Fatalf("GET /instruments: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got []Instrument
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		found := false
		for _, inst := range got {
			if inst.Id == instrumentID {
				found = true
				if inst.Enabled {
					t.Fatalf("test instrument %d came back enabled -- nothing in instruments.Store can set that", instrumentID)
				}
				if inst.TickSize != "1" {
					t.Fatalf("tick_size = %q, want decimal string \"1\" (scale 0)", inst.TickSize)
				}
			}
		}
		if !found {
			t.Fatalf("instrument %d not in list of %d instruments", instrumentID, len(got))
		}
	})

	t.Run("candles in range, decimal-string OHLCV", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		resp, err := http.Get(candlesURL(srv.URL, instrumentID, from, to))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got []Candle
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d candles, want 2", len(got))
		}
		if got[0].Open != "85" || got[1].Open != "90" {
			t.Fatalf("got opens %q, %q, want decimal strings \"85\", \"90\"", got[0].Open, got[1].Open)
		}
		if !got[0].IsClosed || !got[1].IsClosed {
			t.Fatalf("got %+v, want both candles closed", got)
		}
	})

	t.Run("candles order=desc returns newest first", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		resp, err := http.Get(candlesURLWithOrder(srv.URL, instrumentID, from, to, "desc"))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles?order=desc: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got []Candle
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d candles, want 2", len(got))
		}
		if got[0].Open != "90" || got[1].Open != "85" {
			t.Fatalf("got opens %q, %q, want \"90\", \"85\" (newest first)", got[0].Open, got[1].Open)
		}
	})

	t.Run("candles order=desc with limit=1 returns only the newest", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		u := candlesURLWithOrder(srv.URL, instrumentID, from, to, "desc") + "&limit=1"
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles?order=desc&limit=1: %v", err)
		}
		defer resp.Body.Close()
		var got []Candle
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got[0].Open != "90" {
			t.Fatalf("got %+v, want exactly one candle, open \"90\"", got)
		}
	})

	t.Run("candles order=sideways is a 400", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		resp, err := http.Get(candlesURLWithOrder(srv.URL, instrumentID, from, to, "sideways"))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles?order=sideways: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("candles for an unknown instrument is 404", func(t *testing.T) {
		to := time.Now().UTC()
		from := to.Add(-time.Hour)
		resp, err := http.Get(candlesURL(srv.URL, instrumentID+999999, from, to))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})
}

// candlesURL builds GET /instruments/{id}/candles' own URL with
// from/to properly query-escaped -- time.Time.Format(time.RFC3339)
// can emit a "+" in its UTC offset, which an unescaped
// fmt.Sprintf'd query string turns into a literal space on the server
// side (form-encoding's own "+" means space rule), not the intended
// "+".
func candlesURL(base string, instrumentID int64, from, to time.Time) string {
	q := url.Values{}
	q.Set("from", from.Format(time.RFC3339))
	q.Set("to", to.Format(time.RFC3339))
	return fmt.Sprintf("%s/api/v1/instruments/%d/candles?%s", base, instrumentID, q.Encode())
}

// candlesURLWithOrder is candlesURL's own sibling for the order query
// parameter (web-wiring-followup brief, Part 4).
func candlesURLWithOrder(base string, instrumentID int64, from, to time.Time, order string) string {
	q := url.Values{}
	q.Set("from", from.Format(time.RFC3339))
	q.Set("to", to.Format(time.RFC3339))
	q.Set("order", order)
	return fmt.Sprintf("%s/api/v1/instruments/%d/candles?%s", base, instrumentID, q.Encode())
}
