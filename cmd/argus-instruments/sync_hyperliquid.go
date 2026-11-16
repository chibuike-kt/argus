package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/hyperliquid"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/platform/postgres"
	"github.com/chibuike-kt/argus/internal/quant"
)

// runSyncHyperliquid creates (disabled) instruments for every symbol in
// cfg.Hyperliquid.Symbols, from Hyperliquid's own public meta response.
// Idempotent: a symbol already registered under provider "hyperliquid"
// is reported, not re-inserted or updated.
//
// Refuses to run in a production environment (cfg.IsProduction()):
// deciding to show real data to the public is ADR-007's own open
// decision, not something this command -- or any command -- gets to
// make by running in the wrong place.
func runSyncHyperliquid(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.IsProduction() {
		return fmt.Errorf("refusing to run sync-hyperliquid against ARGUS_ENV=%q: enabling real market data for the public is ADR-007's own open decision, not this command's to make", cfg.Env)
	}
	if len(cfg.Hyperliquid.Symbols) == 0 {
		return errors.New("ARGUS_HYPERLIQUID_SYMBOLS is empty -- nothing to sync")
	}

	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	logger := slog.Default()
	provider := hyperliquid.NewProvider(clock.Real{}, logger)
	all, err := provider.Instruments(ctx)
	if err != nil {
		return fmt.Errorf("fetch Hyperliquid instruments: %w", err)
	}

	toInsert, missing, err := buildInstrumentParams(cfg.Hyperliquid.Symbols, all)
	if err != nil {
		return err
	}

	store := instruments.NewStore(db)
	var created, alreadyExisted []string
	for _, p := range toInsert {
		_, err := store.Insert(ctx, p)
		switch {
		case err == nil:
			created = append(created, p.NativeSymbol)
		case errors.Is(err, instruments.ErrAlreadyExists):
			alreadyExisted = append(alreadyExisted, p.NativeSymbol)
		default:
			return fmt.Errorf("sync-hyperliquid: insert %s: %w", p.NativeSymbol, err)
		}
	}

	report(created, alreadyExisted, missing)
	return nil
}

// buildInstrumentParams filters all down to the configured symbols and
// converts each into instruments.NewInstrumentParams -- the pure,
// network- and database-free part of sync-hyperliquid, kept separate
// from runSyncHyperliquid specifically so it has its own unit tests
// (symbol filtering, scale/tick/step conversion, the "configured but
// not found" case) without needing a real Hyperliquid connection or
// database.
func buildInstrumentParams(symbols []string, all []ingestion.ProviderInstrument) (toInsert []instruments.NewInstrumentParams, missing []string, err error) {
	wanted := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		wanted[s] = true
	}

	for _, pi := range all {
		if !wanted[pi.Symbol] {
			continue
		}
		delete(wanted, pi.Symbol)

		priceScale := quant.Scale(pi.PriceDecimalPlaces)
		qtyScale := quant.Scale(pi.QtyDecimalPlaces)
		tick, err := quant.ParsePrice(pi.TickSize, priceScale)
		if err != nil {
			return nil, nil, fmt.Errorf("sync-hyperliquid: %s: parse tick size %q at scale %d: %w", pi.Symbol, pi.TickSize, priceScale, err)
		}
		step, err := quant.ParseQty(pi.QtyStepSize, qtyScale)
		if err != nil {
			return nil, nil, fmt.Errorf("sync-hyperliquid: %s: parse qty step %q at scale %d: %w", pi.Symbol, pi.QtyStepSize, qtyScale, err)
		}

		toInsert = append(toInsert, instruments.NewInstrumentParams{
			Provider:           hyperliquid.ProviderName,
			NativeSymbol:       pi.Symbol,
			AssetClass:         pi.AssetClass,
			BaseAsset:          &pi.BaseAsset,
			QuoteAsset:         &pi.QuoteAsset,
			ContractType:       &pi.ContractType,
			PriceScale:         &priceScale,
			QtyScale:           &qtyScale,
			TickSize:           tick,
			QtyStepSize:        step,
			SupportedIntervals: pi.SupportedIntervals,
		})
	}
	for s := range wanted {
		missing = append(missing, s)
	}
	return toInsert, missing, nil
}

func report(created, alreadyExisted, missing []string) {
	fmt.Println("argus-instruments sync-hyperliquid:")
	if len(created) > 0 {
		fmt.Printf("  created (disabled): %v\n", created)
	}
	if len(alreadyExisted) > 0 {
		fmt.Printf("  already registered, unchanged: %v\n", alreadyExisted)
	}
	if len(missing) > 0 {
		fmt.Printf("  WARNING: configured in ARGUS_HYPERLIQUID_SYMBOLS but not found in Hyperliquid's own meta response: %v\n", missing)
	}
	if len(created) == 0 && len(alreadyExisted) == 0 {
		fmt.Println("  nothing to do")
		return
	}
	fmt.Println()
	fmt.Println("Every instrument above was created disabled. There is no command-line flag or code path in this repository that can enable one --")
	fmt.Println("enabling real Hyperliquid data for the public is ADR-007's own open decision. To enable an instrument by hand, run exactly this SQL")
	fmt.Println("(one statement per symbol, substituting the native_symbol):")
	fmt.Println()
	for _, s := range created {
		fmt.Printf("  UPDATE instruments SET enabled = TRUE WHERE provider = 'hyperliquid' AND native_symbol = '%s';\n", s)
	}
	for _, s := range alreadyExisted {
		fmt.Printf("  UPDATE instruments SET enabled = TRUE WHERE provider = 'hyperliquid' AND native_symbol = '%s';\n", s)
	}
}
