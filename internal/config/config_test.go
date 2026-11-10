package config

import "testing"

// TestLoadDefaults verifies defaults load and validate when the environment is
// not overridden.
func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Env == "" {
		t.Fatal("expected a default env")
	}
	if cfg.HTTPPort == "" {
		t.Fatal("expected a default http port")
	}
}

// TestValidateRejectsEmpty checks that Validate fails fast on a missing
// required field.
func TestValidateRejectsEmpty(t *testing.T) {
	c := Config{HTTPPort: "8080", RedisURL: "redis://x", ShutdownTimeout: 1}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing database url")
	}
}

func validConfig(env string) Config {
	return Config{
		Env: env, DatabaseURL: "postgres://x", RedisURL: "redis://x",
		HTTPPort: "8080", ShutdownTimeout: 1,
		PublicBaseURL: "https://app.example.com",
		Auth:          AuthPolicy{AllowedOrigins: []string{"https://app.example.com"}},
	}
}

// TestIsProduction_UnknownEnvValueIsProduction is architect review
// item 8's own test: before this, IsProduction was true only for the
// exact string "production", so "staging" (or any typo) silently got
// non-Secure cookies and the logging mailer. Now only "local" and
// "test" opt out.
func TestIsProduction_UnknownEnvValueIsProduction(t *testing.T) {
	for _, env := range []string{"staging", "production", "whatever-typo"} {
		if !validConfig(env).IsProduction() {
			t.Errorf("IsProduction() for Env=%q = false, want true", env)
		}
	}
	for _, env := range []string{"local", "test"} {
		if validConfig(env).IsProduction() {
			t.Errorf("IsProduction() for Env=%q = true, want false", env)
		}
	}
}

// TestValidate_ProductionRejectsNonHTTPSOrigin is architect review
// item 8's second test: a plain-http ARGUS_AUTH_ALLOWED_ORIGINS entry
// must refuse startup in production, since CSRF's own Origin check
// would otherwise accept it despite Secure cookies implying TLS.
func TestValidate_ProductionRejectsNonHTTPSOrigin(t *testing.T) {
	c := validConfig("staging")
	c.Auth.AllowedOrigins = []string{"https://app.example.com", "http://app.example.com"}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with a non-https origin in production = nil, want an error")
	}
}

// TestValidate_NonProductionAllowsHTTPOrigin confirms the check above
// is production-only: local/test keep working with the Vite dev
// server's own http:// default origins.
func TestValidate_NonProductionAllowsHTTPOrigin(t *testing.T) {
	c := validConfig("local")
	c.Auth.AllowedOrigins = []string{"http://localhost:5173"}
	c.PublicBaseURL = "http://localhost:5173"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with an http origin outside production = %v, want nil", err)
	}
}

// TestValidate_ProductionRejectsNonHTTPSPublicBaseURL is web auth UI
// brief item A0's own test: ARGUS_PUBLIC_BASE_URL (the origin the
// verification-email link is built against) must be https:// in
// production, validated the same way as ARGUS_AUTH_ALLOWED_ORIGINS.
func TestValidate_ProductionRejectsNonHTTPSPublicBaseURL(t *testing.T) {
	c := validConfig("staging")
	c.PublicBaseURL = "http://app.example.com"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with a non-https PublicBaseURL in production = nil, want an error")
	}
}

// TestValidate_ProductionRejectsDefaultOrEmptySignupCodeHMACKey is
// ADR-015 "Signup and onboarding"'s own test: the local-dev
// placeholder (or an empty value) must never reach production, since
// it is what keeps a database breach of signup_verifications from
// being brute-forced offline.
func TestValidate_ProductionRejectsDefaultOrEmptySignupCodeHMACKey(t *testing.T) {
	for _, key := range []string{"", insecureSignupCodeHMACKey} {
		c := validConfig("staging")
		c.Auth.SignupCodeHMACKey = key
		if err := c.Validate(); err == nil {
			t.Fatalf("Validate() with SignupCodeHMACKey=%q in production = nil, want an error", key)
		}
	}

	c := validConfig("staging")
	c.Auth.SignupCodeHMACKey = "a-real-production-secret-not-the-placeholder"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with a real SignupCodeHMACKey in production = %v, want nil", err)
	}
}
