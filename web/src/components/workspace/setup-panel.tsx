import { useEffect, useRef } from "react";
import { CircleNotch, Play, Stop, Check, X, Question } from "@phosphor-icons/react";
import { Pill } from "@/components/ui/pill";
import { Stat } from "@/components/ui/stat";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { SetupStateBadge } from "@/components/ui/setup-state-badge";
import { mapApiPoiType, mapApiState, parseRatString, toEvidence } from "@/api/mappers";
import { formatRR } from "@/api/money";
import { cn } from "@/lib/cn";
import { STORY_STEPS, utcTime } from "@/components/chart/setup-layers";
import { useAnalysisWorkspace } from "./analysis-workspace";
import { biasSentence, invalidationWords, whySteps, type WhyStep } from "./why-setup";
import type { POIType } from "@/types/domain";
import type { ApiFoundMode } from "@/api/types";

const POI_LABEL: Record<POIType, string> = { order_block: "Order block", qmr: "QMR" };

// [U] display value: how long each beat of "Play the story" stays.
export const STORY_STEP_MS = 900;

// FoundModeChip shows how ARGUS found the setup: live (on the newest
// candle) or replay (catching up on older candles); old setups have none.
export function FoundModeChip({ mode, className }: { mode: ApiFoundMode | undefined; className?: string }) {
  const m = mode ?? "unknown";
  const text = m === "live" ? "Live" : m === "replay" ? "Replay" : "Found mode not recorded";
  const title =
    m === "live"
      ? "Found live: the candle that created it was the newest closed candle"
      : m === "replay"
        ? "Found in replay: the worker was catching up on older candles"
        : "Created before ARGUS recorded how setups were found";
  return (
    <span
      title={title}
      data-testid="found-mode"
      className={cn(
        "inline-flex rounded-full border px-2 py-0.5 text-[11px] font-medium",
        m === "live" ? "border-up/40 text-up" : m === "replay" ? "border-dashed border-line-strong text-fg-muted" : "border-dotted border-line text-fg-subtle",
        className,
      )}
    >
      {text}
    </span>
  );
}

const MARK: Record<WhyStep["result"], { icon: typeof Check; cls: string; label: string }> = {
  met: { icon: Check, cls: "text-up", label: "met" },
  not_met: { icon: X, cls: "text-down", label: "not met" },
  unevaluable: { icon: Question, cls: "text-fg-subtle", label: "could not be evaluated" },
};

function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

