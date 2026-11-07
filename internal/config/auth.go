package config

import "time"

// AuthPolicy holds every tunable number ADR-015's addendum specifies
// (docs/adr/ADR-015-authentication-and-authorization.md, v0.2.0),
// tagged [R] -- a recommendation the product owner approved, per
// CLAUDE.md rule 3's provenance rule, which keeps [S] for text the
// original ADR itself stated. This is the one place every such number
// lives; nothing in internal/identity hardcodes a default of its own.
//
// Four fields (the LoginDelay* ones) are NOT from the addendum: it
// specifies progressive login delay by name ("5 failures... before
// progressive delay (no hard lockout)") but not a formula. Marked
// explicitly below, not [R] -- see this brief's own report for the
// flag.
type AuthPolicy struct {
	// Argon2id (addendum row 2).
	ArgonMemoryKiB     uint32 `env:"ARGUS_AUTH_ARGON_MEMORY_KIB" envDefault:"65536"` // [R] 64 MiB
	ArgonIterations    uint32 `env:"ARGUS_AUTH_ARGON_ITERATIONS" envDefault:"3"`     // [R]
	ArgonParallelism   uint8  `env:"ARGUS_AUTH_ARGON_PARALLELISM" envDefault:"1"`    // [R]
	ArgonSaltLen       uint32 `env:"ARGUS_AUTH_ARGON_SALT_LEN" envDefault:"16"`      // [R] bytes
	ArgonKeyLen        uint32 `env:"ARGUS_AUTH_ARGON_KEY_LEN" envDefault:"32"`       // [R] bytes
	ArgonMaxConcurrent int    `env:"ARGUS_AUTH_ARGON_MAX_CONCURRENT" envDefault:"4"` // [R] hash ops per process

	// Password policy (addendum row 2).
	PasswordMinLength int `env:"ARGUS_AUTH_PASSWORD_MIN_LENGTH" envDefault:"12"`  // [R]
	PasswordMaxLength int `env:"ARGUS_AUTH_PASSWORD_MAX_LENGTH" envDefault:"128"` // [R]

	// Sessions (addendum row 5).
	SessionIdleDuration     time.Duration `env:"ARGUS_AUTH_SESSION_IDLE_DURATION" envDefault:"168h"`     // [R] 7 days
	SessionAbsoluteDuration time.Duration `env:"ARGUS_AUTH_SESSION_ABSOLUTE_DURATION" envDefault:"720h"` // [R] 30 days
	SensitiveReauthWindow   time.Duration `env:"ARGUS_AUTH_SENSITIVE_REAUTH_WINDOW" envDefault:"10m"`    // [R]
	// SessionTouchThrottle is not addendum text either: ADR-015 says
	// the idle expiry must slide, and this brief's own instruction
	// says "throttle the write to at most once a minute per session"
	// -- that interval itself is this brief's own number, not the
	// addendum's.
	SessionTouchThrottle time.Duration `env:"ARGUS_AUTH_SESSION_TOUCH_THROTTLE" envDefault:"1m"`

	// Email tokens (addendum row 12).
	EmailVerificationTTL time.Duration `env:"ARGUS_AUTH_EMAIL_VERIFICATION_TTL" envDefault:"24h"` // [R]
	PasswordResetTTL     time.Duration `env:"ARGUS_AUTH_PASSWORD_RESET_TTL" envDefault:"1h"`      // [R]

	// Rate limits (addendum row 12).
	LoginFailuresPerAccountPer15Min     int `env:"ARGUS_AUTH_RATE_LOGIN_FAILURES_PER_ACCOUNT" envDefault:"5"`      // [R]
	LoginAttemptsPerIPPer15Min          int `env:"ARGUS_AUTH_RATE_LOGIN_ATTEMPTS_PER_IP" envDefault:"20"`          // [R]
	RegisterPerIPPerHour                int `env:"ARGUS_AUTH_RATE_REGISTER_PER_IP" envDefault:"10"`                // [R]
	ForgotPasswordPerAccountPerHour     int `env:"ARGUS_AUTH_RATE_FORGOT_PASSWORD_PER_ACCOUNT" envDefault:"5"`     // [R]
	ForgotPasswordPerIPPerHour          int `env:"ARGUS_AUTH_RATE_FORGOT_PASSWORD_PER_IP" envDefault:"10"`         // [R]
	ResendVerificationPerAccountPerHour int `env:"ARGUS_AUTH_RATE_RESEND_VERIFICATION_PER_ACCOUNT" envDefault:"5"` // [R]
	ResendVerificationPerIPPerHour      int `env:"ARGUS_AUTH_RATE_RESEND_VERIFICATION_PER_IP" envDefault:"10"`     // [R]

	// LoginDelayStep/LoginDelayMax implement "progressive delay" for
	// login failures past LoginFailuresPerAccountPer15Min -- NOT from
	// the addendum (see this file's own doc comment); flagged in the
	// report, not tagged [R].
	LoginDelayStep time.Duration `env:"ARGUS_AUTH_LOGIN_DELAY_STEP" envDefault:"1s"`
	LoginDelayMax  time.Duration `env:"ARGUS_AUTH_LOGIN_DELAY_MAX" envDefault:"10s"`

	// Cookie (addendum row 5). Which name/Secure apply is decided by
	// Env (CookieName/SecureCookies below), not a separate flag --
	// there is no way to ask for the production cookie shape while
	// Env=local.
	CookieNameProd  string `env:"ARGUS_AUTH_COOKIE_NAME_PROD" envDefault:"__Host-argus_session"` // [R]
	CookieNameLocal string `env:"ARGUS_AUTH_COOKIE_NAME_LOCAL" envDefault:"argus_session"`       // [R]

	// AllowedOrigins is the CSRF defense's own allow-list (addendum row
	// 6): a state-changing request's Origin header must exactly match
	// one of these. Defaults to the Vite dev server's own origins so
	// `docker compose up` plus `pnpm dev` works with no extra setup.
	AllowedOrigins []string `env:"ARGUS_AUTH_ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://localhost:5173,http://127.0.0.1:5173"`

	// TrustProxyForIP: when false (the default), the IP recorded on a
	// session/auth_events row is always the direct connection's own
	// address, never X-Forwarded-For -- trusting that header
	// unconditionally lets any client lie about its IP (the same
	// reason internal/httpapi.NewRouter does not use chi's own
	// middleware.RealIP). Set true only when ARGUS really does sit
	// behind a proxy that sets X-Forwarded-For itself and strips any
	// client-supplied one.
	TrustProxyForIP bool `env:"ARGUS_AUTH_TRUST_PROXY_FOR_IP" envDefault:"false"`
}

// IsProduction is the one place "what counts as production" is
// decided -- everything else (cookie shape, the mailer) asks this,
// rather than comparing Env to a literal string itself.
func (c Config) IsProduction() bool {
	return c.Env == "production"
}

// CookieName is the session cookie's name: the __Host- prefixed form
// in production, the plain form on local http (a __Host- cookie is
// rejected by browsers outright on a non-HTTPS origin, so it cannot be
// used there at all).
func (p AuthPolicy) CookieName(isProduction bool) string {
	if isProduction {
		return p.CookieNameProd
	}
	return p.CookieNameLocal
}

// SecureCookies reports whether the session cookie must carry the
// Secure flag -- true in production, false on local http (ADR-015
// addendum row 5: "Secure (except on local http)").
func (p AuthPolicy) SecureCookies(isProduction bool) bool {
	return isProduction
}
