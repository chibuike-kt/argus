package main

import (
	"testing"

	"github.com/chibuike-kt/argus/internal/ingestion"
)

func TestBuildInstrumentParams_FiltersToConfiguredSymbols(t *testing.T) {
	all := []ingestion.ProviderInstrument{
		{Symbol: "BTC", AssetClass: "crypto_perp", PriceDecimalPlaces: 1, QtyDecimalPlaces: 5, TickSize: "0.1", QtyStepSize: "0.00001", SupportedIntervals: []string{"1m"}},
		{Symbol: "ETH", AssetClass: "crypto_perp", PriceDecimalPlaces: 2, QtyDecimalPlaces: 4, TickSize: "0.01", QtyStepSize: "0.0001", SupportedIntervals: []string{"1m"}},
		{Symbol: "DOGE", AssetClass: "crypto_perp", PriceDecimalPlaces: 6, QtyDecimalPlaces: 0, TickSize: "0.000001", QtyStepSize: "1", SupportedIntervals: []string{"1m"}},
	}

	toInsert, missing, err := buildInstrumentParams([]string{"BTC", "ETH"}, all)
	if err != nil {
		t.Fatalf("buildInstrumentParams: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}
	if len(toInsert) != 2 {
		t.Fatalf("got %d params, want 2 (DOGE must be excluded, it was not requested)", len(toInsert))
	}

	var symbols []string
	for _, p := range toInsert {
		symbols = append(symbols, p.NativeSymbol)
		if p.Provider != "hyperliquid" {
			t.Errorf("provider = %q, want hyperliquid", p.Provider)
		}
	}
	if symbols[0] != "BTC" || symbols[1] != "ETH" {
		t.Errorf("symbols = %v, want [BTC ETH]", symbols)
	}
}

func TestBuildInstrumentParams_ReportsConfiguredButNotFound(t *testing.T) {
	all := []ingestion.ProviderInstrument{
		{Symbol: "BTC", AssetClass: "crypto_perp", PriceDecimalPlaces: 1, QtyDecimalPlaces: 5, TickSize: "0.1", QtyStepSize: "0.00001", SupportedIntervals: []string{"1m"}},
	}
	toInsert, missing, err := buildInstrumentParams([]string{"BTC", "NOTREAL"}, all)
	if err != nil {
		t.Fatalf("buildInstrumentParams: %v", err)
	}
	if len(toInsert) != 1 {
		t.Fatalf("got %d params, want 1", len(toInsert))
	}
	if len(missing) != 1 || missing[0] != "NOTREAL" {
		t.Errorf("missing = %v, want [NOTREAL]", missing)
	}
}

func TestBuildInstrumentParams_ScaleAndTickConversion(t *testing.T) {
	all := []ingestion.ProviderInstrument{
		{Symbol: "BTC", AssetClass: "crypto_perp", BaseAsset: "BTC", QuoteAsset: "USD", ContractType: "perpetual",
			PriceDecimalPlaces: 1, QtyDecimalPlaces: 5, TickSize: "0.1", QtyStepSize: "0.00001",
			SupportedIntervals: []string{"1m", "5m", "15m", "1h", "4h", "1d"}},
	}
	toInsert, _, err := buildInstrumentParams([]string{"BTC"}, all)
	if err != nil {
		t.Fatalf("buildInstrumentParams: %v", err)
	}
	p := toInsert[0]
	if *p.PriceScale != 1 {
		t.Errorf("PriceScale = %d, want 1", *p.PriceScale)
	}
	if *p.QtyScale != 5 {
		t.Errorf("QtyScale = %d, want 5", *p.QtyScale)
	}
	if p.TickSize != 1 { // "0.1" at scale 1 == 1 scaled unit
		t.Errorf("TickSize = %d, want 1", p.TickSize)
	}
	if p.QtyStepSize != 1 { // "0.00001" at scale 5 == 1 scaled unit
		t.Errorf("QtyStepSize = %d, want 1", p.QtyStepSize)
	}
	if *p.BaseAsset != "BTC" || *p.QuoteAsset != "USD" || *p.ContractType != "perpetual" {
		t.Errorf("unexpected base/quote/contract: %+v", p)
	}
	if len(p.SupportedIntervals) != 6 {
		t.Errorf("SupportedIntervals = %v, want all six", p.SupportedIntervals)
	}
}

func TestBuildInstrumentParams_RejectsAPriceThatDoesNotFitItsOwnScale(t *testing.T) {
	// Defensive: a provider bug that reports a TickSize string
	// inconsistent with its own PriceDecimalPlaces must fail loudly at
	// sync time, not register a scale/tick mismatch that would later
	// make the Normalizer reject every real candle for this instrument.
	all := []ingestion.ProviderInstrument{
		{Symbol: "BTC", PriceDecimalPlaces: 1, QtyDecimalPlaces: 0, TickSize: "0.123456", QtyStepSize: "1"},
	}
	_, _, err := buildInstrumentParams([]string{"BTC"}, all)
	if err == nil {
		t.Fatal("expected an error for a tick size with more decimals than PriceDecimalPlaces allows")
	}
}
