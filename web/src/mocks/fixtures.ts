import type { Candle, Instrument, Setup, SetupSummary } from "@/types/domain";

// Static fixtures so the scaffold renders without the backend. Shapes match
// @/types/domain; swap for live /api/v1 data when the endpoints land.

export const instrument: Instrument = {
  symbol: "BTCUSDT.P",
  name: "Bitcoin Perpetual",
  venue: "BINANCE",
  last: 68360,
  changePct: 0.53,
};

// Watchlist for the markets strip. Icons resolve from the symbol.
export const markets: Instrument[] = [
  { symbol: "BTCUSDT.P", name: "Bitcoin", venue: "BINANCE", last: 68360, changePct: 0.53 },
  { symbol: "ETHUSDT.P", name: "Ethereum", venue: "BINANCE", last: 2206.34, changePct: -2.04 },
  { symbol: "SOLUSDT.P", name: "Solana", venue: "BINANCE", last: 93.86, changePct: -1.8 },
  { symbol: "BNBUSDT.P", name: "BNB", venue: "BINANCE", last: 612.4, changePct: 0.35 },
  { symbol: "XRPUSDT.P", name: "XRP", venue: "BINANCE", last: 0.5423, changePct: 1.12 },
  { symbol: "ADAUSDT.P", name: "Cardano", venue: "BINANCE", last: 0.3871, changePct: -1.63 },
  { symbol: "AVAXUSDT.P", name: "Avalanche", venue: "BINANCE", last: 27.91, changePct: 2.4 },
  { symbol: "LINKUSDT.P", name: "Chainlink", venue: "BINANCE", last: 14.22, changePct: 0.9 },
  { symbol: "DOGEUSDT.P", name: "Dogecoin", venue: "BINANCE", last: 0.1392, changePct: -0.52 },
];

export const asOf = "2026-10-29T14:40:00Z";

// The Setups inbox: proposed plans across the watchlist.
export const setups: SetupSummary[] = [
  {
    id: "s1",
    symbol: "BTCUSDT.P",
    direction: "bullish",
    poiType: "order_block",
    timeframe: "5m",
    entry: 67880,
    stop: 67630,
    rr: 2.0,
    state: "valid",
    createdAt: "2026-10-29T14:30:00Z",
  },
  {
    id: "s2",
    symbol: "ETHUSDT.P",
    direction: "bearish",
    poiType: "qmr",
    timeframe: "15m",
    entry: 2230,
    stop: 2255,
    rr: 2.4,
    state: "valid",
    createdAt: "2026-10-29T14:05:00Z",
  },
  {
    id: "s3",
    symbol: "SOLUSDT.P",
    direction: "bullish",
    poiType: "order_block",
    timeframe: "5m",
    entry: 92.4,
    stop: 90.8,
    rr: 1.8,
    state: "forming",
    createdAt: "2026-10-29T13:50:00Z",
  },
  {
    id: "s4",
    symbol: "BNBUSDT.P",
    direction: "bullish",
    poiType: "qmr",
    timeframe: "1H",
    entry: 605,
    stop: 598,
    rr: 3.1,
    state: "valid",
    createdAt: "2026-10-29T12:40:00Z",
  },
  {
    id: "s5",
    symbol: "XRPUSDT.P",
    direction: "bearish",
    poiType: "order_block",
    timeframe: "5m",
    entry: 0.546,
    stop: 0.552,
    rr: 1.5,
    state: "triggered",
    createdAt: "2026-10-29T14:38:00Z",
  },
  {
    id: "s6",
    symbol: "AVAXUSDT.P",
    direction: "bullish",
    poiType: "qmr",
    timeframe: "15m",
    entry: 27.2,
    stop: 26.6,
    rr: 2.2,
    state: "valid",
    createdAt: "2026-10-29T13:10:00Z",
  },
  {
    id: "s7",
    symbol: "LINKUSDT.P",
    direction: "bearish",
    poiType: "order_block",
    timeframe: "1H",
    entry: 14.5,
    stop: 14.9,
    rr: 1.9,
    state: "forming",
    createdAt: "2026-10-29T11:40:00Z",
  },
  {
    id: "s8",
    symbol: "ADAUSDT.P",
    direction: "bullish",
    poiType: "order_block",
    timeframe: "5m",
    entry: 0.384,
    stop: 0.378,
    rr: 2.6,
    state: "invalidated",
    createdAt: "2026-10-29T09:40:00Z",
  },
];

