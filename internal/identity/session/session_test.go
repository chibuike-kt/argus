//go:build integration

package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/identity"

	_ "github.com/lib/pq"
)

// These tests exercise the real sessions table and its constraints
// (foreign key to users, the token_hash unique index) -- a handwritten
// fake cannot verify those. Run with:
//
//	go test -tags=integration ./internal/identity/session/...
//
// against a Postgres with all migrations applied. testDB defaults to
// a separate argus_test database, never the dev argus database (this
// brief's own rule) -- set ARGUS_TEST_DATABASE_URL to override.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus_test?sslmode=disable"
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

func testUser(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	email := fmt.Sprintf("session-test-%d@example.com", time.Now().UnixNano())
	now := time.Now()
	u, err := identity.NewStore(db).InsertVerified(context.Background(), email, "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", now, now, "test")
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return u.ID
}

func TestIntegration_CreateResolve_RoundTrip(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, 7*24*time.Hour, 30*24*time.Hour)
	ctx := context.Background()

	rawToken, created, err := store.Create(ctx, userID, "test-agent", "203.0.113.5")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.UserID != userID {
		t.Fatalf("UserID = %d, want %d", created.UserID, userID)
	}

	got, err := store.Resolve(ctx, rawToken)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("Resolve returned session %d, want %d", got.ID, created.ID)
	}

	if _, err := store.Resolve(ctx, "not-a-real-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve(garbage) = %v, want ErrNotFound", err)
	}
}

func TestIntegration_Resolve_IdleExpiry(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, time.Hour, 30*24*time.Hour)
	ctx := context.Background()

	rawToken, _, err := store.Create(ctx, userID, "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(61 * time.Minute)
	if _, err := store.Resolve(ctx, rawToken); !errors.Is(err, ErrExpired) {
		t.Fatalf("Resolve after idle expiry = %v, want ErrExpired", err)
	}
}

func TestIntegration_Resolve_AbsoluteExpiry(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	// Idle duration longer than absolute, so only the absolute check can fire.
	store := NewStore(db, clk, 365*24*time.Hour, time.Hour)
	ctx := context.Background()

	rawToken, _, err := store.Create(ctx, userID, "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(61 * time.Minute)
	if _, err := store.Resolve(ctx, rawToken); !errors.Is(err, ErrExpired) {
		t.Fatalf("Resolve after absolute expiry = %v, want ErrExpired", err)
	}
}

func TestIntegration_Touch_SlidesIdleExpiry(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, time.Hour, 30*24*time.Hour)
	ctx := context.Background()

	rawToken, created, err := store.Create(ctx, userID, "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clk.Advance(50 * time.Minute)
	if err := store.Touch(ctx, created.ID); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	clk.Advance(50 * time.Minute) // 100 minutes since creation, but only 50 since the touch
	if _, err := store.Resolve(ctx, rawToken); err != nil {
		t.Fatalf("Resolve after touch = %v, want nil (idle expiry should have slid)", err)
	}
}

func TestIntegration_Revoke_IsIdempotentAndRevokedSessionFailsResolve(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, 7*24*time.Hour, 30*24*time.Hour)
	ctx := context.Background()

	rawToken, created, err := store.Create(ctx, userID, "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Revoke(ctx, created.ID, userID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := store.Revoke(ctx, created.ID, userID); err != nil {
		t.Fatalf("second Revoke (idempotent) = %v, want nil", err)
	}

	if _, err := store.Resolve(ctx, rawToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Resolve(revoked) = %v, want ErrRevoked", err)
	}
}

func TestIntegration_GetForUser_AnotherUsersSessionIsNotFound(t *testing.T) {
	db := testDB(t)
	owner := testUser(t, db)
	other := testUser(t, db)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, 7*24*time.Hour, 30*24*time.Hour)
	ctx := context.Background()

	_, created, err := store.Create(ctx, owner, "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.GetForUser(ctx, created.ID, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetForUser(owner's session, other user) = %v, want ErrNotFound", err)
	}
	if _, err := store.GetForUser(ctx, created.ID, owner); err != nil {
		t.Fatalf("GetForUser(owner's session, owner) = %v, want nil", err)
	}
}

func TestIntegration_RevokeAllForUser(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, 7*24*time.Hour, 30*24*time.Hour)
	ctx := context.Background()

	tok1, _, err := store.Create(ctx, userID, "", "")
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	tok2, _, err := store.Create(ctx, userID, "", "")
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	if err := store.RevokeAllForUser(ctx, userID); err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}

	for _, tok := range []string{tok1, tok2} {
		if _, err := store.Resolve(ctx, tok); !errors.Is(err, ErrRevoked) {
			t.Fatalf("Resolve after RevokeAllForUser = %v, want ErrRevoked", err)
		}
	}
}
