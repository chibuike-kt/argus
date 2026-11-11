//go:build integration

package tokens

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

// testDB defaults to a separate argus_test database, never the dev
// argus database (this brief's own rule) -- set
// ARGUS_TEST_DATABASE_URL to override. Run with:
//
//	go test -tags=integration ./internal/identity/tokens/...
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
	email := fmt.Sprintf("tokens-test-%d@example.com", time.Now().UnixNano())
	now := time.Now()
	u, err := identity.NewStore(db).InsertVerified(context.Background(), email, "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", now, now, "test")
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return u.ID
}

func TestIntegration_IssueConsume_HappyPath(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk)
	ctx := context.Background()

	rawToken, issued, err := store.Issue(ctx, userID, PurposeVerifyEmail, 24*time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.UserID != userID || issued.Purpose != PurposeVerifyEmail {
		t.Fatalf("Issue returned %+v", issued)
	}

	consumed, err := store.Consume(ctx, rawToken, PurposeVerifyEmail)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.ID != issued.ID {
		t.Fatalf("Consume returned token %d, want %d", consumed.ID, issued.ID)
	}
}

func TestIntegration_Consume_SingleUse(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk)
	ctx := context.Background()

	rawToken, _, err := store.Issue(ctx, userID, PurposeVerifyEmail, 24*time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := store.Consume(ctx, rawToken, PurposeVerifyEmail); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := store.Consume(ctx, rawToken, PurposeVerifyEmail); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second Consume (reuse) = %v, want ErrInvalid", err)
	}
}

func TestIntegration_Consume_Expired(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk)
	ctx := context.Background()

	rawToken, _, err := store.Issue(ctx, userID, PurposeVerifyEmail, time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	clk.Advance(61 * time.Minute)
	if _, err := store.Consume(ctx, rawToken, PurposeVerifyEmail); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Consume(expired) = %v, want ErrInvalid", err)
	}
}

func TestIntegration_Consume_WrongPurposeOrGarbage(t *testing.T) {
	db := testDB(t)
	userID := testUser(t, db)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk)
	ctx := context.Background()

	rawToken, _, err := store.Issue(ctx, userID, PurposeVerifyEmail, 24*time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := store.Consume(ctx, rawToken, PurposeResetPassword); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Consume(right token, wrong purpose) = %v, want ErrInvalid", err)
	}
	if _, err := store.Consume(ctx, "not-a-real-token", PurposeVerifyEmail); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Consume(garbage) = %v, want ErrInvalid", err)
	}
}
