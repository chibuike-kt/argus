package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/session"
)

// publicAPIPaths is the deny-by-default allow-list (ADR-015): every
// /api/v1 route requires a session unless its exact path is listed
// here. A new route added without thinking is protected by
// construction -- it is simply absent from this map, not explicitly
// exempted. /healthz, /readyz live outside /api/v1 entirely (NewRouter
// mounts them before this middleware's own Route group), so they need
// no entry here to be reachable without a session.
var publicAPIPaths = map[string]bool{
	"/api/v1/ping":                     true,
	"/api/v1/auth/register":            true,
	"/api/v1/auth/verify-email":        true,
	"/api/v1/auth/resend-verification": true,
	"/api/v1/auth/login":               true,
	"/api/v1/auth/forgot-password":     true, // Part 2 route; pre-listed now (ADR-015 names it public), inert until it exists
	"/api/v1/auth/reset-password":      true, // Part 2 route; same as above
}

// sessionResolver is the slice of *session.Store requireSession
// actually needs -- a consumer-defined interface (this brief's own
// rule 7) so a test can satisfy it with a hand-written fake instead of
// a real Postgres connection.
type sessionResolver interface {
	Resolve(ctx context.Context, rawToken string) (session.Session, error)
	Touch(ctx context.Context, sessionID int64) error
	Revoke(ctx context.Context, sessionID, userID int64) error
}

// userLookup is the slice of *identity.Store requireSession actually
// needs.
type userLookup interface {
	GetByID(ctx context.Context, id int64) (identity.User, error)
}

// requireSession is the one place deny-by-default is enforced: every
// request to a non-public path must resolve to a valid, non-revoked,
// non-expired session, or the request never reaches a handler at all.
func requireSession(sessions sessionResolver, users userLookup, cookieName string, touchThrottle time.Duration, clk clock.Clock, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicAPIPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(cookieName)
			if err != nil {
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "no session")
				return
			}

			sess, err := sessions.Resolve(r.Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, session.ErrUnavailable) {
					// ADR-015: never treat a database failure as
					// authenticated or as a missing session -- 503,
					// distinct from the 401 every other failure below
					// gets.
					writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "session store unavailable")
					return
				}
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "invalid session")
				return
			}

			// Slide the idle expiry, throttled: only once touchThrottle
			// has actually elapsed since last_seen_at, not on every
			// request (ADR-015's own instruction, to avoid a write per
			// request).
			if clk.Now().Sub(sess.LastSeenAt) >= touchThrottle {
				if terr := sessions.Touch(r.Context(), sess.ID); terr != nil {
					logger.Warn("auth middleware: touch session", "session_id", sess.ID, "error", terr)
				}
			}

			user, err := users.GetByID(r.Context(), sess.UserID)
			if err != nil {
				// A session row with no matching user is a data
				// inconsistency, not a client error -- 503 rather than
				// silently treating it as "not authenticated" (the
				// user genuinely might be authenticated; something is
				// just broken).
				writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "could not load session user")
				return
			}

			// Architect review item 3: disabled_at existed but nothing
			// enforced it at the session layer -- a disabled account's
			// existing session kept working indefinitely. Best-effort
			// revoke (logged, not fatal: the 401 below holds regardless,
			// since this check runs again on the account's very next
			// request either way) so GET /auth/sessions stops listing a
			// now-pointless session too.
			if user.Disabled() {
				if rerr := sessions.Revoke(r.Context(), sess.ID, user.ID); rerr != nil {
					logger.Warn("auth middleware: revoke disabled user's session", "session_id", sess.ID, "user_id", user.ID, "error", rerr)
				}
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "account disabled")
				return
			}

			ctx := identity.WithUser(r.Context(), user)
			ctx = identity.WithSessionID(ctx, sess.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// csrfProtection enforces ADR-015 addendum row 6: a state-changing
// request must send Content-Type: application/json and an Origin
// header that exactly matches one of allowedOrigins. GET/HEAD/OPTIONS
// are read-only and carry no state-changing risk, so they pass
// through unchecked.
func csrfProtection(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				writeAPIError(w, http.StatusForbidden, "csrf_rejected", "Content-Type must be application/json")
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" || !allowed[origin] {
				writeAPIError(w, http.StatusForbidden, "csrf_rejected", "Origin header missing or not allowed")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestMetaMiddleware stashes ip/User-Agent into context for every
// /api/v1 request, public or not -- auth.go's own handlers (register,
// login, ...) read them back via requestMeta to record auth_events
// rows and apply rate limits, without needing *http.Request directly
// (the strict-server pattern only hands a handler a context.Context).
func requestMetaMiddleware(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, trustProxy)
			ua := r.Header.Get("User-Agent")
			next.ServeHTTP(w, r.WithContext(withRequestMeta(r.Context(), ip, ua)))
		})
	}
}

// clientIP returns the direct connection's own address, never
// X-Forwarded-For, unless trustProxy says this deployment really does
// sit behind a proxy that sets that header itself (ADR-015; the same
// reason NewRouter does not use chi's own middleware.RealIP).
//
// When trusted, this takes the LAST entry in X-Forwarded-For, not the
// first (architect review item 7): the header is a comma-separated
// list each hop appends its own observed client to, so the first
// entry is whatever the ORIGINAL client claimed as its own address --
// fully attacker-controlled -- while the last entry is the one the
// trusted proxy itself appended, the only entry this deployment did
// not just take the client's word for.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}
