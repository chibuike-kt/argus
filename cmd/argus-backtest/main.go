// Command argus-backtest replays history through the real analysis
// engine, point in time, and reports how the setups it produced would
// have resolved (strategy spec section 16; docs/briefs/brief-outcome-engine.md).
//
// It takes its own --database-url flag rather than reading
// ARGUS_DATABASE_URL: this must point at a database set aside for the
// run (internal/backtest's own doc.go explains why), never the live
// dev/production database (CLAUDE.md rule 7, ADR-019) -- there is
// deliberately no default here that could make that mistake easy.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chibuike-kt/argus/internal/backtest"
	"github.com/chibuike-kt/argus/internal/platform/postgres"
)

const defaultAlgorithmVersion = "swing-structure-v1"

func main() {
	var (
		databaseURL      = flag.String("database-url", "", "Postgres connection string for the database to replay against (required; never the dev/production database)")
		instrumentsFlag  = flag.String("instruments", "", "comma-separated instrument ids to replay (required)")
		startFlag        = flag.String("start", "", "RFC3339 start of the candle open_time range to replay, inclusive (required)")
		endFlag          = flag.String("end", "", "RFC3339 end of the candle open_time range to replay, exclusive (required)")
		outputDir        = flag.String("output-dir", ".", "directory to write report.json and report.md into")
		algorithmVersion = flag.String("algorithm-version", defaultAlgorithmVersion, "analysis.Config.AlgorithmVersion to evaluate with")
	)
	flag.Parse()

	if err := run(*databaseURL, *instrumentsFlag, *startFlag, *endFlag, *outputDir, *algorithmVersion); err != nil {
		fmt.Fprintln(os.Stderr, "argus-backtest:", err)
		os.Exit(1)
	}
}

func run(databaseURL, instrumentsFlag, startFlag, endFlag, outputDir, algorithmVersion string) error {
	if databaseURL == "" {
		return fmt.Errorf("--database-url is required")
	}
	instrumentIDs, err := parseInstrumentIDs(instrumentsFlag)
	if err != nil {
		return fmt.Errorf("--instruments: %w", err)
	}
	start, err := time.Parse(time.RFC3339, startFlag)
	if err != nil {
		return fmt.Errorf("--start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, endFlag)
	if err != nil {
		return fmt.Errorf("--end: %w", err)
	}
	if !end.After(start) {
		return fmt.Errorf("--end must be after --start")
	}

	ctx := context.Background()
	db, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	report, err := backtest.Run(ctx, db, backtest.Params{
		InstrumentIDs:    instrumentIDs,
		Start:            start,
		End:              end,
		AlgorithmVersion: algorithmVersion,
	})
	if err != nil {
		return fmt.Errorf("run backtest: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report json: %w", err)
	}
	jsonPath := filepath.Join(outputDir, "report.json")
	if err := os.WriteFile(jsonPath, jsonBytes, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}

	mdPath := filepath.Join(outputDir, "report.md")
	if err := os.WriteFile(mdPath, []byte(report.RenderMarkdown()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", mdPath, err)
	}

	fmt.Printf("wrote %s and %s (%d setups evaluated)\n", jsonPath, mdPath, report.TotalRows)
	return nil
}

func parseInstrumentIDs(s string) ([]int64, error) {
	parts := strings.Split(s, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid instrument id %q: %w", p, err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one instrument id is required")
	}
	return ids, nil
}
