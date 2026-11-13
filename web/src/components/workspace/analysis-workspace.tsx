import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { getSetup, listSetups } from "@/api/setups";
import { listInstruments } from "@/api/instruments";
import type { ApiCandle, ApiInstrument, ApiSetup, ApiSetupEvent } from "@/api/types";
import { listSetupEvents } from "@/api/setups";
import { ApiClientError } from "@/api/client";
import { useLiveCandles, type LiveTick } from "@/lib/use-live-candles";
import type { CandleInterval, ConnectionState, WireStatusPayload } from "@/lib/live-socket";

// Centralizes every fetch the /analysis workspace's many separate panels
// need (chart, right panel's analysis tab, other-setups strip, markets
// list), so each one reads from one place instead of independently
// re-deriving "which setup/instrument is this". The selected setup comes
// from the URL (?setupId=&instrumentId=, set by the /setups table's own
// links); with neither present, this falls back to the first instrument's
// most-recently-updated setup, so /analysis is never empty just because
// a user landed on it directly.
//
// Candles (see useLiveCandles below) are "the most recent MAX_CANDLES"
// (that constant lives in use-live-candles.ts now, not here), not a
// window anchored to this setup's own evaluation_time -- order=desc
// with no from/to (web-wiring-polish brief, item 1) is what makes that
// possible without a dedicated "latest closed candle" endpoint.

interface AnalysisWorkspace {
  instruments: ApiInstrument[];
  instrumentsLoading: boolean;
  instrumentsError: ApiClientError | undefined;

  instrument: ApiInstrument | undefined;
  setup: ApiSetup | undefined;
  setupLoading: boolean;
  setupError: ApiClientError | undefined;

  events: ApiSetupEvent[];
  eventsLoading: boolean;

  otherSetups: ApiSetup[];
  otherSetupsLoading: boolean;

  candles: ApiCandle[];
  candlesLoading: boolean;
  candlesError: ApiClientError | undefined;

  // The chart's own timeframe (live-chart brief, phase 4, Part 4, item
  // 1) -- owned here, not in DrawingProvider (which reads it back via
  // useAnalysisWorkspace() instead of a second copy), because it also
  // drives useLiveCandles' own subscription below and DrawingProvider
  // is a descendant of this provider, not an ancestor: it cannot feed
  // a value back up to where the socket subscription lives.
  timeframe: CandleInterval;
  // Clamps to "1m" if the requested interval is not in the current
  // instrument's own supported_intervals (ChartToolbar's own job is to
  // not offer a disabled one in the first place; this is the backend's
  // own authoritative list, never a client-side crypto/non-crypto
  // guess, and the second line of defense if it is ever asked for
  // anyway).
  setTimeframe: (tf: CandleInterval) => void;
  connectionState: ConnectionState;
  // The latest live candle/candle_closed tick -- read by chart-surface.tsx
  // alone, on its own series.update() path (Part 2D), never folded into
  // `candles` above (rule 3: live ticks never go through the candles prop).
  forming: LiveTick | null;
  // Part 2F: the server's own freshness verdict (live/stale/degraded)
  // and provider name for the status badge -- read straight off the
  // wire, never derived or guessed client-side.
  status: WireStatusPayload | null;
  // Part 2E: set once the connection closes for a reason this client
  // will not retry (the session itself is invalid) -- chart-surface.tsx
  // shows this instead of a spinner.
  fatalReason: string | null;
}

const Ctx = createContext<AnalysisWorkspace | null>(null);

export function useAnalysisWorkspace(): AnalysisWorkspace {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAnalysisWorkspace must be used within AnalysisWorkspaceProvider");
  return ctx;
}

