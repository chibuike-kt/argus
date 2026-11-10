package identity

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDisplayName_TrimsWhitespace(t *testing.T) {
	name, err := ValidateDisplayName("  Ada Lovelace  ", 60)
	if err != nil {
		t.Fatalf("ValidateDisplayName: %v", err)
	}
	if name != "Ada Lovelace" {
		t.Fatalf("name = %q, want trimmed \"Ada Lovelace\"", name)
	}
}

func TestValidateDisplayName_EmptyAfterTrim_Rejected(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		if _, err := ValidateDisplayName(raw, 60); !errors.Is(err, ErrDisplayNameEmpty) {
			t.Fatalf("ValidateDisplayName(%q) = %v, want ErrDisplayNameEmpty", raw, err)
		}
	}
}

func TestValidateDisplayName_Length(t *testing.T) {
	if _, err := ValidateDisplayName(strings.Repeat("a", 60), 60); err != nil {
		t.Fatalf("60 chars (the maximum): err = %v, want nil", err)
	}
	if _, err := ValidateDisplayName(strings.Repeat("a", 61), 60); !errors.Is(err, ErrDisplayNameTooLong) {
		t.Fatalf("61 chars: err = %v, want ErrDisplayNameTooLong", err)
	}
}

func TestValidateDisplayName_ControlCharacter_Rejected(t *testing.T) {
	if _, err := ValidateDisplayName("Ada\x00Lovelace", 60); !errors.Is(err, ErrDisplayNameControlRune) {
		t.Fatalf("name with a NUL byte: err = %v, want ErrDisplayNameControlRune", err)
	}
	if _, err := ValidateDisplayName("Ada\nLovelace", 60); !errors.Is(err, ErrDisplayNameControlRune) {
		t.Fatalf("name with a newline: err = %v, want ErrDisplayNameControlRune", err)
	}
}