// WhyThisSetup (Phase 6.4): the bias in one sentence, then the recorded
// evidence steps in order with their marks and values, what would
// invalidate the setup, and its versions. Clicking a row highlights its
// element on the chart, and clicking an element highlights its row.
function WhyThisSetup() {
  const { setup, evidence, timeframe, focusedElement, setFocusedElement, storyStep, setStoryStep } = useAnalysisWorkspace();
  const rowRefs = useRef(new Map<string, HTMLLIElement>());
  const focusedRow = focusedElement?.replace(/:formation$/, ":zone") ?? null;

  useEffect(() => {
    if (!focusedRow) return;
    rowRefs.current.get(focusedRow)?.scrollIntoView?.({ block: "nearest" });
  }, [focusedRow]);

  // Play the story: one beat per STORY_STEP_MS, then the whole picture.
  useEffect(() => {
    if (storyStep === null) return;
    if (storyStep >= STORY_STEPS.length - 1) {
      const t = setTimeout(() => setStoryStep(null), STORY_STEP_MS);
      return () => clearTimeout(t);
    }
    const t = setTimeout(() => setStoryStep(storyStep + 1), prefersReducedMotion() ? 0 : STORY_STEP_MS);
    return () => clearTimeout(t);
  }, [storyStep, setStoryStep]);

  if (!setup) return null;
  if (evidence === undefined) {
    return <p className="text-[12px] text-fg-subtle">Loading the evidence…</p>;
  }
  if (!evidence.recorded) {
    return (
      <div data-testid="why-setup">
        <p className="text-[12px] text-fg-muted" data-testid="evidence-not-recorded">
          Evidence not recorded for this setup.
        </p>
        <p className="mt-1 text-[11px] text-fg-subtle">
          It was created before ARGUS recorded the evidence behind each decision. Nothing is reconstructed for it; its zone and
          plan are drawn from the stored plan only.
        </p>
      </div>
    );
  }
  const steps = whySteps(setup, evidence);
  const playing = storyStep !== null;
  return (
    <div data-testid="why-setup" className="space-y-3">
      <p className="text-[13px] leading-snug text-fg" data-testid="bias-sentence">
        {biasSentence(setup, evidence)}
      </p>
      <div className="flex items-center justify-between">
        <span className="text-[11px] text-fg-subtle">
          Found on {setup.timeframe}
          {timeframe !== setup.timeframe ? `, shown on this ${timeframe} chart` : ""}
        </span>
        <button
          type="button"
          data-testid="play-story"
          onClick={() => setStoryStep(playing ? null : 0)}
          className="focus-ring inline-flex items-center gap-1 rounded-md border border-line px-2 py-0.5 text-[11px] font-medium text-fg hover:bg-white/5"
        >
          {playing ? <Stop className="size-3" /> : <Play className="size-3" />}
          {playing ? `${STORY_STEPS[storyStep]}…` : "Play the story"}
        </button>
      </div>
      <ol className="space-y-1" data-testid="why-steps">
        {steps.map((st, i) => {
          const m = MARK[st.result];
          const Icon = m.icon;
          const active = st.elementId !== null && st.elementId === focusedRow;
          const lit = playing && st.storyStep <= (storyStep ?? 0);
          return (
            <li
              key={st.key}
              ref={(el) => {
                if (el && st.elementId) rowRefs.current.set(st.elementId, el);
              }}
              data-testid={`why-step-${i}`}
              data-active={active ? "true" : "false"}
              onClick={() => st.elementId && setFocusedElement(active ? null : st.elementId)}
              className={cn(
                "flex cursor-default gap-2 rounded-md px-2 py-1.5 transition-colors",
                st.elementId && "cursor-pointer hover:bg-white/[0.04]",
                active && "bg-accent/15 ring-1 ring-accent/50",
                playing && !lit && "opacity-40",
              )}
            >
              <Icon className={cn("mt-0.5 size-3.5 shrink-0", m.cls)} aria-label={m.label} weight="bold" />
              <div className="min-w-0">
                <div className="text-[12px] font-medium text-fg">
                  {st.title} <span className="text-[10px] font-normal text-fg-subtle">§{st.section}</span>
                </div>
                <div className="tnum text-[11px] leading-snug text-fg-muted">{st.detail}</div>
              </div>
            </li>
          );
        })}
      </ol>
      <div className="rounded-lg bg-white/[0.03] px-3 py-2 text-[11px] leading-snug text-fg-muted">
        <div className="mb-0.5 font-medium text-fg-subtle">What would invalidate it</div>
        <p data-testid="invalidation-words">{invalidationWords(setup)}</p>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[11px]" data-testid="why-versions">
        <dt className="text-fg-subtle">Strategy</dt>
        <dd>{evidence.strategy_version ?? setup.strategy_version}</dd>
        <dt className="text-fg-subtle">Algorithm</dt>
        <dd>{evidence.algorithm_version ?? setup.algorithm_version}</dd>
        <dt className="text-fg-subtle">Data</dt>
        <dd>
          formation candle #{setup.plan_snapshot.poi.order_block_candle_id} revision {setup.poi_source_candle_revision}
        </dd>
        <dt className="text-fg-subtle">Recorded</dt>
        <dd>
          with the {evidence.event_type ?? "?"} event{evidence.evaluation_time ? `, ${utcTime(evidence.evaluation_time)}` : ""} · evidence schema{" "}
          {evidence.schema ?? "?"}
        </dd>
      </dl>
    </div>
  );
}

