package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/tracking"
)

type fakeClearer struct {
	marked  map[string]tracking.Status
	cleared []string
}

func (f *fakeClearer) ClearNeedsAttention(_ context.Context, symbol string) (tracking.Status, error) {
	st, ok := f.marked[symbol]
	if !ok {
		return tracking.Status{}, tracking.ErrNoAttention
	}
	delete(f.marked, symbol)
	f.cleared = append(f.cleared, symbol)
	return st, nil
}

func TestClearAttention_ClearsAndLogsWhoAndWhy(t *testing.T) {
	f := &fakeClearer{marked: map[string]tracking.Status{
		"LINK": {InstrumentID: 7, State: tracking.NeedsAttention, Reason: "price precision changed", UpdatedAt: time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC)},
	}}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if err := clearAttention(context.Background(), f, "LINK", "local", "kingsley@desk", logger); err != nil {
		t.Fatal(err)
	}
	if len(f.cleared) != 1 || f.cleared[0] != "LINK" {
		t.Fatalf("cleared %v, want LINK", f.cleared)
	}
	out := buf.String()
	for _, want := range []string{"symbol=LINK", "run_by=kingsley@desk", "allowed_because=", "not production", `was="price precision changed"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q lacks %q", out, want)
		}
	}
}

func TestClearAttention_NothingMarkedIsAnErrorAndProductionIsRefused(t *testing.T) {
	f := &fakeClearer{marked: map[string]tracking.Status{}}
	err := clearAttention(context.Background(), f, "BTC", "local", "x@y", slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "not marked needs attention") {
		t.Fatalf("err = %v, want 'not marked needs attention'", err)
	}
	if err := clearAttentionAllowed(config.Config{Env: "production"}); err == nil {
		t.Fatal("clear-attention was allowed against production")
	}
	if err := clearAttentionAllowed(config.Config{Env: "local"}); err != nil {
		t.Fatalf("refused outside production: %v", err)
	}
}
