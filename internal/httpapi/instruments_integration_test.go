//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	deps, users, sessions := testAuthDeps(db)
	deps.Logger, deps.DB = slog.Default(), db
	r := NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()
	client := authenticatedClient(t, srv.URL, users, sessions)

	t.Run("list instruments includes the disabled test instrument", func(t *testing.T) {
		// after_id=instrumentID-1 pages starting exactly at the test
		// instrument -- GET /instruments now defaults to a page of 200
		// ordered by id (backend-guards-cleanup brief, item 2), and a
		// long-lived test database has far more than 200 rows ahead of
		// this freshly-inserted one, so an unparameterized call would
		// no longer reliably include it. enabled=all is also required
		// now (web-wiring-polish brief, item 2): the default changed to
		// enabled=true only, and this test's own instrument is always
		// disabled (instruments.Store.Insert's own doc comment).
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instruments?after_id=%d&enabled=all", srv.URL, instrumentID-1))
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

	t.Run("enabled filter: true (default), false, all", func(t *testing.T) {
		// instrumentID (this test function's own fixture) is disabled;
		// a second instrument, enabled via a raw UPDATE (Insert itself
		// can never set it, ADR-007), gives one of each.
		enabledID := testInstrument(t, db)
		if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, enabledID); err != nil {
			t.Fatalf("UPDATE enabled = TRUE: %v", err)
		}
		beforeBoth := instrumentID - 1
		if enabledID < instrumentID {
			beforeBoth = enabledID - 1
		}

		fetch := func(query string) []Instrument {
			t.Helper()
			resp, err := client.Get(fmt.Sprintf("%s/api/v1/instruments?after_id=%d%s", srv.URL, beforeBoth, query))
			if err != nil {
				t.Fatalf("GET /instruments%s: %v", query, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			var got []Instrument
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			return got
		}
		hasID := func(rows []Instrument, id int64) bool {
			for _, r := range rows {
				if r.Id == id {
					return true
				}
			}
			return false
		}

		// Default (no ?enabled= at all): enabled only (web-wiring-polish
		// brief, item 2's own behaviour change).
		defaultRows := fetch("")
		if !hasID(defaultRows, enabledID) || hasID(defaultRows, instrumentID) {
			t.Fatalf("default: got ids %+v, want only the enabled one (%d), not the disabled one (%d)", defaultRows, enabledID, instrumentID)
		}

		trueRows := fetch("&enabled=true")
		if !hasID(trueRows, enabledID) || hasID(trueRows, instrumentID) {
			t.Fatalf("enabled=true: got ids %+v, want only %d", trueRows, enabledID)
		}

		falseRows := fetch("&enabled=false")
		if hasID(falseRows, enabledID) || !hasID(falseRows, instrumentID) {
			t.Fatalf("enabled=false: got ids %+v, want only %d", falseRows, instrumentID)
		}

		allRows := fetch("&enabled=all")
		if !hasID(allRows, enabledID) || !hasID(allRows, instrumentID) {
			t.Fatalf("enabled=all: got ids %+v, want both %d and %d", allRows, enabledID, instrumentID)
		}

		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instruments?enabled=maybe", srv.URL))
		if err != nil {
			t.Fatalf("GET /instruments?enabled=maybe: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("enabled=maybe: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("candles in range, decimal-string OHLCV", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		resp, err := client.Get(candlesURL(srv.URL, instrumentID, from, to))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got CandleHistory
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Candles) != 2 {
			t.Fatalf("got %d candles, want 2", len(got.Candles))
		}
		if got.Candles[0].Open != "85" || got.Candles[1].Open != "90" {
			t.Fatalf("got opens %q, %q, want decimal strings \"85\", \"90\"", got.Candles[0].Open, got.Candles[1].Open)
		}
		if !got.Candles[0].IsClosed || !got.Candles[1].IsClosed {
			t.Fatalf("got %+v, want both candles closed", got)
		}
	})

	t.Run("candles order=desc returns newest first", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		resp, err := client.Get(candlesURLWithOrder(srv.URL, instrumentID, from, to, "desc"))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles?order=desc: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var got CandleHistory
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Candles) != 2 {
			t.Fatalf("got %d candles, want 2", len(got.Candles))
		}
		if got.Candles[0].Open != "90" || got.Candles[1].Open != "85" {
			t.Fatalf("got opens %q, %q, want \"90\", \"85\" (newest first)", got.Candles[0].Open, got.Candles[1].Open)
		}
	})

	t.Run("candles order=desc with limit=1 returns only the newest", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		u := candlesURLWithOrder(srv.URL, instrumentID, from, to, "desc") + "&limit=1"
		resp, err := client.Get(u)
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles?order=desc&limit=1: %v", err)
		}
		defer resp.Body.Close()
		var got CandleHistory
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Candles) != 1 || got.Candles[0].Open != "90" {
			t.Fatalf("got %+v, want exactly one candle, open \"90\"", got)
		}
	})

	t.Run("candles order=desc with no from/to returns newest first (item 1)", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instruments/%d/candles?order=desc&limit=2", srv.URL, instrumentID))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles?order=desc&limit=2: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want %d, body=%s", resp.StatusCode, http.StatusOK, body)
		}
		var got CandleHistory
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Candles) != 2 {
			t.Fatalf("got %d candles, want 2", len(got.Candles))
		}
		if got.Candles[0].Open != "90" || got.Candles[1].Open != "85" {
			t.Fatalf("got opens %q, %q, want \"90\", \"85\" (newest first, no range given at all)", got.Candles[0].Open, got.Candles[1].Open)
		}
	})

	t.Run("candles order=asc with no from is still a 400 (unchanged)", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instruments/%d/candles?order=asc&limit=2", srv.URL, instrumentID))
		if err != nil {
			t.Fatalf("GET /instruments/{id}/candles?order=asc&limit=2: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("candles order=sideways is a 400", func(t *testing.T) {
		from := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
		resp, err := client.Get(candlesURLWithOrder(srv.URL, instrumentID, from, to, "sideways"))
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
		resp, err := client.Get(candlesURL(srv.URL, instrumentID+999999, from, to))
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
