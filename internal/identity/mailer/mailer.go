// Package mailer defines the outbound-email boundary (ADR-015 addendum
// row 12). No real provider is chosen yet (out of scope for this
// brief) -- LogMailer is the only implementation, for local
// development, and the one place in this codebase allowed to write a
// verification/reset link to the log (see its own doc comment for why
// this is a deliberate, narrow exception to CLAUDE.md rule 3, not an
// oversight).
package mailer

import "context"

// Message is everything a Mailer needs to send one email. Body is
// plain text; this package does not render templates -- callers build
// the final text (including any link) before calling Send.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer sends email. A real implementation (SMTP, or a provider's
// API) is not part of this brief; see LogMailer's own doc comment for
// what such an implementation would additionally need.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}
