package marketdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

func TestContinuousCalendar_AlwaysOpen(t *testing.T) {
	var c Calendar = ContinuousCalendar{}
	status, err := c.Status(context.Background(), "crypto_perp", clock.NewEventTime(time.Now()))
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != MarketStatusOpen {
		t.Errorf("Status = %v, want MarketStatusOpen", status)
	}
}

func TestForexCalendar_AlwaysErrors(t *testing.T) {
	var c Calendar = ForexCalendar{}
	status, err := c.Status(context.Background(), "forex", clock.NewEventTime(time.Now()))
	if !errors.Is(err, ErrForexCalendarNotImplemented) {
		t.Fatalf("Status error = %v, want ErrForexCalendarNotImplemented", err)
	}
	if status != MarketStatusUnknown {
		t.Errorf("Status = %v, want MarketStatusUnknown alongside the error", status)
	}
}