export function SetupPanel() {
  const { setup, instrument, setupLoading, setupError, events, eventsLoading } = useAnalysisWorkspace();

  if (setupLoading) {
    return (
      <div className="flex items-center justify-center gap-2 py-8 text-[13px] text-fg-subtle">
        <CircleNotch className="size-4 animate-spin" />
        Loading plan…
      </div>
    );
  }
  if (setupError) {
    return <div className="py-8 text-center text-[13px] text-down">Could not load setup: {setupError.message}</div>;
  }
  if (!setup || !instrument) {
    return <div className="py-8 text-center text-[13px] text-fg-subtle">No setups for this instrument yet.</div>;
  }

  const bull = setup.direction === "bullish";
  const state = mapApiState(setup.state);
  const targets = setup.plan_snapshot.targets;
  // The furthest target's stored R:R (owner decision 3: shown as it is).
  const rr = targets.length > 0 ? parseRatString(targets[targets.length - 1].rr) : null;
  // The bar's split is the R:R itself (1 part risk, rr parts reward): no
  // arithmetic on prices.
  const rewardPct = rr !== null && rr > 0 ? (rr / (1 + rr)) * 100 : null;

  return (
    <div className="space-y-4">
      {/* State first (owner requirement f). */}
      <div className="space-y-1.5" data-testid="setup-state-line">
        <div className="flex items-center gap-1.5">
          <SetupStateBadge state={state} className="text-[12px]" />
          <FoundModeChip mode={setup.found_mode} />
          <span className="ml-auto text-[11px] text-fg-subtle">#{setup.id}</span>
        </div>
        {setup.state === "invalidated" && setup.invalidated_at && (
          <p className="text-[11px] text-down" data-testid="invalidated-at">
            Invalidated {utcTime(setup.invalidated_at)}: no longer live.
          </p>
        )}
        {setup.state === "triggered" && setup.triggered_at && (
          <p className="text-[11px] text-fg-subtle">Triggered {utcTime(setup.triggered_at)}: still active until a candle closes through the stop.</p>
        )}
      </div>

      <div className="flex items-center justify-between">
        <span className="text-sm font-semibold">The plan</span>
        <div className="flex items-center gap-1.5">
          <Pill variant={bull ? "up" : "down"} dot>
            {setup.direction}
          </Pill>
          <Pill variant="outline">
            {POI_LABEL[mapApiPoiType(setup.poi_type)]} · {setup.timeframe}
          </Pill>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-2">
        <Stat label="Entry" value={setup.plan_snapshot.entry} />
        <Stat label="Stop" value={setup.plan_snapshot.stop} tone="down" />
        <Stat label="R:R" value={rr !== null ? formatRR(rr) : "—"} />
      </div>

      {rewardPct !== null && (
        <div>
          <div className="flex h-2 overflow-hidden rounded-full" title="Risk against reward to the furthest target, from the stored R:R">
            <div className="bg-down/70" style={{ width: `${100 - rewardPct}%` }} />
            <div className="bg-up/80" style={{ width: `${rewardPct}%` }} />
          </div>
          <div className="mt-1 flex justify-between text-[10px] text-fg-subtle">
            <span>Risk 1R</span>
            <span>Reward {formatRR(rr!)}</span>
          </div>
        </div>
      )}

      <div>
        <div className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">Targets</div>
        <div className="mt-1.5 flex flex-wrap gap-1.5">
          {targets.map((t, i) => (
            <Pill key={`${t.level}-${i}`} variant="glass" size="md" className="gap-1.5">
              <span className="text-fg-subtle">TP{i + 1}</span>
              {t.level}
              <span className="text-fg">{formatRR(parseRatString(t.rr))}</span>
            </Pill>
          ))}
          {targets.length === 0 && <span className="text-[12px] text-fg-subtle">No structural target beyond entry.</span>}
        </div>
      </div>

      <div className="border-t border-line pt-3">
        <div className="mb-2 text-sm font-semibold">Why this setup</div>
        <WhyThisSetup />
      </div>

      <div className="border-t border-line pt-3">
        <div className="mb-2 flex items-center justify-between">
          <span className="text-sm font-semibold">History</span>
          <span className="text-xs text-fg-subtle">
            {eventsLoading ? "Loading…" : `${events.length} event${events.length === 1 ? "" : "s"}`}
          </span>
        </div>
        {!eventsLoading && events.length === 0 && <p className="text-[12px] text-fg-subtle">No events recorded for this setup yet.</p>}
        <ul className="space-y-3">
          {events.map((e) => {
            const item = toEvidence(e);
            return (
              <li key={e.id} className="flex gap-2.5">
                <ProvenanceChip p={item.provenance} className="mt-0.5" />
                <div className="min-w-0">
                  <div className="text-[13px] font-medium">{item.label}</div>
                  <div className="text-[12px] leading-snug text-fg-muted">{item.detail}</div>
                </div>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
