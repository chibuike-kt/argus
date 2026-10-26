package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLiveness checks the liveness endpoint returns 200 without dependencies.
func TestLiveness(t *testing.T) {
	h := &healthHandler{}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	h.live(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected json content type, got %q", ct)
	}
}

// TestReadinessNoDeps reports 200 with "not configured" when no datastores are wired.
func TestReadinessNoDeps(t *testing.T) {
	h := &healthHandler{}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	h.ready(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
}
