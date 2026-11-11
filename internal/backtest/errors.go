package backtest

import "errors"

// Sentinel errors. Use errors.Is.
var (
	// ErrSetupKeyCollision is returned when two different setups (by
	// their own database-assigned id) produce the same SetupKey. Run
	// refuses to proceed rather than silently merge them into one row.
	ErrSetupKeyCollision = errors.New("backtest: two setups produced the same report key")

	// ErrNonFixtureInstrument is returned by CheckFixtureOnly when the
	// database already holds a setup for an instrument whose own
	// provider is not FixtureProviderName.
	ErrNonFixtureInstrument = errors.New("backtest: database holds a setup for a non-fixture instrument")
)
