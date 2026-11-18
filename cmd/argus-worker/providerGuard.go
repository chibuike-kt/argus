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
// fallback: a worker that quietly runs the fixture default looks healthy
// ("tick complete") while ingesting nothing real.
//
//   - ARGUS_INGEST_PROVIDER unset: refuse to start (err), always -- the
//     provider must be chosen explicitly (Phase 2, D2: the product runs
//     Hyperliquid; the e2e stack sets "fixture" on purpose).
//   - set, but some enabled instruments use a provider it does not feed
//     (e.g. "fixture" with Hyperliquid instruments enabled): start, but
//     return a loud warning naming them.
//
// enabled is every enabled instrument.
func checkIngestProvider(envSet bool, provider string, enabled []instruments.Instrument) (warning string, err error) {
	if !envSet {
		return "", fmt.Errorf("%s is not set -- the worker never picks an ingest provider by default; set it explicitly (hyperliquid for the real stack, fixture only for the e2e stack)", ingestProviderEnv)
	}
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
	return fmt.Sprintf("%s=%s does not ingest these enabled instruments; they will receive no new candles: %s", ingestProviderEnv, provider, list), nil
}
