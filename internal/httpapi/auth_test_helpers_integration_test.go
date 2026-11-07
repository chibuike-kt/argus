//go:build integration

package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/session"
)

// testAuthDeps builds the Sessions/Users/AuthPolicy/IsProduction/
// Clock fields every NewRouter(Deps{...}) call in this package now
// needs: deny-by-default (ADR-015) means every /api/v1 route,
// including the pre-existing instruments/setups ones these
// not-about-auth integration tests exercise, requires a session.
// Merge this into an existing Deps with the db/logger already set.
func testAuthDeps(db *sql.DB) (Deps, *identity.Store, *session.Store) {
	clk := clock.Real{}
	policy := config.AuthPolicy{
		SessionIdleDuration: 7 * 24 * time.Hour, SessionAbsoluteDuration: 30 * 24 * time.Hour,
		CookieNameLocal: "argus_session",
	}
	users := identity.NewStore(db)
	sessions := session.NewStore(db, clk, policy.SessionIdleDuration, policy.SessionAbsoluteDuration)
	return Deps{Sessions: sessions, Users: users, AuthPolicy: policy, IsProduction: false, Clock: clk}, users, sessions
}

// authenticatedClient creates a fresh verified test user and a real
// session for it (bypassing the HTTP register/verify/login flow
// entirely -- these tests are about instruments/setups, not auth),
// and returns an *http.Client whose cookie jar already carries that
// session's cookie for srv's own origin.
func authenticatedClient(t *testing.T, serverURL string, users *identity.Store, sessions *session.Store) *http.Client {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("httpapi-router-test-%d@example.com", time.Now().UnixNano())
	u, err := users.Insert(ctx, email, "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA")
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	rawToken, _, err := sessions.Create(ctx, u.ID, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("create test session: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	jar.SetCookies(parsed, []*http.Cookie{{Name: "argus_session", Value: rawToken}})
	return &http.Client{Jar: jar}
}
