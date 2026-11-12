// use-live-candles.ts: the load sequence (live-chart brief, phase 4,
// Part 2C) -- fetch history via the "latest N" mode, apply it once,
// subscribe, apply the snapshot, and fill the review's own item 6 race
// (a candle closed between the REST fetch and the subscribe) if one is
// found. Wires live-socket.ts (Part 2B, the one-socket-per-page
// connection) and live-candles-reducer.ts (the pure decision logic)
// together behind one hook.
//
// Part 2D: `candle`/`candle_closed` messages are exposed via `forming`
// (payload + the wire's own per-connection seq, needed downstream for
// the (open_time, revision, seq) newest-wins comparison, rule 5) for a
// caller to apply with series.update() on its own path -- this hook
// itself never touches a chart. `candle_corrected` is deliberately NOT
// exposed through `forming`: live-candles-reducer.ts's own doc comment
// on decideApplyAction is explicit that a correction is never applied
// through update() (rule 6, closed candles are immutable except via a
// correction, and even a correction goes through a refetch+setData,
// never a direct rewrite) -- it goes through refetchAndMergeCorrection
// below instead, landing in `history` the same way a snapshot gap-fill
// already does.
import { useEffect, useRef, useState } from "react";
import { listInstrumentCandles, type CandleInterval as ApiCandleInterval } from "@/api/instruments";
import type { ApiCandleHistory } from "@/api/types";
import {
  getLiveSocket,
  type CandleInterval,
  type ConnectionState,
  type WireCandlePayload,
  type WireErrorPayload,
  type WireSnapshotPayload,
} from "./live-socket";
import { checkSnapshotGap, INTERVAL_MS } from "./live-candles-reducer";

const MAX_CANDLES = 1000;

export interface LiveTick {
  candle: WireCandlePayload;
  seq: number;
}

export interface UseLiveCandlesResult {
  history: ApiCandleHistory | null;
  historyLoading: boolean;
  historyError: Error | null;
  connectionState: ConnectionState;
  // The latest candle/candle_closed message this subscription has seen
  // -- see this file's own doc comment above on why candle_corrected is
  // never surfaced here.
  forming: LiveTick | null;
  lastError: WireErrorPayload | null;
}

