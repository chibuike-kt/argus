// Command argus-instruments is a small operator CLI for the instrument
// registry. Its only subcommand today, sync-hyperliquid, creates
// (disabled) instruments for the configured Hyperliquid symbol list --
// see docs/briefs/brief-hyperliquid-adapter.md. It never enables an
// instrument: instruments.Store.Insert is structurally incapable of
// writing enabled = TRUE (see that package's own doc comment), and
// this command does not touch the enabled column at all.
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "sync-hyperliquid":
		err = runSyncHyperliquid(context.Background())
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "argus-instruments: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "argus-instruments:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: argus-instruments <subcommand>

subcommands:
  sync-hyperliquid   create (disabled) instruments for ARGUS_HYPERLIQUID_SYMBOLS,
                      from Hyperliquid's own public meta response. Refuses to
                      run against a production environment. Never enables an
                      instrument -- prints the exact SQL to do that by hand.`)
}
