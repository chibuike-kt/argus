// live-candles-reducer.ts: the pure decision logic behind the load
// sequence (live-chart brief, phase 4, Part 2C) -- kept free of
// fetch/WebSocket/React so it can be unit-tested directly, the same
// way internal/hub's own routing logic is tested against a fake
// Subscriber rather than a real Redis connection.
//
// Prices/volume are never converted to number here (brief rule 2):
// candleKey below compares by open_time string equality and
// quant.Price-shaped decimal strings are compared only through
// isNewerCandleKey's own (open_time, revision, seq) tuple -- brief
// rule 5 -- never by parsing the OHLC values themselves.

import type { CandleInterval, WireCandlePayload, WireSnapshotPayload } from "./live-socket";

export interface CandleKey {
  openTime: string;
  revision: number;
  seq: number;
}

// isNewer implements rule 5: a message is applied only if it is newer
// than what is applied, compared as (open_time, revision, seq) --
// never by wall-clock arrival order, which duplicates and out-of-order
// delivery cannot be trusted to reflect.
export function isNewerCandleKey(next: CandleKey, applied: CandleKey | null): boolean {
  if (!applied) return true;
  if (next.openTime !== applied.openTime) return next.openTime > applied.openTime;
  if (next.revision !== applied.revision) return next.revision > applied.revision;
  return next.seq > applied.seq;
}

export interface GapCheckResult {
  // hasGap is true when the review's own item 6 race applies: a candle
  // closed between this subscription's initial REST fetch and the
  // snapshot that followed it, so the chart is missing one or more
  // closed bars the live stream will never resend (it only ever sends
  // forward from here).
  hasGap: boolean;
  // refetchFrom/refetchTo bound exactly the missing range, in the
  // caller's own interval -- refetchFrom is the minute after the
  // newest bar already held (never re-requesting a bar already
  // applied), refetchTo is the snapshot's own join point, inclusive
  // (the REST endpoint's own "to" is exclusive of the window AFTER
  // the one named, matching listInstrumentCandles' existing [from, to)
  // convention -- the caller passes refetchTo through unchanged).
  refetchFrom?: string;
  refetchTo?: string;
}

// checkSnapshotGap is the review's own item 6 rule: "after the snapshot
// arrives, if its last_closed_open_time is later than the newest
// closed candle the client holds, the client refetches from its newest
// closed candle to the join point before applying live updates."
// newestHeldOpenTime is this client's own newest APPLIED closed bar
// (null if history came back empty); intervalMs lets the caller
// compute "the minute after" generically for any of the six intervals.
export function checkSnapshotGap(
  snapshot: WireSnapshotPayload,
  newestHeldOpenTime: string | null,
  intervalMs: number,
): GapCheckResult {
  const joinPoint = snapshot.last_closed_open_time;
  if (joinPoint === null) return { hasGap: false };
  if (newestHeldOpenTime === null) {
    // No history was held at all (an empty instrument, or the very
    // first subscribe before any REST fetch completed) -- there is
    // nothing to gap-check against; the snapshot's own forming
    // candle (if any) and the join point are the only truth available,
    // and whatever REST history call already covers the window handles
    // filling it in, not this function.
    return { hasGap: false };
  }
  if (joinPoint <= newestHeldOpenTime) return { hasGap: false };

  const refetchFrom = new Date(new Date(newestHeldOpenTime).getTime() + intervalMs).toISOString();
  // +intervalMs again: refetchTo is exclusive in listInstrumentCandles'
  // own [from, to) convention, and the join point's own bar must be
  // included.
  const refetchTo = new Date(new Date(joinPoint).getTime() + intervalMs).toISOString();
  return { hasGap: true, refetchFrom, refetchTo };
}

export const INTERVAL_MS: Record<CandleInterval, number> = {
  "1m": 60_000,
  "5m": 5 * 60_000,
  "15m": 15 * 60_000,
  "1h": 60 * 60_000,
  "4h": 4 * 60 * 60_000,
  "1d": 24 * 60 * 60_000,
};

// applyCandle decides, for one incoming candle/candle_closed message,
// whether it replaces the current last bar (same open_time) or
// appends (a strictly newer open_time) -- lightweight-charts 5.2.1's
// own series.update() contract (lightweight-charts-update-contract.test.ts),
// which throws for an older time without historicalUpdate=true. An
// older time here is never applied through update() at all (rule 6:
// closed candles are never rewritten except through candle_corrected,
// and even that goes through a refetch+setData, not update()) -- this
// function reports that case so the caller can simply drop the message,
// never attempt and catch a throw.
export type ApplyAction = "replace" | "append" | "drop-stale";

export function decideApplyAction(bar: WireCandlePayload, currentLastOpenTime: string | null): ApplyAction {
  if (currentLastOpenTime === null) return "append";
  if (bar.open_time === currentLastOpenTime) return "replace";
  if (bar.open_time > currentLastOpenTime) return "append";
  return "drop-stale";
}
