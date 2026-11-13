// Package hyperliquid implements ingestion.Provider against Hyperliquid's
// public info API only (POST https://api.hyperliquid.xyz/info and
// wss://api.hyperliquid.xyz/ws). ADR-007 (provider selection and
// commercial data rights) is still open; this is a private analytics
// source for the owner's own use, never a public feed -- see
// docs/briefs/brief-hyperliquid-adapter.md for the full brief this
// implements, including the owner's own approvals and the items still
// flagged open there.
//
// Hard limits this package holds itself to, because nothing upstream of
// it enforces them:
//   - No code path here can enable an instrument. Nothing in this
//     package writes to the instruments table at all (that is
//     cmd/argus-instruments' job, via instruments.Store.Insert, which
//     itself cannot write enabled = TRUE -- see instruments' own package
//     doc). Instruments is read-only from this provider's own
//     perspective.
//   - Public endpoints only. There is no field anywhere in this package
//     for an API key, a wallet address, or a private key, and no request
//     this package builds is signed. Every exported type and function
//     only ever needs a coin symbol, an interval, and a time range.
//   - No VPN, proxy, or alternate DNS resolver. This package dials
//     api.hyperliquid.xyz directly, over whatever network path the
//     process already has.
//
// Price and volume semantics (verify-first findings, recorded here so
// nothing downstream has to guess):
//   - Candle prices (`candle`/`candleSnapshot`, o/h/l/c) are last-trade
//     prices. They are not Hyperliquid's own mark price or oracle price,
//     which live on separate, differently-named endpoints this package
//     does not touch.
//   - Volume (`v`) is base-asset volume (e.g. BTC, not USD).
//   - Hyperliquid's own candle schema -- both the `candle` WebSocket
//     message and the `candleSnapshot` REST response -- carries no
//     closed/final flag at all. See the closed-candle doc comment on
//     StreamCandles and HistoricalCandles for how this package infers
//     finality without one, and without ever consulting the clock to do
//     it (owner's own rule).
//   - Hyperliquid's own price-precision rule is not a fixed tick the way
//     Binance's PRICE_FILTER.tickSize is: a price may have at most 5
//     significant figures and at most (6 - szDecimals) decimal places
//     (MAX_DECIMALS=6 for perps), except that integer prices are always
//     valid regardless of significant figures
//     (hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/tick-and-lot-size,
//     read directly, quoted in the brief). price_scale is derived as
//     max(0, 6 - szDecimals) -- an upper bound on precision, not a
//     guarantee that every price actually uses that many decimals.
//   - Hyperliquid's own terms of use could not be read as text (the page
//     is a JavaScript-rendered single-page app); nothing found elsewhere
//     contradicts private research use of the public info API, but
//     nothing confirms it either. Every dataset label this package's
//     data ever reaches must say so -- see cmd/argus-instruments'
//     sync-hyperliquid command and internal/backtest's own dataset
//     label.
package hyperliquid

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

// ProviderName is the provider string stored on every instrument and
// candle this package produces.
const ProviderName = "hyperliquid"

// BaseURL and WSURL are Hyperliquid's own public info API hosts, read
// directly from its official docs during this brief's verify-first
// phase -- never behind a VPN, proxy, or alternate resolver, and never
// hardcoded anywhere else in this package.
const (
	BaseURL = "https://api.hyperliquid.xyz"
	WSURL   = "wss://api.hyperliquid.xyz/ws"
)

// Provider implements ingestion.Provider against Hyperliquid's public
// info API. Every field is a seam a test replaces: httpClient and wsDial
// so tests run against a local fake server rather than the real
// exchange, clk so no behavior in this package depends on wall-clock
// state (CLAUDE.md's own "inject the clock" rule), and logger for the
// structured rejection/backoff logging the hostile-condition tests
// check for.
type Provider struct {
	baseURL    string
	wsURL      string
	httpClient *http.Client
	wsDial     dialFunc
	clk        clock.Clock
	sleep      sleepFunc
	logger     *slog.Logger
	limiter    *restLimiter

	restBudget int
	restWindow time.Duration
}

// Option configures a Provider beyond NewProvider's required
// parameters. Every Option here exists for a real caller (a test fake,
// or a non-default pacing choice), not speculatively.
type Option func(*Provider)

// WithBaseURL overrides the REST base URL (BaseURL by default) -- tests
// point this at an httptest.Server.
func WithBaseURL(url string) Option {
	return func(p *Provider) { p.baseURL = url }
}

// WithWSURL overrides the WebSocket URL (WSURL by default) -- tests
// point this at a local fake WebSocket server.
func WithWSURL(url string) Option {
	return func(p *Provider) { p.wsURL = url }
}

// WithHTTPClient overrides the *http.Client used for every REST call.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// WithDialFunc overrides how StreamCandles/StreamQuotes dial the
// WebSocket -- tests substitute a dialer that skips TLS/the real
// network entirely.
func WithDialFunc(d dialFunc) Option {
	return func(p *Provider) { p.wsDial = d }
}

// WithRESTBudget overrides the REST weight-per-minute budget (1200 by
// default, Hyperliquid's own documented per-IP limit) -- tests use a
// small budget to exercise pacing/backoff without waiting a real
// minute. The actual limiter is constructed in NewProvider, after every
// Option has run, so this does not depend on option ordering relative
// to WithSleepFunc.
func WithRESTBudget(weightPerMinute int, window time.Duration) Option {
	return func(p *Provider) {
		p.restBudget = weightPerMinute
		p.restWindow = window
	}
}

// WithSleepFunc overrides the seam every backoff/pacing wait in this
// package goes through (realSleep by default). Tests substitute a
// function that advances a fake clock instead of actually waiting --
// see sleepFunc's own doc comment for why time.After directly cannot
// be driven by a fake clock the way this can.
func WithSleepFunc(s sleepFunc) Option {
	return func(p *Provider) { p.sleep = s }
}

// NewProvider wires a Provider against the real Hyperliquid endpoints.
// clk must not be nil (every time-dependent decision in this package --
// REST pacing, the rate-limit backoff clock -- reads it, never
// time.Now() directly). logger defaults to slog.Default() if nil.
func NewProvider(clk clock.Clock, logger *slog.Logger, opts ...Option) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Provider{
		baseURL:    BaseURL,
		wsURL:      WSURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		wsDial:     dialReal,
		clk:        clk,
		sleep:      realSleep,
		logger:     logger,
		// Hyperliquid's own documented per-IP budget
		// (hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/rate-limits-and-user-limits,
		// read directly): 1200 weight per minute. This is a fact about
		// the exchange, not an ARGUS policy choice, so it is not an env-
		// configurable [U] value the way symbol list/backfill depth are --
		// WithRESTBudget exists only for tests.
		restBudget: 1200,
		restWindow: time.Minute,
	}
	for _, opt := range opts {
		opt(p)
	}
	// Constructed last, after every Option has run, so this never
	// depends on WithRESTBudget/WithSleepFunc's own relative order.
	p.limiter = newRESTLimiter(p.restBudget, p.restWindow, p.clk, p.sleep)
	return p
}