// A clean textbook bullish SD-LQ setup on 5m: downtrend, a sell-side sweep of
// the prior swing low, a CHOCH up, an order block, a pullback into discount,
// then expansion. The annotations below are indexed into this series.
export const candles: Candle[] = [
  { openTime: "2026-10-29T13:05:00Z", open: 68000, high: 68060, low: 67980, close: 68040 },
  { openTime: "2026-10-29T13:10:00Z", open: 68040, high: 68090, low: 68020, close: 68070 },
  { openTime: "2026-10-29T13:15:00Z", open: 68070, high: 68120, low: 68050, close: 68100 },
  { openTime: "2026-10-29T13:20:00Z", open: 68100, high: 68130, low: 68080, close: 68110 },
  { openTime: "2026-10-29T13:25:00Z", open: 68110, high: 68115, low: 68010, close: 68030 },
  { openTime: "2026-10-29T13:30:00Z", open: 68030, high: 68050, low: 67950, close: 67970 },
  { openTime: "2026-10-29T13:35:00Z", open: 67970, high: 67990, low: 67890, close: 67910 },
  { openTime: "2026-10-29T13:40:00Z", open: 67910, high: 67930, low: 67820, close: 67850 },
  { openTime: "2026-10-29T13:45:00Z", open: 67850, high: 67900, low: 67840, close: 67880 },
  { openTime: "2026-10-29T13:50:00Z", open: 67880, high: 67920, low: 67850, close: 67900 },
  { openTime: "2026-10-29T13:55:00Z", open: 67900, high: 67910, low: 67650, close: 67880 },
  { openTime: "2026-10-29T14:00:00Z", open: 67880, high: 67900, low: 67840, close: 67860 },
  { openTime: "2026-10-29T14:05:00Z", open: 67860, high: 67980, low: 67850, close: 67960 },
  { openTime: "2026-10-29T14:10:00Z", open: 67960, high: 68080, low: 67950, close: 68060 },
  { openTime: "2026-10-29T14:15:00Z", open: 68060, high: 68160, low: 68040, close: 68150 },
  { openTime: "2026-10-29T14:20:00Z", open: 68150, high: 68170, low: 68020, close: 68050 },
  { openTime: "2026-10-29T14:25:00Z", open: 68050, high: 68060, low: 67880, close: 67900 },
  { openTime: "2026-10-29T14:30:00Z", open: 67900, high: 68050, low: 67880, close: 68030 },
  { openTime: "2026-10-29T14:35:00Z", open: 68030, high: 68180, low: 68010, close: 68160 },
  { openTime: "2026-10-29T14:40:00Z", open: 68160, high: 68400, low: 68150, close: 68360 },
];

// The analytical overlay layer: what ARGUS has drawn on this setup, indexed
// into `candles`. In production this comes from the engine's events and POI.
export interface ChartSwing {
  index: number;
  price: number;
  kind: "high" | "low";
}
export interface ChartEvent {
  index: number;
  price: number;
  label: string;
  tone: "accent" | "up" | "down";
}
export interface ChartZone {
  low: number;
  high: number;
  fromIndex: number;
}
export interface ChartRange {
  low: number;
  high: number;
  equilibrium: number;
}
export interface ChartPlan {
  entry: number;
  stop: number;
  targets: number[];
  fromIndex: number;
}

export const annotations: {
  swings: ChartSwing[];
  events: ChartEvent[];
  liquidity: { index: number; price: number };
  orderBlock: ChartZone;
  range: ChartRange;
  plan: ChartPlan;
} = {
  swings: [
    { index: 3, price: 68130, kind: "high" },
    { index: 7, price: 67820, kind: "low" },
    { index: 10, price: 67650, kind: "low" },
  ],
  events: [
    { index: 10, price: 67650, label: "Sweep", tone: "accent" },
    { index: 14, price: 68130, label: "CHOCH", tone: "up" },
  ],
  liquidity: { index: 7, price: 67820 },
  orderBlock: { low: 67840, high: 67900, fromIndex: 11 },
  range: { low: 67650, high: 68400, equilibrium: 68025 },
  plan: { entry: 67880, stop: 67630, targets: [68100, 68240, 68380], fromIndex: 14 },
};

export const setup: Setup = {
  id: "setup_0f3a",
  instrument: "BTCUSDT.P",
  timeframe: "5m",
  direction: "bullish",
  poiType: "order_block",
  entry: 67880,
  stop: 67630,
  targets: [68100, 68240, 68380],
  rr: 2.0,
  state: "valid",
  evidence: [
    {
      label: "Sell-side sweep",
      detail: "Liquidity at 67,820 swept to 67,650 and reclaimed on the same candle, 13:55 UTC.",
      provenance: "S",
    },
    {
      label: "CHOCH, bullish",
      detail: "Close above the prior swing high at 68,130 flips structure bullish.",
      provenance: "S",
    },
    {
      label: "Sweep-validated break",
      detail: "Validated by the opposing-side protected low at 67,650 (Reading B).",
      provenance: "S",
    },
    {
      label: "Order block",
      detail: "Last bearish candle before the impulse, 14:00 UTC. Zone 67,840 to 67,900.",
      provenance: "S",
    },
    {
      label: "Structural liquidity",
      detail: "Intact low at 67,820 in the discount half backs the POI.",
      provenance: "S",
    },
    {
      label: "Discount location",
      detail: "Entry sits below equilibrium (68,025) of the active dealing range.",
      provenance: "S",
    },
    {
      label: "Full-candle zone",
      detail: "Zone drawn high to low; body-only convention pending a marked chart.",
      provenance: "R",
    },
    {
      label: "Sweep shape",
      detail: "Single-candle sweep; a multi-candle V or A qualifier is pending backtest.",
      provenance: "U",
    },
  ],
};