export function AnalysisWorkspaceProvider({ children }: { children: ReactNode }) {
  const [params] = useSearchParams();
  const urlSetupId = params.get("setupId");
  const urlInstrumentId = params.get("instrumentId");

  const [instruments, setInstruments] = useState<ApiInstrument[]>([]);
  const [instrumentsLoading, setInstrumentsLoading] = useState(true);
  const [instrumentsError, setInstrumentsError] = useState<ApiClientError | undefined>();

  const [setup, setSetup] = useState<ApiSetup | undefined>();
  const [setupLoading, setSetupLoading] = useState(true);
  const [setupError, setSetupError] = useState<ApiClientError | undefined>();

  const [events, setEvents] = useState<ApiSetupEvent[]>([]);
  const [eventsLoading, setEventsLoading] = useState(false);

  const [otherSetups, setOtherSetups] = useState<ApiSetup[]>([]);
  const [otherSetupsLoading, setOtherSetupsLoading] = useState(false);

  const [timeframe, setTimeframeState] = useState<CandleInterval>("1m");

  // 1. Instruments, once.
  useEffect(() => {
    const controller = new AbortController();
    setInstrumentsLoading(true);
    listInstruments({ signal: controller.signal })
      .then((data) => {
        if (!controller.signal.aborted) setInstruments(data);
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setInstrumentsError(err instanceof ApiClientError ? err : new ApiClientError(String(err), 0));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setInstrumentsLoading(false);
      });
    return () => controller.abort();
  }, []);

  // 2. The selected setup: from the URL if given, otherwise the first
  // instrument's own most-recently-updated setup (GET /setups already
  // orders newest-updated-first).
  useEffect(() => {
    if (instrumentsLoading) return;
    const controller = new AbortController();
    setSetupLoading(true);
    setSetupError(undefined);

    async function resolve(): Promise<ApiSetup | undefined> {
      if (urlSetupId) {
        return getSetup(Number(urlSetupId), { signal: controller.signal });
      }
      const targetInstrumentId = urlInstrumentId ? Number(urlInstrumentId) : instruments[0]?.id;
      if (targetInstrumentId === undefined) return undefined;
      const list = await listSetups({ instrumentId: targetInstrumentId }, { signal: controller.signal });
      return list[0];
    }

    resolve()
      .then((s) => {
        if (!controller.signal.aborted) setSetup(s);
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setSetupError(err instanceof ApiClientError ? err : new ApiClientError(String(err), 0));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setSetupLoading(false);
      });
    return () => controller.abort();
  }, [instrumentsLoading, instruments, urlSetupId, urlInstrumentId]);

  const instrument = useMemo(
    () => instruments.find((i) => i.id === setup?.instrument_id),
    [instruments, setup],
  );

  // A genuine instrument switch can land on a timeframe the NEW
  // instrument's own supported_intervals does not have (e.g. 1h was
  // selected on a crypto instrument, the user then switches to a
  // non-crypto one that only supports 1m) -- clamp rather than let
  // useLiveCandles below subscribe with an interval the backend will
  // reject.
  useEffect(() => {
    if (instrument && !instrument.supported_intervals.includes(timeframe)) {
      setTimeframeState("1m");
    }
  }, [instrument, timeframe]);

  function setTimeframe(tf: CandleInterval) {
    if (instrument && !instrument.supported_intervals.includes(tf)) return;
    setTimeframeState(tf);
  }

  // 3. Evidence (the setup's own event history).
  useEffect(() => {
    if (!setup) {
      setEvents([]);
      return;
    }
    const controller = new AbortController();
    setEventsLoading(true);
    listSetupEvents(setup.id, { signal: controller.signal })
      .then((data) => {
        if (!controller.signal.aborted) setEvents(data);
      })
      .catch(() => {
        if (!controller.signal.aborted) setEvents([]);
      })
      .finally(() => {
        if (!controller.signal.aborted) setEventsLoading(false);
      });
    return () => controller.abort();
  }, [setup]);

  // 4. "Other setups" on the SAME instrument (brief's own scope: "from
  // the setups list for the same instrument" -- a deliberate change from
  // the fixture-era strip, which showed other INSTRUMENTS; see the
  // report).
  useEffect(() => {
    if (!setup) {
      setOtherSetups([]);
      return;
    }
    const controller = new AbortController();
    setOtherSetupsLoading(true);
    listSetups({ instrumentId: setup.instrument_id }, { signal: controller.signal })
      .then((data) => {
        if (!controller.signal.aborted) setOtherSetups(data.filter((s) => s.id !== setup.id));
      })
      .catch(() => {
        if (!controller.signal.aborted) setOtherSetups([]);
      })
      .finally(() => {
        if (!controller.signal.aborted) setOtherSetupsLoading(false);
      });
    return () => controller.abort();
  }, [setup]);

  // 5. Chart candles for the selected instrument: history (the most
  // recent MAX_CANDLES, not a window anchored at the setup's own
  // evaluation_time -- web-wiring-followup brief, Part 5) plus the live
  // snapshot and gap-fill (live-chart brief, phase 4, Part 2C) --
  // useLiveCandles already returns ascending order and already
  // subscribes to /ws, filling the review's own item 6 race (a candle
  // closed between this REST fetch and the subscribe) into `history`
  // itself.
  //
  // interval is now `timeframe` (Part 4, item 1), not a fixed "1m" --
  // useLiveCandles' own effect is keyed on [instrumentId, interval], so
  // React runs the OLD effect's cleanup (which calls live-socket.ts's
  // own unsubscribe) before the new one subscribes with the new
  // interval; the old subscription is always released first, nothing
  // extra is needed here for that.
  const live = useLiveCandles(setup?.instrument_id, timeframe);
  const candles = useMemo(() => live.history?.candles ?? [], [live.history]);
  const candlesLoading = live.historyLoading;
  const candlesError = useMemo(
    () => (live.historyError ? new ApiClientError(live.historyError.message, 0) : undefined),
    [live.historyError],
  );

  const value: AnalysisWorkspace = {
    instruments,
    instrumentsLoading,
    instrumentsError,
    instrument,
    setup,
    setupLoading,
    setupError,
    events,
    eventsLoading,
    otherSetups,
    otherSetupsLoading,
    candles,
    candlesLoading,
    candlesError,
    timeframe,
    setTimeframe,
    connectionState: live.connectionState,
    forming: live.forming,
    status: live.status,
    fatalReason: live.fatalReason,
  };

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