export function useLiveCandles(instrumentId: number | undefined, interval: CandleInterval): UseLiveCandlesResult {
  const [history, setHistory] = useState<ApiCandleHistory | null>(null);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<Error | null>(null);
  const [connectionState, setConnectionState] = useState<ConnectionState>("closed");
  const [forming, setForming] = useState<LiveTick | null>(null);
  const [lastError, setLastError] = useState<WireErrorPayload | null>(null);

  // historyRef mirrors `history` for the gap-check and the live-socket
  // callbacks below, which run outside React's own render cycle and
  // need the CURRENT newest held bar, not whatever was captured in the
  // closure that registered them (the same staleness Part 2A's own
  // crosshair-handler bug, lastBarRef, already fixed once in
  // chart-surface.tsx, for the same underlying reason).
  const historyRef = useRef<ApiCandleHistory | null>(null);
  historyRef.current = history;

  useEffect(() => {
    if (instrumentId === undefined) {
      // No instrument resolved (yet, or at all) -- reset rather than
      // leave a previous instrument's own history on screen under a
      // caller that has moved on from it.
      setHistory(null);
      historyRef.current = null;
      setHistoryLoading(false);
      setHistoryError(null);
      setForming(null);
      return;
    }

    let cancelled = false;
    const controller = new AbortController();
    setHistoryLoading(true);
    setHistoryError(null);
    setHistory(null);
    historyRef.current = null;
    // A real instrument or interval switch (not just instrumentId going
    // undefined, the branch above): the OLD forming tick belongs to a
    // subscription this effect's own cleanup is about to release, and
    // the new one may not send its first candle/candle_closed message
    // for up to a whole interval -- chart-surface.tsx's own live-tick
    // effect must never apply that stale tick to the freshly-loaded
    // chart in the meantime.
    setForming(null);

    listInstrumentCandles(
      instrumentId,
      { interval: interval as ApiCandleInterval, order: "desc", limit: MAX_CANDLES },
      { signal: controller.signal },
    )
      .then((data) => {
        if (cancelled) return;
        // order=desc comes back newest-first; chart and gap-check logic
        // both expect ascending, same convention
        // AnalysisWorkspaceProvider's own candle fetch already uses.
        const ascending: ApiCandleHistory = { ...data, candles: [...data.candles].reverse() };
        setHistory(ascending);
        historyRef.current = ascending;
        setHistoryLoading(false);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setHistoryError(err instanceof Error ? err : new Error(String(err)));
        setHistoryLoading(false);
      });

    const unsubscribe = getLiveSocket().subscribe(instrumentId, interval, {
      onConnectionState: setConnectionState,
      onSnapshot: (payload, seq) => {
        applySnapshot(instrumentId, interval, payload, seq);
      },
      onCandle: (payload, seq) => setForming({ candle: payload, seq }),
      onCandleClosed: (payload, seq) => setForming({ candle: payload, seq }),
      // Never surfaced via `forming` -- see this file's own doc comment
      // at the top on why a correction must go through a refetch
      // instead of series.update().
      onCandleCorrected: (payload) => refetchAndMergeCorrection(instrumentId, interval, payload.open_time),
      onError: (payload) => setLastError(payload),
    });

    function applySnapshot(id: number, iv: CandleInterval, payload: WireSnapshotPayload, seq: number) {
      const held = historyRef.current;
      const newestHeldOpenTime = held && held.candles.length > 0 ? held.candles[held.candles.length - 1].open_time : null;
      const gap = checkSnapshotGap(payload, newestHeldOpenTime, INTERVAL_MS[iv]);
      if (payload.candle) setForming({ candle: payload.candle, seq });
      if (!gap.hasGap || !gap.refetchFrom || !gap.refetchTo) return;

      // review item 6: fill exactly the missing range before any live
      // update is trusted -- never guessed, always a real REST read.
      listInstrumentCandles(id, { interval: iv as ApiCandleInterval, order: "asc", from: gap.refetchFrom, to: gap.refetchTo })
        .then((filled) => {
          if (cancelled || !historyRef.current) return;
          const merged: ApiCandleHistory = {
            ...filled,
            candles: [...historyRef.current.candles, ...filled.candles],
          };
          setHistory(merged);
          historyRef.current = merged;
        })
        .catch((err: unknown) => {
          if (!cancelled) setHistoryError(err instanceof Error ? err : new Error(String(err)));
        });
    }

    // refetchAndMergeCorrection: a candle_corrected message names an
    // open_time that may already be a CLOSED bar on screen -- rule 6
    // (closed candles are immutable except via a correction) and the
    // reducer's own doc comment both say this never goes through
    // series.update(), only a real REST read replacing that one bar in
    // `history`, the same setData path a normal reload already uses.
    // A no-op if that bar is not currently held (outside the MAX_CANDLES
    // window): nothing on screen needs correcting.
    function refetchAndMergeCorrection(id: number, iv: CandleInterval, openTime: string) {
      const to = new Date(new Date(openTime).getTime() + INTERVAL_MS[iv]).toISOString();
      listInstrumentCandles(id, { interval: iv as ApiCandleInterval, order: "asc", from: openTime, to })
        .then((corrected) => {
          if (cancelled || !historyRef.current || corrected.candles.length === 0) return;
          const fixed = corrected.candles[0];
          const merged: ApiCandleHistory = {
            ...historyRef.current,
            candles: historyRef.current.candles.map((c) => (c.open_time === openTime ? fixed : c)),
          };
          setHistory(merged);
          historyRef.current = merged;
        })
        .catch((err: unknown) => {
          if (!cancelled) setHistoryError(err instanceof Error ? err : new Error(String(err)));
        });
    }

    return () => {
      cancelled = true;
      controller.abort();
      unsubscribe();
    };
  }, [instrumentId, interval]);

  return { history, historyLoading, historyError, connectionState, forming, lastError };
}
