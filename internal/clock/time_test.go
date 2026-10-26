package clock

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimeTypes_NormalizeToUTC(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	src := time.Date(2026, 3, 15, 9, 30, 0, 0, loc)
	want := src.UTC()

	if got := NewEventTime(src).Time(); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("EventTime = %v, want %v in UTC", got, want)
	}
	if got := NewIngestionTime(src).Time(); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("IngestionTime = %v, want %v in UTC", got, want)
	}
	if got := NewProcessingTime(src).Time(); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("ProcessingTime = %v, want %v in UTC", got, want)
	}
	if got := NewEvaluationTime(src).Time(); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("EvaluationTime = %v, want %v in UTC", got, want)
	}
}

func TestTimeTypes_JSONRoundTrip(t *testing.T) {
	src := time.Date(2026, 10, 26, 18, 4, 5, 123000000, time.UTC)

	t.Run("EventTime", func(t *testing.T) {
		original := NewEventTime(src)
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var decoded EventTime
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !decoded.Time().Equal(original.Time()) {
			t.Fatalf("round trip = %v, want %v", decoded.Time(), original.Time())
		}
	})

	t.Run("IngestionTime", func(t *testing.T) {
		original := NewIngestionTime(src)
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var decoded IngestionTime
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !decoded.Time().Equal(original.Time()) {
			t.Fatalf("round trip = %v, want %v", decoded.Time(), original.Time())
		}
	})

	t.Run("ProcessingTime", func(t *testing.T) {
		original := NewProcessingTime(src)
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var decoded ProcessingTime
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !decoded.Time().Equal(original.Time()) {
			t.Fatalf("round trip = %v, want %v", decoded.Time(), original.Time())
		}
	})

	t.Run("EvaluationTime", func(t *testing.T) {
		original := NewEvaluationTime(src)
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var decoded EvaluationTime
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !decoded.Time().Equal(original.Time()) {
			t.Fatalf("round trip = %v, want %v", decoded.Time(), original.Time())
		}
	})
}

func TestEventTime_UnmarshalJSON_RejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"not a string", `1761500645`},
		{"empty string", `""`},
		{"not RFC3339", `"2026-10-26"`},
		{"garbage", `"not-a-time"`},
		{"null", `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got EventTime
			if err := json.Unmarshal([]byte(tt.json), &got); err == nil {
				t.Fatalf("Unmarshal(%s) = nil error, want error", tt.json)
			}
		})
	}
}

func TestEventTime_String(t *testing.T) {
	src := time.Date(2026, 10, 26, 18, 4, 5, 0, time.UTC)
	got := NewEventTime(src).String()
	want := "2026-10-26T18:04:05Z"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestTimeTypes_AreDistinctTypes(_ *testing.T) {
	// This test exists to document the invariant at compile time: each
	// constructor returns its own named type, so the following would not
	// compile if the types were interchangeable:
	//   var _ EventTime = NewIngestionTime(time.Now())
	// The explicit types below are the point of the test, not redundancy:
	// they pin each constructor's exact return type against an accidental
	// signature change that would otherwise only surface where it's called.
	src := time.Now()
	var _ EventTime = NewEventTime(src)           //nolint:staticcheck // intentional: pins the return type
	var _ IngestionTime = NewIngestionTime(src)   //nolint:staticcheck // intentional: pins the return type
	var _ ProcessingTime = NewProcessingTime(src) //nolint:staticcheck // intentional: pins the return type
	var _ EvaluationTime = NewEvaluationTime(src) //nolint:staticcheck // intentional: pins the return type
}
