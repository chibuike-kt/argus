package config

import "time"

// insecureSignupCodeHMACKey is SignupCodeHMACKey's own envDefault
// below, repeated here (a struct tag cannot reference a const) so
// Config.Validate can reject it outside local/test without comparing
// against a second copy of the literal.
const insecureSignupCodeHMACKey = "local-dev-only-signup-code-hmac-key-change-me"

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

	// Password reset tokens (addendum row 12). EmailVerificationTTL
	// (the old link-token flow's own TTL) is gone along with
	// Register/VerifyEmail/ResendVerification themselves (ADR-015
	// "Signup and onboarding": "removed so there is a single signup
	// path") -- signup verification now uses SignupCodeTTL below.
	PasswordResetTTL time.Duration `env:"ARGUS_AUTH_PASSWORD_RESET_TTL" envDefault:"1h"` // [R]

	// Rate limits (addendum row 12). RegisterPerIPPerHour/
	// ResendVerificationPer{Account,IP}PerHour are gone with the
	// endpoints they used to throttle; SignupStartsPerIPPerHour/
	// SignupCodesPerEmailPerHour below replace them.
	LoginFailuresPerAccountPer15Min int `env:"ARGUS_AUTH_RATE_LOGIN_FAILURES_PER_ACCOUNT" envDefault:"5"`  // [R]
	LoginAttemptsPerIPPer15Min      int `env:"ARGUS_AUTH_RATE_LOGIN_ATTEMPTS_PER_IP" envDefault:"20"`      // [R]
	ForgotPasswordPerAccountPerHour int `env:"ARGUS_AUTH_RATE_FORGOT_PASSWORD_PER_ACCOUNT" envDefault:"5"` // [R]
	ForgotPasswordPerIPPerHour      int `env:"ARGUS_AUTH_RATE_FORGOT_PASSWORD_PER_IP" envDefault:"10"`     // [R]

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

	// Signup by emailed code and onboarding (ADR-015 "Signup and
	// onboarding", v0.3.0).
	SignupCodeTTL              time.Duration `env:"ARGUS_AUTH_SIGNUP_CODE_TTL" envDefault:"10m"`             // [R]
	SignupCodeMaxWrongGuesses  int           `env:"ARGUS_AUTH_SIGNUP_CODE_MAX_WRONG_GUESSES" envDefault:"5"` // [R]
	SignupCodeResendCooldown   time.Duration `env:"ARGUS_AUTH_SIGNUP_CODE_RESEND_COOLDOWN" envDefault:"60s"` // [R]
	SignupCodesPerEmailPerHour int           `env:"ARGUS_AUTH_RATE_SIGNUP_CODES_PER_EMAIL" envDefault:"5"`   // [R]
	SignupStartsPerIPPerHour   int           `env:"ARGUS_AUTH_RATE_SIGNUP_STARTS_PER_IP" envDefault:"10"`    // [R]
	SignupTicketTTL            time.Duration `env:"ARGUS_AUTH_SIGNUP_TICKET_TTL" envDefault:"30m"`           // [R]

	// SignupCodeHMACKey is the server key an emailed signup code is
	// HMAC-SHA256'd against before storage (ADR-015: "Only an
	// HMAC-SHA256 of the code (server key) is stored, never the code").
	// The addendum specifies the mechanism, not a key value -- there is
	// nothing to default safely outside local/test, so Config.Validate
	// rejects the local placeholder below in production.
	SignupCodeHMACKey string `env:"ARGUS_AUTH_SIGNUP_CODE_HMAC_KEY" envDefault:"local-dev-only-signup-code-hmac-key-change-me"`

	// DisplayNameMaxLength/WatchlistMaxItems (addendum: "1 to 60
	// characters"; "maximum 50").
	DisplayNameMaxLength int `env:"ARGUS_AUTH_DISPLAY_NAME_MAX_LENGTH" envDefault:"60"` // [R]
	WatchlistMaxItems    int `env:"ARGUS_AUTH_WATCHLIST_MAX_ITEMS" envDefault:"50"`     // [R]

	// TermsVersion is stamped onto terms_accepted_at's sibling column at
	// signup start. NOT from the addendum, which specifies that a
	// version is recorded but not what it is -- this brief's own choice
	// of an initial value, flagged in the report, the same as
	// LoginDelayStep/LoginDelayMax above.
	TermsVersion string `env:"ARGUS_AUTH_TERMS_VERSION" envDefault:"1"`

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
//
// Architect review item 8: this used to be true only for the exact
// string "production", so any other value -- "staging", a typo --
// silently got non-Secure cookies and the logging mailer. Inverted:
// only "local" and "test" (this project's own two known-safe,
// non-production environments) opt OUT of production behaviour; any
// other value, known or not, gets it.
func (c Config) IsProduction() bool {
	return c.Env != "local" && c.Env != "test"
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
