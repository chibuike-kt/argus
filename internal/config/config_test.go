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
