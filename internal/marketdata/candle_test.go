package marketdata

import (
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
)

func validTick() Tick {
	return Tick{
		InstrumentID: 1,
		OpenTime:     clock.NewEventTime(time.Date(2026, time.October, 26, 18, 0, 0, 0, time.UTC)),
		Open:         100, High: 110, Low: 95, Close: 105,
		Volume:     10,
		Provider:   "binance",
		IngestedAt: clock.NewIngestionTime(time.Now()),
	}
}

func TestTick_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(t *Tick)
		wantErr error
	}{
		{"valid tick passes", func(*Tick) {}, nil},
		{"missing instrument id", func(tk *Tick) { tk.InstrumentID = 0 }, ErrInstrumentIDRequired},
		{"missing provider", func(tk *Tick) { tk.Provider = "" }, ErrProviderRequired},
		{
			"open_time not minute-aligned",
			func(tk *Tick) { tk.OpenTime = clock.NewEventTime(tk.OpenTime.Time().Add(30 * time.Second)) },
			ErrNotMinuteAligned,
		},
		{"negative open", func(tk *Tick) { tk.Open = -1 }, ErrOHLCNegative},
		{"negative volume", func(tk *Tick) { tk.Volume = -1 }, ErrVolumeNegative},
		{"high below low", func(tk *Tick) { tk.High = 50 }, ErrOHLCInconsistent},
		{"low above open", func(tk *Tick) { tk.Low = 200 }, ErrOHLCInconsistent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tk := validTick()
			tt.mutate(&tk)
			err := tk.validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTick_CloseTime_IsAlwaysOpenTimePlusOneMinute(t *testing.T) {
	tk := validTick()
	want := tk.OpenTime.Time().Add(time.Minute)
	if got := tk.closeTime(); !got.Equal(want) {
		t.Fatalf("closeTime() = %s, want %s", got, want)
	}
}

func validCorrection() Correction {
	return Correction{
		Open: 100, High: 110, Low: 95, Close: 105,
		Volume:     10,
		Provider:   "binance",
		IngestedAt: clock.NewIngestionTime(time.Now()),
	}
}

func TestCorrection_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Correction)
		wantErr error
	}{
		{"valid correction passes", func(*Correction) {}, nil},
		{"missing provider", func(c *Correction) { c.Provider = "" }, ErrProviderRequired},
		{"negative close", func(c *Correction) { c.Close = -1 }, ErrOHLCNegative},
		{"negative volume", func(c *Correction) { c.Volume = -1 }, ErrVolumeNegative},
		{"high below close", func(c *Correction) { c.High = quant.Price(1) }, ErrOHLCInconsistent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCorrection()
			tt.mutate(&c)
			err := c.validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
