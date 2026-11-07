package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/session"
)

type fakeDisabledSessionResolver struct {
	sess        session.Session
	revokeCalls int
}

func (f *fakeDisabledSessionResolver) Resolve(context.Context, string) (session.Session, error) {
	return f.sess, nil
}

func (f *fakeDisabledSessionResolver) Touch(context.Context, int64) error { return nil }

func (f *fakeDisabledSessionResolver) Revoke(context.Context, int64, int64) error {
	f.revokeCalls++
	return nil
}

type fakeDisabledUserLookup struct{ user identity.User }

func (f fakeDisabledUserLookup) GetByID(context.Context, int64) (identity.User, error) {
	return f.user, nil
}

// TestRequireSession_DisabledUser_401AndRevokesSession is architect
// review item 3's own middleware test: a session whose user is
// disabled gets 401 -- even though Resolve itself reports it as a
// perfectly valid, non-expired session -- and the session is
// revoked, best-effort, so it stops being listed as active.
func TestRequireSession_DisabledUser_401AndRevokesSession(t *testing.T) {
	disabledAt := time.Now()
	sessions := &fakeDisabledSessionResolver{sess: session.Session{ID: 7, UserID: 1}}
	users := fakeDisabledUserLookup{user: identity.User{ID: 1, DisabledAt: &disabledAt}}

	mw := requireSession(sessions, users, "argus_session", time.Minute, clock.Real{}, slog.Default())
	handlerCalled := false
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { handlerCalled = true }))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "argus_session", Value: "whatever"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if handlerCalled {
		t.Fatal("handler was called for a disabled user's session, want it never reached")
	}
	if sessions.revokeCalls != 1 {
		t.Fatalf("Revoke called %d times, want 1", sessions.revokeCalls)
	}
}

// TestRequireSession_EnabledUser_Passes proves the disabled check
// above does not accidentally reject every session: a user with no
// DisabledAt reaches the handler and nothing gets revoked.
func TestRequireSession_EnabledUser_Passes(t *testing.T) {
	sessions := &fakeDisabledSessionResolver{sess: session.Session{ID: 7, UserID: 1, LastSeenAt: time.Now()}}
	users := fakeDisabledUserLookup{user: identity.User{ID: 1}}

	mw := requireSession(sessions, users, "argus_session", time.Minute, clock.Real{}, slog.Default())
	handlerCalled := false
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { handlerCalled = true }))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "argus_session", Value: "whatever"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the test handler never writes a status, so chi/net-http default to 200)", rec.Code)
	}
	if !handlerCalled {
		t.Fatal("handler was not called for an enabled user's valid session")
	}
	if sessions.revokeCalls != 0 {
		t.Fatalf("Revoke called %d times, want 0", sessions.revokeCalls)
	}
}

// TestClientIP_TrustedProxy_UsesLastEntry is architect review item 7's
// own test: X-Forwarded-For's first entry is whatever the original
// client claimed as its own address -- fully attacker-controlled --
// while the last entry is the one the trusted proxy itself appended.
// A spoofed leading entry must not change the result.
func TestClientIP_TrustedProxy_UsesLastEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 10.0.0.5")

	got := clientIP(req, true)
	if got != "10.0.0.5" {
		t.Fatalf("clientIP(trusted) = %q, want the proxy-appended entry %q, not the client-spoofed leading one", got, "10.0.0.5")
	}
}

// TestClientIP_UntrustedProxy_IgnoresHeaderEntirely confirms the
// default (TrustProxyForIP=false) is unchanged by item 7's fix: the
// header is not consulted at all.
func TestClientIP_UntrustedProxy_IgnoresHeaderEntirely(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 10.0.0.5")

	got := clientIP(req, false)
	if got != "10.0.0.1" {
		t.Fatalf("clientIP(untrusted) = %q, want the direct connection address %q", got, "10.0.0.1")
	}
}
