package config

import (
	"testing"
)

// TestLoadDefaults verifies defaults load when the environment is empty.
func TestLoadDefaults(t *testing.T) {
	t.Setenv("ARGUS_HTTP_PORT", "")
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
