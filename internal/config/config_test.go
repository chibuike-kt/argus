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
		Auth: AuthPolicy{AllowedOrigins: []string{"https://app.example.com"}},
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
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with an http origin outside production = %v, want nil", err)
	}
}
