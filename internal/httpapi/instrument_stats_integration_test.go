//go:build integration

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// seedStatsSeries bulk-inserts current, closed 1-minute candles i =
// from..to (open_time = start + i minutes, close = 10000 + i, volume
// 2) in one statement.
func seedStatsSeries(t *testing.T, db *sql.DB, instrumentID int64, start time.Time, from, to int) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO candles (instrument_id, open_time, close_time, open, high, low, close, volume,
		                     revision, is_closed, tick_aligned, provider, ingested_at)
		SELECT $1, $2::timestamptz + i * interval '1 minute', $2::timestamptz + (i + 1) * interval '1 minute',
		       10000 + i, 10000 + i, 10000 + i, 10000 + i, 2, 1, TRUE, TRUE, 'test', now()
		FROM generate_series($3::int, $4::int) AS i`, instrumentID, start, from, to)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func enableInstrument(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, id); err != nil {
		t.Fatalf("enable instrument: %v", err)
	}
}

// TestIntegration_InstrumentStats_ProductionRouter runs GET
// /instruments/stats through the production router and middleware
// chain (NewRouter: request id, logging, recoverer, 30s timeout, body
// limit, CSRF, deny-by-default session) served by the production
// NewServer, against the real store.
func TestIntegration_InstrumentStats_ProductionRouter(t *testing.T) {
	db := testDB(t)
	deps, users, sessions := testAuthDeps(db)
	deps.Logger, deps.DB = slog.Default(), db
	base := startRealServer(t, NewRouter(deps), 15*time.Second, 30*time.Second)
	client := authenticatedClient(t, base, users, sessions)

	start := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	full := testInstrument(t, db) // scale 0 prices and quantities
	enableInstrument(t, db, full)
	seedStatsSeries(t, db, full, start, 0, 1440)

	short := testInstrument(t, db)
	enableInstrument(t, db, short)
	seedStatsSeries(t, db, short, start, 1000, 1440)

	disabled := testInstrument(t, db) // never enabled
	seedStatsSeries(t, db, disabled, start, 0, 1440)

	url := fmt.Sprintf("%s/api/v1/instruments/stats?ids=%d,%d,%d,999999999", base, full, short, disabled)

	t.Run("unauthenticated is 401", func(t *testing.T) {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("authenticated: real values, absent as null, disabled and unknown omitted", func(t *testing.T) {
		resp, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.StatusCode, body)
		}
		var got []map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d rows, want 2 (disabled and unknown omitted): %s", len(got), body)
		}
		f, s := got[0], got[1]
		if int64(f["instrument_id"].(float64)) != full || int64(s["instrument_id"].(float64)) != short {
			t.Fatalf("order: %s", body)
		}
		// Hand-computed at scale 0: latest close 11440, reference 10000.
		// change 1440, 14.40%; volume 1440 x 2 = 2880.
		if f["last_close"] != "11440" || f["change_24h"] != "1440" || f["change_24h_pct"] != "14.40" || f["volume_24h"] != "2880" {
			t.Fatalf("full row = %v", f)
		}
		if f["last_closed_open_time"] != "2026-11-11T00:00:00Z" {
			t.Fatalf("last_closed_open_time = %v", f["last_closed_open_time"])
		}
		if s["change_24h"] != nil || s["change_24h_pct"] != nil || s["volume_24h"] != nil {
			t.Fatalf("short row = %v, want change/volume null", s)
		}
		if !strings.Contains(string(body), `"change_24h":null`) {
			t.Fatalf("absent value not sent as explicit null: %s", body)
		}
	})

	t.Run("bad ids are 400 in the API error shape", func(t *testing.T) {
		ids := make([]string, 51)
		for i := range ids {
			ids[i] = fmt.Sprint(i + 1)
		}
		for _, q := range []string{"ids=abc", "", "ids=" + strings.Join(ids, ",")} {
			resp, err := client.Get(base + "/api/v1/instruments/stats?" + q)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"code":"invalid_argument"`) {
				t.Fatalf("%q: status %d body %s, want 400 invalid_argument", q, resp.StatusCode, body)
			}
		}
	})
}

// TestIntegration_InstrumentStats_RequestTimeout proves the endpoint is
// inside the router's 30s middleware.Timeout: with the candles table
// locked so the stats statement blocks, the client gets an error
// response at about 30 seconds instead of hanging, and the error body
// does not leak the database error. Slow (30+ real seconds) and it
// briefly blocks every other reader of candles in the test database, so
// it runs only with ARGUS_RUN_SLOW_TESTS=1.
func TestIntegration_InstrumentStats_RequestTimeout(t *testing.T) {
	if os.Getenv("ARGUS_RUN_SLOW_TESTS") != "1" {
		t.Skip("slow: set ARGUS_RUN_SLOW_TESTS=1")
	}
	db := testDB(t)
	deps, users, sessions := testAuthDeps(db)
	deps.Logger, deps.DB = slog.Default(), db
	base := startRealServer(t, NewRouter(deps), 15*time.Second, 60*time.Second)
	client := authenticatedClient(t, base, users, sessions)
	client.Timeout = 50 * time.Second

	id := testInstrument(t, db)
	enableInstrument(t, db, id)
	seedStatsSeries(t, db, id, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), 0, 10)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`LOCK TABLE candles IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	began := time.Now()
	resp, err := client.Get(fmt.Sprintf("%s/api/v1/instruments/stats?ids=%d", base, id))
	elapsed := time.Since(began)
	_ = tx.Rollback()
	if err != nil {
		t.Fatalf("request failed after %v (client timeout 50s): %v", elapsed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	t.Logf("blocked stats request: status %d after %v, body %s", resp.StatusCode, elapsed, body)
	if resp.StatusCode < 500 {
		t.Fatalf("status = %d, want a 5xx", resp.StatusCode)
	}
	if elapsed < 29*time.Second || elapsed > 40*time.Second {
		t.Fatalf("elapsed = %v, want about 30s (the router's request timeout)", elapsed)
	}
	if strings.Contains(strings.ToLower(string(body)), "cancel") || strings.Contains(string(body), "pq:") {
		t.Fatalf("database error leaked to client: %s", body)
	}
}
