import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useCallback } from "react";
import { getSetup, getSetupEvidence, listSetups } from "@/api/setups";
import { listInstruments } from "@/api/instruments";
import { useWatchlistStore } from "@/lib/watchlist-store";
import type { ApiCandle, ApiInstrument, ApiInstrumentStats, ApiSetup, ApiSetupEvent, ApiSetupEvidence } from "@/api/types";
import { toInstrumentStats, useLiveStat, type LiveStatsEntry, type LiveStatsSnapshot } from "@/lib/live-stats";
import { listSetupEvents } from "@/api/setups";
import { ApiClientError } from "@/api/client";
import { useLiveCandles, type HistoryBackResult, type HistoryEdge, type LiveTick, type OlderPageOutcome } from "@/lib/use-live-candles";
import { preferences, usePreferences, usePreferencesSynced } from "@/lib/preferences";
import { useWatchlist } from "@/api/hooks";
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

  // Resolved independently of setup (see resolvedInstrumentId's own
  // doc comment below) -- an instrument with zero setups still
  // resolves here, unlike before.
  instrument: ApiInstrument | undefined;
  // True once instruments have loaded and a specific instrument id was
  // requested (by the URL's own instrumentId, or by a resolved
  // setup's instrument_id) but matches no enabled instrument --
  // distinct from "nothing requested yet" or "still loading", so a
  // consumer can show a real error instead of just treating this the
  // same as the ordinary "no instrument resolved yet" case.
  instrumentNotFound: boolean;
  setup: ApiSetup | undefined;
  setupLoading: boolean;
  setupError: ApiClientError | undefined;

  events: ApiSetupEvent[];
  eventsLoading: boolean;

  otherSetups: ApiSetup[];
  otherSetupsLoading: boolean;

  // Every setup ARGUS found for the selected instrument (newest updated
  // first, at most SETUPS_ON_CHART), each with its lifecycle and recorded
  // evidence (Phase 6): what the chart's setup layers draw.
  instrumentSetups: ApiSetup[];
  // The selected setup's recorded evidence: undefined while loading,
  // recorded false for a setup created before evidence was stored.
  evidence: ApiSetupEvidence | undefined;
  // Opens a setup (the URL carries it, so a link to it works).
  selectSetup: (id: number) => void;
  // The chart element and the "Why this setup" row that are highlighted
  // together ("<setupId>:<kind>[:<role>]"); null for none.
  focusedElement: string | null;
  setFocusedElement: (id: string | null) => void;
  // "Play the story": the step drawn up to, null when not playing.
  storyStep: number | null;
  setStoryStep: (n: number | null) => void;
  // Framing the selected setup on the chart (polish pass, item 4): bumped
  // when a setup is explicitly selected (the link names it) and by the
  // panel's Focus button, never by ticks or refreshes. The chart reports
  // back a sentence when not all of the evidence fits with its labels.
  focusRequest: number;
  focusSetup: () => void;
  focusNotice: string | null;
  setFocusNotice: (s: string | null) => void;

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
  // The selected instrument's figures from the page's ONE live stats
  // source (live-stats.ts, Phase 3): GET /instruments/stats once, then
  // every `stats` WebSocket message. The side panel, the watchlist and
  // the chart legend all read this same entry, so they can never show
  // two different 24h changes. stats is undefined until both the live
  // and the closed-candle figures have arrived; statsEntry carries the
  // live part (and its motion) on its own as soon as it exists. Never
  // the previous instrument's figures: entries are keyed by id.
  stats: ApiInstrumentStats | undefined;
  statsEntry: LiveStatsEntry | undefined;
  statsSnapshot: LiveStatsSnapshot;
  // Extends the chart's history back to fromISO (the range buttons):
  // pages of older candles until it is covered, nothing older is stored
  // (exhausted) or the chart's own bar cap is reached (capped).
  loadHistoryBack: (fromISO: string) => Promise<HistoryBackResult>;
  // One older page in front of the chart's history, for scrolling back to
  // the left edge (L1.2); historyEdge says when the start of the stored
  // history (or the chart's bar cap) has been reached.
  loadOlder: () => Promise<OlderPageOutcome>;
  historyEdge: HistoryEdge;
}

