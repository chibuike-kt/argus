package password

import (
	_ "embed"
	"errors"
	"strings"
)

// commonPasswordsRaw is SecLists' own "10k-most-common.txt"
// (danielmiessler/SecLists, Passwords/Common-Credentials), a widely
// used, publicly published list -- the bundled list ADR-015 addendum
// row 2 asks for, not something generated for this project.
//
//go:embed common_passwords.txt
var commonPasswordsRaw string

var commonPasswords = buildCommonPasswordSet(commonPasswordsRaw)

func buildCommonPasswordSet(raw string) map[string]struct{} {
	lines := strings.Split(raw, "\n")
	set := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Case-folded: a blocklist that only caught "password" and not
		// "Password" or "PASSWORD" would miss the overwhelming majority
		// of real attempts to use a common password (NIST SP 800-63B's
		// own guidance on this kind of check).
		set[strings.ToLower(line)] = struct{}{}
	}
	return set
}

// MinLength/MaxLength are ADR-015 addendum row 2's own password-length
// policy: 12 to 128 characters, no composition rules, no rotation --
// there is no rotation check to write, since none exists.
const (
	MinLength = 12
	MaxLength = 128
)

// ValidatePolicy's own rejection reasons, one per policy rule.
var (
	ErrTooShort      = errors.New("password: must be at least 12 characters")
	ErrTooLong       = errors.New("password: must be at most 128 characters")
	ErrCommon        = errors.New("password: is one of the 10,000 most common passwords")
	ErrContainsEmail = errors.New("password: must not contain your email address")
)

// ValidatePolicy enforces ADR-015 addendum row 2's password policy.
// emailLocalPart is the part of the account's own email before the
// "@" -- a password containing it (case-insensitively) is rejected.
// Online breach screening (e.g. a Have I Been Pwned lookup) is
// explicitly deferred by the addendum; this is the bundled-list check
// only.
func ValidatePolicy(pw, emailLocalPart string) error {
	n := len([]rune(pw)) // characters, not bytes: length as the person
	// who typed it would count it, not however many UTF-8 bytes that
	// happens to take.
	if n < MinLength {
		return ErrTooShort
	}
	if n > MaxLength {
		return ErrTooLong
	}
	if _, common := commonPasswords[strings.ToLower(pw)]; common {
		return ErrCommon
	}
	if emailLocalPart != "" && strings.Contains(strings.ToLower(pw), strings.ToLower(emailLocalPart)) {
		return ErrContainsEmail
	}
	return nil
}
