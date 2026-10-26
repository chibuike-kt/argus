package quant

import (
	"encoding/json"
	"testing"
)

func TestAmount_PriceQtyRoundTrip(t *testing.T) {
	p, err := ParsePrice("64250.50", 2)
	if err != nil {
		t.Fatalf("ParsePrice: %v", err)
	}
	amt := PriceAmount(p, 2)
	if amt.Value != "64250.50" || amt.Scale != 2 {
		t.Fatalf("PriceAmount = %+v, want value=64250.50 scale=2", amt)
	}
	got, err := amt.Price()
	if err != nil {
		t.Fatalf("Amount.Price: %v", err)
	}
	if got != p {
		t.Fatalf("Amount.Price round trip = %d, want %d", got, p)
	}
}

func TestAmount_JSONShape(t *testing.T) {
	amt := Amount{Value: "64250.50", Scale: 2}
	data, err := json.Marshal(amt)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"value":"64250.50","scale":2}`
	if string(data) != want {
		t.Fatalf("Marshal(Amount) = %s, want %s", data, want)
	}
}

func TestAmount_UnmarshalJSON_RejectsBareNumber(t *testing.T) {
	// The whole point of the wire contract: a JSON number for "value" must
	// be rejected, never silently coerced through a float64.
	var a Amount
	err := json.Unmarshal([]byte(`{"value": 64250.50, "scale": 2}`), &a)
	if err == nil {
		t.Fatal("Unmarshal accepted a bare JSON number for value, want error")
	}
}

func TestAmount_UnmarshalJSON_RejectsInvalidScale(t *testing.T) {
	var a Amount
	err := json.Unmarshal([]byte(`{"value": "1.5", "scale": 999}`), &a)
	if err == nil {
		t.Fatal("Unmarshal accepted an out-of-range scale, want error")
	}
}

func TestAmount_UnmarshalJSON_AcceptsWellFormed(t *testing.T) {
	var a Amount
	if err := json.Unmarshal([]byte(`{"value": "1.2500", "scale": 4}`), &a); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if a.Value != "1.2500" || a.Scale != 4 {
		t.Fatalf("Unmarshal = %+v, want value=1.2500 scale=4", a)
	}
}

func TestQtyAmount(t *testing.T) {
	q, err := ParseQty("10.25", 2)
	if err != nil {
		t.Fatalf("ParseQty: %v", err)
	}
	amt := QtyAmount(q, 2)
	got, err := amt.Qty()
	if err != nil {
		t.Fatalf("Amount.Qty: %v", err)
	}
	if got != q {
		t.Fatalf("Amount.Qty round trip = %d, want %d", got, q)
	}
}