const Ctx = createContext<AnalysisWorkspace | null>(null);

// [U] display values: how many of an instrument's setups the chart draws
// (the newest updated; the API's own maximum) and how often they are re-read.
export const SETUPS_ON_CHART = 200;
export const SETUPS_REFRESH_MS = 30_000;

function resolvedInstrumentIdForSetups(
  urlInstrumentId: string | null,
  saved: number | undefined,
  fallback: number | undefined,
): number | undefined {
  return urlInstrumentId ? Number(urlInstrumentId) : (saved ?? fallback);
}

export function useAnalysisWorkspace(): AnalysisWorkspace {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAnalysisWorkspace must be used within AnalysisWorkspaceProvider");
  return ctx;
}

export function AnalysisWorkspaceProvider({ children }: { children: ReactNode }) {
  const [params, setParams] = useSearchParams();
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

  // Saved preferences (D1): the timeframe and, when the link names no
  // instrument or setup, the last instrument the user chose.
  const prefs = usePreferences();
  const prefsSynced = usePreferencesSynced();
  const watchlistIds = useWatchlist().data;
  // Only an instrument that is still enabled (in the list) counts. An old
  // saved id (e.g. a fixture instrument the operator has since disabled)
  // falls back to the user's first watchlist item that is enabled, then the
  // first instrument -- never an error screen.
  const enabledIds = useMemo(() => new Set(instruments.map((i) => i.id)), [instruments]);
  const savedInstrumentId =
    prefs.selected_instrument_id !== undefined && enabledIds.has(prefs.selected_instrument_id)
      ? prefs.selected_instrument_id
      : undefined;
  const fallbackInstrumentId = watchlistIds?.find((id) => enabledIds.has(id)) ?? instruments[0]?.id;
  // ...and the stale saved value is corrected to that fallback, once the
  // server copy has been read (so a stale local cache never overwrites a
  // valid server value).
  const staleSavedId =
    prefs.selected_instrument_id !== undefined && instruments.length > 0 && !enabledIds.has(prefs.selected_instrument_id);
  useEffect(() => {
    if (staleSavedId && prefsSynced && fallbackInstrumentId !== undefined) {
      preferences.set("selected_instrument_id", fallbackInstrumentId);
    }
  }, [staleSavedId, prefsSynced, fallbackInstrumentId]);

  // 1. Instruments: once, and again (quietly, no loading state) after each
  // watchlist add, which may have enabled an instrument this list did not
  // have (D15.1), so selecting the new row finds it.
  const watchlistVersion = useWatchlistStore().version;
  useEffect(() => {
    const controller = new AbortController();
    if (watchlistVersion === 0) setInstrumentsLoading(true);
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
  }, [watchlistVersion]);

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
      const targetInstrumentId = urlInstrumentId ? Number(urlInstrumentId) : (savedInstrumentId ?? fallbackInstrumentId);
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
  }, [instrumentsLoading, instruments, urlSetupId, urlInstrumentId, savedInstrumentId, fallbackInstrumentId]);

  // 2b. The resolved instrument id -- independent of whether the
  // resolved setup (if any) exists, deliberately NOT just
  // `setup?.instrument_id` the way this used to be derived. That
  // derivation meant an instrument with zero setups could never be
  // selected at all: resolve() above sets `setup` to undefined for
  // such an instrument (list[0] of an empty array), so `instrument`
  // came out undefined too, the symbol picker stayed on "Select
  // instrument" forever, and useLiveCandles below never subscribed to
  // anything (real bug this fixes).
  //
  // Order, matching the brief's own instruction: the instrument that
  // owns the URL's setupId if one is given (setup wins over a
  // disagreeing URL instrumentId -- see the second effect below),
  // else the URL's instrumentId directly (never waiting on any setup
  // lookup), else the first instrument.
  const [resolvedInstrumentId, setResolvedInstrumentId] = useState<number | undefined>();

  useEffect(() => {
    if (instrumentsLoading) return;
    if (urlSetupId) {
      // Deferred to the effect below, once the setup itself resolves
      // -- the URL's own instrumentId (if present and disagreeing) is
      // never consulted in this case.
      return;
    }
    setResolvedInstrumentId(urlInstrumentId ? Number(urlInstrumentId) : (savedInstrumentId ?? fallbackInstrumentId));
  }, [instrumentsLoading, instruments, urlSetupId, urlInstrumentId, savedInstrumentId, fallbackInstrumentId]);

  // setup wins: once a URL-specified setup resolves, the instrument it
  // belongs to is authoritative, even if the URL also carried a
  // different instrumentId. That disagreeing instrumentId is simply
  // never read in this branch.
  useEffect(() => {
    if (urlSetupId && setup) setResolvedInstrumentId(setup.instrument_id);
  }, [urlSetupId, setup]);

  const instrument = useMemo(
    () => instruments.find((i) => i.id === resolvedInstrumentId),
    [instruments, resolvedInstrumentId],
  );

  // A specific instrument id was asked for (by the URL directly, or
  // by a resolved setup's own instrument_id) but matches nothing in
  // the enabled-instrument list GET /instruments returns -- either it
  // does not exist, or it is disabled. Distinct from "nothing resolved
  // yet" (resolvedInstrumentId still undefined, e.g. instruments still
  // loading, or no instruments exist at all and none was explicitly
  // requested) -- only the former is a real error ChartSurface shows.
  const instrumentNotFound =
    !instrumentsLoading && resolvedInstrumentId !== undefined && !instrument;

  // An explicit choice (the link names the instrument or setup -- the
  // symbol picker selects by link too) is saved. A fallback (the saved
  // value itself, or the first instrument) is not: saving an automatic
  // default on a new device would mark it as this device's own change
  // and override the choice the server copy is about to deliver.
  useEffect(() => {
    if (instrument && (urlInstrumentId || urlSetupId)) preferences.set("selected_instrument_id", instrument.id);
  }, [instrument, urlInstrumentId, urlSetupId]);

  // The saved timeframe, unless the selected instrument does not offer it
  // (e.g. 1h saved, then an instrument that only has 1m): then 1m for this
  // instrument, without overwriting the saved choice.
  const savedTimeframe = prefs.timeframe ?? "1m";
  const timeframe: CandleInterval =
    instrument && !instrument.supported_intervals.includes(savedTimeframe) ? "1m" : savedTimeframe;

  function setTimeframe(tf: CandleInterval) {
    if (instrument && !instrument.supported_intervals.includes(tf)) return;
    preferences.set("timeframe", tf);
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

  // 4. Every setup of the selected instrument, with its lifecycle and
  // evidence, for the chart layers and the "other setups" strip; re-read
  // every SETUPS_REFRESH_MS so a new setup or a state change appears
  // without a reload (no setup push exists on the socket yet).
  const [instrumentSetups, setInstrumentSetups] = useState<ApiSetup[]>([]);
  const layerInstrumentId = setup?.instrument_id ?? resolvedInstrumentIdForSetups(urlInstrumentId, savedInstrumentId, fallbackInstrumentId);
  useEffect(() => {
    if (layerInstrumentId === undefined) {
      setInstrumentSetups([]);
      return;
    }
    const controller = new AbortController();
    let first = true;
    const load = () => {
      if (first) setOtherSetupsLoading(true);
      listSetups({ instrumentId: layerInstrumentId, includeEvidence: true, limit: SETUPS_ON_CHART }, { signal: controller.signal })
        .then((data) => {
          if (!controller.signal.aborted) setInstrumentSetups(data);
        })
        .catch(() => {
          // Keep what is drawn; the next refresh tries again.
        })
        .finally(() => {
          if (!controller.signal.aborted && first) setOtherSetupsLoading(false);
          first = false;
        });
    };
    setInstrumentSetups([]);
    load();
    const id = setInterval(() => {
      if (document.visibilityState === "visible") load();
    }, SETUPS_REFRESH_MS);
    return () => {
      controller.abort();
      clearInterval(id);
    };
  }, [layerInstrumentId]);

  useEffect(() => {
    setOtherSetups(setup ? instrumentSetups.filter((s) => s.id !== setup.id) : instrumentSetups);
  }, [instrumentSetups, setup]);

  // 4b. The selected setup's evidence: from the list when it is there,
  // else read on its own (a setup older than the SETUPS_ON_CHART newest).
  const listed = setup ? instrumentSetups.find((s) => s.id === setup.id) : undefined;
  const [fetchedEvidence, setFetchedEvidence] = useState<ApiSetupEvidence | undefined>();
  useEffect(() => {
    setFetchedEvidence(undefined);
    if (!setup || listed?.evidence) return;
    const controller = new AbortController();
    getSetupEvidence(setup.id, { signal: controller.signal })
      .then((ev) => {
        if (!controller.signal.aborted) setFetchedEvidence(ev);
      })
      .catch(() => {});
    return () => controller.abort();
  }, [setup, listed?.evidence]);
  const evidence = listed?.evidence ?? fetchedEvidence;

  const [focusedElement, setFocusedElement] = useState<string | null>(null);
  const [focusRequest, setFocusRequest] = useState(0);
  const [focusNotice, setFocusNotice] = useState<string | null>(null);
  const focusSetup = useCallback(() => setFocusRequest((n) => n + 1), []);
  // An explicit selection (a link or the URL names the setup) frames it,
  // once that setup has loaded (never the previous one while it loads).
  const selectedId = setup?.id;
  useEffect(() => {
    if (urlSetupId && selectedId !== undefined && String(selectedId) === urlSetupId) setFocusRequest((n) => n + 1);
  }, [urlSetupId, selectedId]);
  const [storyStep, setStoryStep] = useState<number | null>(null);
  useEffect(() => {
    setFocusedElement(null);
    setStoryStep(null);
    setFocusNotice(null);
  }, [setup?.id]);
  const selectSetup = useCallback(
    (id: number) => {
      const next = new URLSearchParams(params);
      next.set("setupId", String(id));
      next.delete("instrumentId");
      setParams(next);
    },
    [params, setParams],
  );

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
  //
  // instrument.id, not setup?.instrument_id: an instrument with zero
  // setups must still get its candles/live subscription (the bug this
  // fixes) -- instrument is already undefined for a not-found/disabled
  // id (instrumentNotFound above), so this correctly never subscribes
  // to an invalid instrument either.
  const live = useLiveCandles(instrument?.id, timeframe);
  const candles = useMemo(() => live.history?.candles ?? [], [live.history]);
  const candlesLoading = live.historyLoading;
  const candlesError = useMemo(
    () => (live.historyError ? new ApiClientError(live.historyError.message, 0) : undefined),
    [live.historyError],
  );

  // 6. Live figures for the selected instrument, from the page's one
  // stats source (no polling: the socket pushes every change).
  const { entry: statsEntry, snapshot: statsSnapshot } = useLiveStat(instrument?.id);
  const stats = toInstrumentStats(statsEntry);

  const value: AnalysisWorkspace = {
    instruments,
    instrumentsLoading,
    instrumentsError,
    instrument,
    instrumentNotFound,
    setup: listed ?? setup,
    setupLoading,
    setupError,
    events,
    eventsLoading,
    otherSetups,
    otherSetupsLoading,
    // The selected setup as listed (with its lifecycle) when it is there.
    instrumentSetups,
    evidence,
    selectSetup,
    focusedElement,
    setFocusedElement,
    storyStep,
    setStoryStep,
    focusRequest,
    focusSetup,
    focusNotice,
    setFocusNotice,
    candles,
    candlesLoading,
    candlesError,
    timeframe,
    setTimeframe,
    connectionState: live.connectionState,
    forming: live.forming,
    status: live.status,
    fatalReason: live.fatalReason,
    stats,
    statsEntry,
    statsSnapshot,
    loadHistoryBack: live.loadHistoryBack,
    loadOlder: live.loadOlder,
    historyEdge: live.historyEdge,
  };

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
