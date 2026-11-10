//go:build integration

package signupcode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/chibuike-kt/argus/internal/clock"
)

// Run with (needs a real Postgres with all migrations applied):
//
//	go test -tags=integration ./internal/identity/signupcode/...
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
		t.Skipf("integration test database unreachable: %v", err)
	}
	return db
}

const testHMACKey = "test-only-signup-code-hmac-key-not-for-production-use"

// testEmail is already normalized (lowercase) -- t.Name() contains
// uppercase letters, and email_normalized is a lookup key the store
// itself lowercases before writing, so a test querying the row
// directly (as TestIntegration_IssueCode_StoresOnlyTheHMACNeverThePlainCode
// does) must use the same normalized form IssueCode stored it under.
// Unique per call (not just per test), since email_normalized is
// UNIQUE and rows from an earlier run of the same test are not
// cleaned up -- a deterministic, t.Name()-only email would collide
// with (and inherit the resend cooldown of) a leftover row.
func testEmail(t *testing.T) string {
	t.Helper()
	return strings.ToLower(fmt.Sprintf("signupcode-test-%s-%d@example.com", t.Name(), time.Now().UnixNano()))
}

func TestIntegration_IssueCode_StoresOnlyTheHMACNeverThePlainCode(t *testing.T) {
	db := testDB(t)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, testHMACKey)
	email := testEmail(t)

	code, err := store.IssueCode(context.Background(), email, time.Millisecond, 10*time.Minute, clk.Now(), "1")
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code = %q, want 6 digits", code)
	}

	var codeHash string
	if err := db.QueryRow(`SELECT code_hash FROM signup_verifications WHERE email_normalized = $1`, email).Scan(&codeHash); err != nil {
		t.Fatalf("query stored row: %v", err)
	}
	if codeHash == code {
		t.Fatal("code_hash equals the raw code -- the code was stored in plaintext")
	}
	if len(codeHash) != 64 { // hex-encoded SHA-256/HMAC-SHA256 digest
		t.Fatalf("code_hash length = %d, want 64 (hex-encoded 32-byte digest)", len(codeHash))
	}
}

func TestIntegration_VerifyCode_WrongCode_ThenCorrect(t *testing.T) {
	db := testDB(t)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, testHMACKey)
	email := testEmail(t)

	code, err := store.IssueCode(context.Background(), email, time.Millisecond, 10*time.Minute, clk.Now(), "1")
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}

	if _, err := store.VerifyCode(context.Background(), email, wrong, 5, 30*time.Minute); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("VerifyCode(wrong) = %v, want ErrInvalidCode", err)
	}

	ticket, err := store.VerifyCode(context.Background(), email, code, 5, 30*time.Minute)
	if err != nil {
		t.Fatalf("VerifyCode(correct) = %v, want nil", err)
	}
	if len(ticket) == 0 {
		t.Fatal("VerifyCode returned an empty ticket")
	}
}

func TestIntegration_VerifyCode_Expired_Rejected(t *testing.T) {
	db := testDB(t)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, testHMACKey)
	email := testEmail(t)

	code, err := store.IssueCode(context.Background(), email, time.Millisecond, time.Minute, clk.Now(), "1")
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	clk.Advance(2 * time.Minute)

	if _, err := store.VerifyCode(context.Background(), email, code, 5, 30*time.Minute); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("VerifyCode(expired) = %v, want ErrInvalidCode", err)
	}
}

func TestIntegration_IssueCode_ResendCooldown(t *testing.T) {
	db := testDB(t)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, testHMACKey)
	email := testEmail(t)

	if _, err := store.IssueCode(context.Background(), email, time.Hour, 10*time.Minute, clk.Now(), "1"); err != nil {
		t.Fatalf("first IssueCode: %v", err)
	}

	var tooSoon *TooSoonError
	_, err := store.IssueCode(context.Background(), email, time.Hour, 10*time.Minute, clk.Now(), "1")
	if !errors.As(err, &tooSoon) {
		t.Fatalf("second IssueCode within cooldown = %v, want *TooSoonError", err)
	}
	if tooSoon.RetryAfter != time.Hour {
		t.Fatalf("RetryAfter = %s, want 1h (no time has passed on the mutable clock)", tooSoon.RetryAfter)
	}

	clk.Advance(time.Hour + time.Second)
	if _, err := store.IssueCode(context.Background(), email, time.Hour, 10*time.Minute, clk.Now(), "1"); err != nil {
		t.Fatalf("IssueCode after cooldown elapsed: %v", err)
	}
}

func TestIntegration_ConsumeTicket_SingleUse(t *testing.T) {
	db := testDB(t)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	store := NewStore(db, clk, testHMACKey)
	email := testEmail(t)

	code, err := store.IssueCode(context.Background(), email, time.Millisecond, 10*time.Minute, clk.Now(), "1")
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	ticket, err := store.VerifyCode(context.Background(), email, code, 5, 30*time.Minute)
	if err != nil {
		t.Fatalf("VerifyCode: %v", err)
	}

	verification, err := store.ConsumeTicket(context.Background(), ticket)
	if err != nil {
		t.Fatalf("ConsumeTicket: %v", err)
	}
	if verification.Email != email {
		t.Fatalf("verification.Email = %q, want %q", verification.Email, email)
	}

	if _, err := store.ConsumeTicket(context.Background(), ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("second ConsumeTicket (reuse) = %v, want ErrInvalidTicket", err)
	}
}
