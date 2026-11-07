package identity

import "context"

type contextKey int

const (
	userContextKey contextKey = iota
	sessionIDContextKey
)

// WithUser attaches the authenticated user to ctx -- the auth
// middleware's own output, set once a session has been resolved and
// validated.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// UserFromContext retrieves the user WithUser attached, or ok=false if
// the request reached this point with no authenticated user (a public
// route, or a bug if called from a route that should have required
// one).
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userContextKey).(User)
	return u, ok
}

// WithSessionID attaches the current request's own session id -- what
// logout and DELETE /auth/sessions/{id}'s "revoke the current one"
// case need, alongside the user WithUser carries.
func WithSessionID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, sessionIDContextKey, id)
}

// SessionIDFromContext retrieves the session id WithSessionID attached.
func SessionIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(sessionIDContextKey).(int64)
	return id, ok
}
