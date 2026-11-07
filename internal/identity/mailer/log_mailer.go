package mailer

import (
	"context"
	"errors"
	"log/slog"
)

// ErrProductionNotConfigured is what NewLogMailer returns when env
// says this is a production deployment -- LogMailer must never run
// there (ADR-015 addendum row 12: "the local logging mailer must not
// run in production"), and there is no real provider yet to use
// instead, so refusing to construct it at all is what makes "must fail
// startup" actually true: whatever calls this in cmd/argus-api/main.go
// has nothing to fall back to and must itself fail to start.
var ErrProductionNotConfigured = errors.New("mailer: no mail provider is configured; the log mailer must not run in production")

// LogMailer writes the email's subject and body -- including, for a
// verification or reset email, the link itself -- to the structured
// log at Info. This is the ONE place in this codebase allowed to do
// that: CLAUDE.md rule 3 and this brief's own rule 3 forbid logging a
// verification or reset token everywhere else, but LogMailer's entire
// purpose is to stand in for a real mail provider in local development,
// where writing the link to the log IS the delivery mechanism (there
// is nowhere else for it to go). NewLogMailer refuses to construct one
// at all when isProduction is true, which is what makes this safe: the
// exception is bounded to non-production environments only, checked
// once, here, not left to every call site to remember.
type LogMailer struct {
	logger *slog.Logger
}

// NewLogMailer returns ErrProductionNotConfigured when isProduction is
// true.
func NewLogMailer(logger *slog.Logger, isProduction bool) (*LogMailer, error) {
	if isProduction {
		return nil, ErrProductionNotConfigured
	}
	return &LogMailer{logger: logger}, nil
}

// Send writes msg to the structured log at Info; see LogMailer's own
// doc comment for why this is safe only outside production.
func (m *LogMailer) Send(_ context.Context, msg Message) error {
	m.logger.Info("mailer: sending email (log mailer -- local development only, never production)",
		"to", msg.To, "subject", msg.Subject, "body", msg.Body)
	return nil
}
