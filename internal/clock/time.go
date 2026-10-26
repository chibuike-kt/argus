package clock

import (
	"encoding/json"
	"fmt"
	"time"
)

// These four types distinguish the moments guide section 16 requires ARGUS
// to keep separate. They share the same underlying representation but are
// distinct Go types so the compiler rejects passing one where another is
// required — the specific mistake that causes look-ahead bias (ADR-019):
// using a later clock's reading as if it were the time evidence was
// actually available.
//
// All four are always stored, compared and serialized in UTC. Use the New*
// constructors, which normalize the input; do not convert a bare time.Time
// with a type conversion.

// EventTime is when something happened at the source, e.g. the close time
// of a candle as reported by the exchange.
type EventTime time.Time

// IngestionTime is when ARGUS received and stored an event.
type IngestionTime time.Time

// ProcessingTime is when a worker acted on an event.
type ProcessingTime time.Time

// EvaluationTime is the instant a strategy evaluation is anchored to.
type EvaluationTime time.Time

// NewEventTime normalizes t to UTC and returns it as an EventTime.
func NewEventTime(t time.Time) EventTime { return EventTime(t.UTC()) }

// NewIngestionTime normalizes t to UTC and returns it as an IngestionTime.
func NewIngestionTime(t time.Time) IngestionTime { return IngestionTime(t.UTC()) }

// NewProcessingTime normalizes t to UTC and returns it as a ProcessingTime.
func NewProcessingTime(t time.Time) ProcessingTime { return ProcessingTime(t.UTC()) }

// NewEvaluationTime normalizes t to UTC and returns it as an EvaluationTime.
func NewEvaluationTime(t time.Time) EvaluationTime { return EvaluationTime(t.UTC()) }

// Time returns the underlying UTC time.Time.
func (t EventTime) Time() time.Time { return time.Time(t) }

// Time returns the underlying UTC time.Time.
func (t IngestionTime) Time() time.Time { return time.Time(t) }

// Time returns the underlying UTC time.Time.
func (t ProcessingTime) Time() time.Time { return time.Time(t) }

// Time returns the underlying UTC time.Time.
func (t EvaluationTime) Time() time.Time { return time.Time(t) }

// String renders the time as RFC3339Nano in UTC.
func (t EventTime) String() string { return formatTime(t.Time()) }

// String renders the time as RFC3339Nano in UTC.
func (t IngestionTime) String() string { return formatTime(t.Time()) }

// String renders the time as RFC3339Nano in UTC.
func (t ProcessingTime) String() string { return formatTime(t.Time()) }

// String renders the time as RFC3339Nano in UTC.
func (t EvaluationTime) String() string { return formatTime(t.Time()) }

// MarshalJSON renders the time as an RFC3339Nano string in UTC.
func (t EventTime) MarshalJSON() ([]byte, error) { return marshalTime(t.Time()) }

// MarshalJSON renders the time as an RFC3339Nano string in UTC.
func (t IngestionTime) MarshalJSON() ([]byte, error) { return marshalTime(t.Time()) }

// MarshalJSON renders the time as an RFC3339Nano string in UTC.
func (t ProcessingTime) MarshalJSON() ([]byte, error) { return marshalTime(t.Time()) }

// MarshalJSON renders the time as an RFC3339Nano string in UTC.
func (t EvaluationTime) MarshalJSON() ([]byte, error) { return marshalTime(t.Time()) }

// UnmarshalJSON requires an RFC3339 string and normalizes it to UTC.
func (t *EventTime) UnmarshalJSON(data []byte) error {
	v, err := unmarshalTime(data, "EventTime")
	if err != nil {
		return err
	}
	*t = EventTime(v)
	return nil
}

// UnmarshalJSON requires an RFC3339 string and normalizes it to UTC.
func (t *IngestionTime) UnmarshalJSON(data []byte) error {
	v, err := unmarshalTime(data, "IngestionTime")
	if err != nil {
		return err
	}
	*t = IngestionTime(v)
	return nil
}

// UnmarshalJSON requires an RFC3339 string and normalizes it to UTC.
func (t *ProcessingTime) UnmarshalJSON(data []byte) error {
	v, err := unmarshalTime(data, "ProcessingTime")
	if err != nil {
		return err
	}
	*t = ProcessingTime(v)
	return nil
}

// UnmarshalJSON requires an RFC3339 string and normalizes it to UTC.
func (t *EvaluationTime) UnmarshalJSON(data []byte) error {
	v, err := unmarshalTime(data, "EvaluationTime")
	if err != nil {
		return err
	}
	*t = EvaluationTime(v)
	return nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func marshalTime(t time.Time) ([]byte, error) { return json.Marshal(formatTime(t)) }

func unmarshalTime(data []byte, typeName string) (time.Time, error) {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return time.Time{}, fmt.Errorf("decode %s: %w", typeName, err)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode %s: parse %q: %w", typeName, s, err)
	}
	return t.UTC(), nil
}
