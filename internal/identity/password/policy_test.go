package password

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePolicy_Length(t *testing.T) {
	if err := ValidatePolicy(strings.Repeat("a", 11), ""); !errors.Is(err, ErrTooShort) {
		t.Fatalf("11 chars: err = %v, want ErrTooShort", err)
	}
	if err := ValidatePolicy(strings.Repeat("a", 12), ""); err != nil {
		t.Fatalf("12 chars (the minimum): err = %v, want nil", err)
	}
	if err := ValidatePolicy(strings.Repeat("a", 128), ""); err != nil {
		t.Fatalf("128 chars (the maximum): err = %v, want nil", err)
	}
	if err := ValidatePolicy(strings.Repeat("a", 129), ""); !errors.Is(err, ErrTooLong) {
		t.Fatalf("129 chars: err = %v, want ErrTooLong", err)
	}
}

func TestValidatePolicy_CommonPassword(t *testing.T) {
	// "unbelievable" is on the bundled list and, unlike "password"
	// itself, is already >=12 characters, so this exercises the
	// common-list check specifically, not the length check.
	if err := ValidatePolicy("unbelievable", ""); !errors.Is(err, ErrCommon) {
		t.Fatalf("\"unbelievable\": err = %v, want ErrCommon", err)
	}
	// Case-folded: catches "Unbelievable" too, not only the exact-case
	// list entry.
	if err := ValidatePolicy("Unbelievable", ""); !errors.Is(err, ErrCommon) {
		t.Fatalf("\"Unbelievable\": err = %v, want ErrCommon", err)
	}
}

func TestValidatePolicy_ContainsEmailLocalPart(t *testing.T) {
	if err := ValidatePolicy("kingsleyIsMyOwnPassphrase1", "kingsley"); !errors.Is(err, ErrContainsEmail) {
		t.Fatalf("password containing the email local part: err = %v, want ErrContainsEmail", err)
	}
	// Case-insensitive, same reasoning as the common-password check.
	if err := ValidatePolicy("KINGSLEYsOwnPassphrase1", "kingsley"); !errors.Is(err, ErrContainsEmail) {
		t.Fatalf("different case: err = %v, want ErrContainsEmail", err)
	}
}

func TestValidatePolicy_AcceptsAReasonablePassword(t *testing.T) {
	if err := ValidatePolicy("a genuinely uncommon passphrase 42", "unrelated-user"); err != nil {
		t.Fatalf("got %v, want nil for a long, non-common, email-unrelated password", err)
	}
}

func TestValidatePolicy_NoCompositionRules(t *testing.T) {
	// ADR-015 addendum row 2: "no composition rules" -- all lowercase,
	// no digits, no symbols, still accepted purely on length/common-
	// list/email grounds.
	if err := ValidatePolicy("alllowercasenodigitsatall", "someone-else"); err != nil {
		t.Fatalf("got %v, want nil -- no composition rule should reject this", err)
	}
}
