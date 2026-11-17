package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chibuike-kt/argus/internal/instruments"
)

// ingestProviderEnv is the variable that selects the worker's ingest
// provider. config.Load fills in "fixture" when it is unset, so the
// worker reads whether it was actually set separately (os.LookupEnv in
// main) -- after Load, "unset" and "set to fixture" look identical.
const ingestProviderEnv = "ARGUS_INGEST_PROVIDER"

// fixtureProvider is the dev fixture's provider name on instrument rows.
const fixtureProvider = "fixture"

// checkIngestProvider is the startup guard against a silent provider
// fallback: a worker that quietly runs the fixture default while real-
// provider instruments are enabled looks healthy ("tick complete")
// while ingesting nothing for them.
//
//   - ARGUS_INGEST_PROVIDER unset and any enabled instrument has a
//     non-fixture provider: refuse to start (err).
//   - set, but no enabled instrument of a non-fixture provider will be
//     fed by it (e.g. explicitly "fixture" with Hyperliquid instruments
//     enabled): start, but return a loud warning naming them.
//
// enabled is every enabled instrument.
func checkIngestProvider(envSet bool, provider string, enabled []instruments.Instrument) (warning string, err error) {
	unfed := map[string][]string{}
	for _, inst := range enabled {
		if inst.Provider == fixtureProvider || inst.Provider == provider {
			continue
		}
		unfed[inst.Provider] = append(unfed[inst.Provider], inst.NativeSymbol)
	}
	if len(unfed) == 0 {
		return "", nil
	}
	providers := make([]string, 0, len(unfed))
	for p, syms := range unfed {
		sort.Strings(syms)
		providers = append(providers, fmt.Sprintf("%s (%s)", p, strings.Join(syms, ", ")))
	}
	sort.Strings(providers)
	list := strings.Join(providers, "; ")

	if !envSet {
		return "", fmt.Errorf("%s is not set, so the worker would fall back to %q, but enabled instruments use another provider: %s -- set %s explicitly (e.g. hyperliquid) or disable those instruments",
			ingestProviderEnv, provider, list, ingestProviderEnv)
	}
	return fmt.Sprintf("%s=%s does not ingest these enabled instruments; they will receive no new candles: %s", ingestProviderEnv, provider, list), nil
}
