import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { CaretRight, CircleNotch } from "@phosphor-icons/react";
import { Panel } from "@/components/ui/panel";
import { Pill } from "@/components/ui/pill";
import { SetupStateBadge } from "@/components/ui/setup-state-badge";
import { CoinIcon, baseTicker } from "@/components/ui/coin-icon";
import { useInstruments, useSetupsAcrossInstruments } from "@/api/hooks";
import { mapApiState, toSetupSummary } from "@/api/mappers";
import type { ApiInstrument, ApiSetupState } from "@/api/types";
import type { SetupState, POIType } from "@/types/domain";
import { cn } from "@/lib/cn";

const COLS =
  "grid grid-cols-[1.5fr_0.9fr_0.9fr_0.5fr_0.9fr_0.9fr_0.5fr_1fr_0.5fr_0.3fr] items-center gap-3";

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 4 });

const POI_LABEL: Record<POIType, string> = { order_block: "Order block", qmr: "QMR" };

// Labels and values follow the backend's own 4 lifecycle states (spec
// section 15.2) directly -- "Forming" (the fixture-era label for a
// setup-lifecycle concept the backend plan-status enum never had) is
// renamed to "Detected" to match what the API actually returns; an
// "Invalidated" filter is added because the real data has plenty of it
// (the backend's own stop-breach invalidation work this session) and
// hiding it entirely would be worse than the original 4-button set
// silently returning nothing for a state users can actually filter to.
const FILTERS: { label: string; value: ApiSetupState | "all" }[] = [
  { label: "All", value: "all" },
  { label: "Detected", value: "detected" },
  { label: "Actionable", value: "actionable" },
  { label: "Triggered", value: "triggered" },
  { label: "Invalidated", value: "invalidated" },
];

function age(createdAt: string) {
  const ms = Date.now() - new Date(createdAt).getTime();
  const m = Math.max(1, Math.round(ms / 60000));
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  return h < 24 ? `${h}h` : `${Math.round(h / 24)}d`;
}

export function Setups() {
  const [filter, setFilter] = useState<ApiSetupState | "all">("all");
  const { data: instruments, loading: instrumentsLoading, error: instrumentsError } = useInstruments();
  const instrumentIds = useMemo(() => (instruments ?? []).map((i) => i.id), [instruments]);
  const {
    data: apiSetups,
    loading: setupsLoading,
    error: setupsError,
  } = useSetupsAcrossInstruments(instrumentIds, { state: filter === "all" ? undefined : filter });

  const instrumentById = useMemo(() => {
    const m = new Map<number, ApiInstrument>();
    for (const inst of instruments ?? []) m.set(inst.id, inst);
    return m;
  }, [instruments]);

  const rows = useMemo(() => {
    if (!apiSetups) return [];
    return apiSetups
      .map((s) => {
        const inst = instrumentById.get(s.instrument_id);
        if (!inst) return null;
        return { api: s, summary: toSetupSummary(s, inst) };
      })
      .filter((r): r is NonNullable<typeof r> => r !== null)
      .sort((a, b) => b.api.updated_at.localeCompare(a.api.updated_at));
  }, [apiSetups, instrumentById]);

  const loading = instrumentsLoading || setupsLoading;
  const error = instrumentsError ?? setupsError;

  return (
    <div className="mx-auto h-full max-w-[1200px] space-y-5 overflow-y-auto p-4 sm:p-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Setups</h1>
          <p className="mt-1 text-sm text-fg-muted">
            What ARGUS found across your watchlist.{" "}
            {loading ? "Loading…" : `${rows.length} finding${rows.length === 1 ? "" : "s"}.`}
          </p>
        </div>
        <div className="glass inline-flex items-center gap-1 rounded-full p-1">
          {FILTERS.map((f) => (
            <button
              key={f.value}
              onClick={() => setFilter(f.value)}
              className={cn(
                "focus-ring h-8 rounded-full px-4 text-[13px] font-medium transition-colors",
                f.value === filter
                  ? "bg-chrome-active text-chrome-active-foreground"
                  : "text-fg-muted hover:text-fg",
              )}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>

      <Panel variant="glass" pad="none" className="argus-in overflow-hidden">
        <div
          className={cn(
            COLS,
            "border-b border-line px-4 py-2.5 text-[11px] font-semibold uppercase tracking-wide text-fg-subtle",
          )}
        >
          <span>Instrument</span>
          <span>Direction</span>
          <span>POI</span>
          <span>TF</span>
          <span className="text-right">Entry</span>
          <span className="text-right">Stop</span>
          <span className="text-right">R:R</span>
          <span>State</span>
          <span className="text-right">Age</span>
          <span />
        </div>

        {error && (
          <div className="px-4 py-8 text-center text-[13px] text-down">
            Could not load setups: {error.message}
          </div>
        )}

        {!error && loading && (
          <div className="flex items-center justify-center gap-2 px-4 py-10 text-[13px] text-fg-subtle">
            <CircleNotch className="size-4 animate-spin" />
            Loading…
          </div>
        )}

        {!error && !loading && rows.length === 0 && (
          <div className="px-4 py-10 text-center text-[13px] text-fg-subtle">
            No setups yet. The worker may not have processed any candles.
          </div>
        )}

        {!error &&
          !loading &&
          rows.map(({ api, summary: s }) => {
            const bull = s.direction === "bullish";
            const state: SetupState = mapApiState(api.state);
            return (
              <Link
                key={s.id}
                to={`/analysis?setupId=${api.id}&instrumentId=${api.instrument_id}`}
                className={cn(
                  COLS,
                  "focus-ring border-b border-line/60 px-4 py-3 text-[13px] transition-colors last:border-0 hover:bg-white/[0.03]",
                )}
              >
                <span className="flex items-center gap-2.5">
                  <CoinIcon symbol={s.symbol} size={24} />
                  <span className="font-medium">{baseTicker(s.symbol).toUpperCase()}</span>
                </span>
                <span>
                  <Pill variant={bull ? "up" : "down"} dot>
                    {bull ? "Long" : "Short"}
                  </Pill>
                </span>
                <span className="text-fg-muted">{POI_LABEL[s.poiType]}</span>
                <span className="text-fg-muted">{s.timeframe}</span>
                <span className="tnum text-right">{n(s.entry)}</span>
                <span className="tnum text-right text-down">{n(s.stop)}</span>
                <span className="tnum text-right font-medium text-fg">{s.rr.toFixed(1)}</span>
                <span>
                  <SetupStateBadge state={state} />
                </span>
                <span className="tnum text-right text-fg-subtle">{age(s.createdAt)} ago</span>
                <CaretRight className="size-4 justify-self-end text-fg-subtle" />
              </Link>
            );
          })}
      </Panel>
    </div>
  );
}
