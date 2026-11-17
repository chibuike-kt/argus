//go:build integration

package preferences

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"

	_ "github.com/lib/pq"
)

// dbInZone opens the test database with the session TimeZone set to zone
// (Postgres "options" startup parameter), skipping if unreachable.
func dbInZone(t *testing.T, zone string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "options=-c%20timezone%3D" + strings.ReplaceAll(zone, "/", "%2F")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var got string
	if err := db.QueryRowContext(ctx, "SHOW timezone").Scan(&got); err != nil {
		t.Skipf("test database unreachable: %v", err)
	}
	if got != zone {
		t.Fatalf("session timezone = %q, want %q", got, zone)
	}
	return db
}

func newTestUser(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	email := fmt.Sprintf("prefs-tz-%d@example.com", time.Now().UnixNano())
	err := db.QueryRowContext(context.Background(), `
		INSERT INTO users (email, email_normalized, password_hash) VALUES ($1, $1, 'x') RETURNING id`,
		email).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// In a non-UTC session across a DST change (America/New_York, 2026-11-01
// 06:30 UTC, half an hour after clocks fell back) updated_at comes back as
// the exact instant written, in UTC, compared with Equal.
func TestIntegration_Store_UpdatedAtExactInNonUTCSession(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := dbInZone(t, zone)
			at := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
			s := NewStore(db, clock.NewFixed(at))
			uid := newTestUser(t, db)

			doc, err := s.PutPreferences(context.Background(), uid, []byte(`{"timeframe":"1h"}`), 0)
			if err != nil {
				t.Fatal(err)
			}
			if doc.UpdatedAt == nil || !doc.UpdatedAt.Equal(at) || doc.UpdatedAt.Location() != time.UTC {
				t.Fatalf("updated_at = %v, want %v in UTC", doc.UpdatedAt, at)
			}
			got, err := s.GetPreferences(context.Background(), uid)
			if err != nil || got.Version != 1 || !got.UpdatedAt.Equal(at) {
				t.Fatalf("get = %+v %v", got, err)
			}
			if _, err := s.PutPreferences(context.Background(), uid, []byte(`{}`), 0); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("stale write err = %v, want ErrVersionConflict", err)
			}
		})
	}
}
