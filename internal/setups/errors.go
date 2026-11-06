package setups

import "errors"

// ErrSetupNotFound is returned when no setups row exists for the
// requested identity or id.
var ErrSetupNotFound = errors.New("setups: setup not found")
