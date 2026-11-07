import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { getSetup, listSetups } from "@/api/setups";
import { listInstruments, listInstrumentCandles } from "@/api/instruments";
import type { ApiCandle, ApiInstrument, ApiSetup, ApiSetupEvent } from "@/api/types";
import { listSetupEvents } from "@/api/setups";
import { ApiClientError } from "@/api/client";

// Centralizes every fetch the /analysis workspace's many separate panels
// need (chart, right panel's analysis tab, other-setups strip, markets
// list), so each one reads from one place instead of independently
// re-deriving "which setup/instrument is this". The selected setup comes
// from the URL (?setupId=&instrumentId=, set by the /setups table's own
// links); with neither present, this falls back to the first instrument's
// most-recently-updated setup, so /analysis is never empty just because
// a user landed on it directly.
//
// The API still has no "latest closed candle" endpoint, but order=desc
// (web-wiring-followup brief, Part 4/5) solves the actual problem that
// gap created: [from, to) no longer needs to tightly bracket the real
// data, since limit still caps how many rows the query returns
// regardless of how wide the range is (enforced in SQL, see
// ListClosedCandlesInRangeLimitedDesc's own comment in db/queries) --
// EPOCH to "now" safely contains any real or historical dataset, and
// order=desc&limit=MAX_CANDLES gets back exactly the most recent
// MAX_CANDLES candles within it, reversed below into the ascending
// order the chart needs.
const MAX_CANDLES = 1000;
const EPOCH = new Date(0).toISOString();

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

  const [candles, setCandles] = useState<ApiCandle[]>([]);
  const [candlesLoading, setCandlesLoading] = useState(false);
  const [candlesError, setCandlesError] = useState<ApiClientError | undefined>();

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

  // 5. Chart candles for the selected instrument: the most recent
  // MAX_CANDLES, not a window anchored at the setup's own
  // evaluation_time (web-wiring-followup brief, Part 5) -- order=desc
  // comes back newest-first, reversed here into the ascending order the
  // chart (and everything downstream of `candles`) expects.
  useEffect(() => {
    if (!setup) {
      setCandles([]);
      return;
    }
    const controller = new AbortController();
    setCandlesLoading(true);
    setCandlesError(undefined);
    listInstrumentCandles(
      setup.instrument_id,
      { from: EPOCH, to: new Date().toISOString(), limit: MAX_CANDLES, order: "desc" },
      { signal: controller.signal },
    )
      .then((data) => {
        if (!controller.signal.aborted) setCandles([...data].reverse());
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setCandlesError(err instanceof ApiClientError ? err : new ApiClientError(String(err), 0));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setCandlesLoading(false);
      });
    return () => controller.abort();
  }, [setup]);

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
  };

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
