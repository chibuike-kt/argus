package identity

import (
	"errors"
	"strings"
	"unicode"
)

// ValidateDisplayName's own rejection reasons, one per policy rule
// (ADR-015 "Signup and onboarding": "one field, 1 to 60 characters
// after trimming, with no control characters").
var (
	ErrDisplayNameEmpty       = errors.New("identity: display name must not be empty")
	ErrDisplayNameTooLong     = errors.New("identity: display name is too long")
	ErrDisplayNameControlRune = errors.New("identity: display name must not contain control characters")
)

// ValidateDisplayName trims raw and enforces ADR-015's own rule,
// returning the trimmed name ready to store. maxLen is
// config.AuthPolicy.DisplayNameMaxLength -- the one number this
// package does not hardcode a default for itself.
func ValidateDisplayName(raw string, maxLen int) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", ErrDisplayNameEmpty
	}
	if len([]rune(name)) > maxLen {
		return "", ErrDisplayNameTooLong
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrDisplayNameControlRune
		}
	}
	return name, nil
}
