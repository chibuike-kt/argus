// setup-layers.ts: what ARGUS draws on the chart for its setups (Phase 6.3).
// Pure: every element comes from a setup's stored fields and its recorded
// evidence (GET /setups?include_evidence=true), nothing is computed from
// prices beyond turning the server's decimal strings into canvas numbers.
// The same setups and candles always give the same elements (ADR-019).
//
// Rules (owner decisions for Phase 6, recorded in the brief hand-off):
//  1. A zone starts at its formation candle and ends at the setup's
//     invalidation, or at the newest candle while the setup is still
//     active. "Triggered" (spec section 14: a finalized candle after the
//     POI formed reached the entry) does not end it; the setup carries on.
//  2. Bearish setups say "premium", bullish say "discount".
//  3. R:R is the stored plan's, shown as it is, however small.
//  4. Only each setup's own evidence is drawn, never all stored structure.
// Every setup ARGUS found is drawn as its zone; the selected one also gets
// its evidence (swings, sweep, break, range, structural liquidity, the
// formation candle and the plan). Old setups without recorded evidence get
// their zone and plan only, and say so.
import type { ApiEvidenceLevel, ApiEvidenceStep, ApiFoundMode, ApiSetup, ApiSetupState } from "@/api/types";
import { formatRR, parseRatString } from "@/api/money";
import type { Drawing } from "./drawings/types";

export type ElementKind =
  | "zone"
  | "formation"
  | "swing"
  | "sweep"
  | "break"
  | "range"
  | "structural"
  | "plan";

// Geometry in chart space: unix seconds and price numbers (canvas only).
export type Geometry =
  | { type: "rect"; t1: number; t2: number; low: number; high: number }
  | { type: "segment"; t1: number; p1: number; t2: number; p2: number }
  | { type: "marker"; t: number; p: number; above: boolean }
  | { type: "range"; t1: number; t2: number; low: number; high: number; eq: number; side: "premium" | "discount" }
  | {
      type: "plan";
      t1: number;
      t2: number;
      entry: number;
      stop: number;
      // Pill text for the right gutter ("Entry 70.0250", "TP1 70.0240 · 0.006R"),
      // the server's exact strings.
      entryText: string;
      stopText: string;
      targets: { price: number; label: string }[];
    };

export interface PopoverInfo {
  title: string;
  // What it is and why, in plain words, templated from recorded fields.
  reason: string;
  // Label and value pairs (values are the server's strings).
  rows: [string, string][];
  // The candles it came from, "role: time UTC".
  candles: string[];
}

export interface LayerElement {
  id: string;
  setupId: number;
  kind: ElementKind;
  // The short on-chart label ("BOS", "Sweep", "1m bear OB"); the full
  // explanation is the popover's.
  label: string;
  geometry: Geometry;
  // Invalidated setups are drawn dimmed (only when "show invalidated" is on,
  // or the setup is the selected one).
  dimmed: boolean;
  selected: boolean;
  foundMode: ApiFoundMode;
  direction: "bullish" | "bearish";
  // The zone of a setup found on another timeframe than the chart's.
  foreignTimeframe: boolean;
  // Index in the story (sweep, break, POI, plan) for "Play the story";
  // -1 for elements that are always shown (other setups' zones).
  storyStep: number;
  popover: PopoverInfo;
  // The editable drawing "Copy to my drawings" creates. The original is
  // never changed: a setup is an immutable decision (ADR-011).
  copy: DrawingSpec;
}

// DrawingSpec is a Drawing without its identity (distributes over the union).
export type DrawingSpec = Drawing extends infer D ? (D extends Drawing ? Omit<D, "id" | "createdAt" | "updatedAt"> : never) : never;

export const SETUP_STATES: readonly ApiSetupState[] = ["detected", "actionable", "triggered", "invalidated"];
export const LAYER_TIMEFRAMES = ["1m", "5m", "15m", "1h", "4h", "1d"] as const;

export interface LayerFilters {
  // Setups in these states are drawn. invalidated is "show invalidated":
  // off by default, and drawn dimmed when on.
  states: Record<ApiSetupState, boolean>;
  // Setups found on these timeframes are drawn.
  timeframes: Record<string, boolean>;
  // The selected setup's evidence is drawn.
  evidence: boolean;
}

export const DEFAULT_FILTERS: LayerFilters = {
  states: { detected: true, actionable: true, triggered: true, invalidated: false },
  timeframes: Object.fromEntries(LAYER_TIMEFRAMES.map((t) => [t, true])),
  evidence: true,
};

// filtersFromPrefs reads the saved setup_layers map (state_<s>, tf_<t>,
// evidence) over the defaults.
export function filtersFromPrefs(saved: Record<string, boolean> | undefined): LayerFilters {
  const f: LayerFilters = {
    states: { ...DEFAULT_FILTERS.states },
    timeframes: { ...DEFAULT_FILTERS.timeframes },
    evidence: DEFAULT_FILTERS.evidence,
  };
  if (!saved) return f;
  for (const s of SETUP_STATES) if (typeof saved[`state_${s}`] === "boolean") f.states[s] = saved[`state_${s}`];
  for (const t of LAYER_TIMEFRAMES) if (typeof saved[`tf_${t}`] === "boolean") f.timeframes[t] = saved[`tf_${t}`];
  if (typeof saved.evidence === "boolean") f.evidence = saved.evidence;
  return f;
}

// setupVisible says whether a setup passes the filters. The selected setup
// is always drawn (the trader chose it), dimmed when invalidated.
export function setupVisible(s: ApiSetup, f: LayerFilters, selectedId: number | undefined): boolean {
  if (s.id === selectedId) return true;
  return f.states[s.state] === true && f.timeframes[s.timeframe] !== false;
}

const POI_WORDS: Record<string, string> = { order_block: "order block", qmr: "QMR zone" };
// shortPoi is the chart label's abbreviation: OB or QMR.
export function shortPoi(t: string): string {
  return t === "qmr" ? "QMR" : t === "order_block" ? "OB" : t.replace(/_/g, " ");
}

export function poiWords(t: string): string {
  return POI_WORDS[t] ?? t.replace(/_/g, " ");
}

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);
const sec = (iso: string) => Math.floor(Date.parse(iso) / 1000);
const num = (s: string) => Number(s);

// utcTime formats an instant as "2026-11-21 23:49 UTC".
export function utcTime(iso: string | number): string {
  const d = typeof iso === "number" ? new Date(iso * 1000) : new Date(iso);
  return d.toISOString().replace("T", " ").slice(0, 16) + " UTC";
}

export const TIMEFRAME_SECONDS: Record<string, number> = { "1m": 60, "5m": 300, "15m": 900, "1h": 3600, "4h": 14400, "1d": 86400 };

export function foundWords(m: ApiFoundMode | undefined): string {
  switch (m) {
    case "live":
      return "live (on the newest candle)";
    case "replay":
      return "replay (while catching up on older candles)";
    default:
      return "not recorded (found before this was stored)";
  }
}

export function stateWords(s: ApiSetupState): string {
  switch (s) {
    case "detected":
      return "detected";
    case "actionable":
      return "actionable (price has not reached the entry)";
    case "triggered":
      return "triggered (a candle reached the entry; still active)";
    case "invalidated":
      return "invalidated (a candle closed through the stop)";
  }
}

// zoneSpan is rule 1: formation candle to invalidation, or to the newest
// candle while active. lastCandleSec is the chart's newest bar open time.
export function zoneSpan(s: ApiSetup, lastCandleSec: number): { start: number; end: number; ended: boolean } {
  const start = sec(s.plan_snapshot.poi.order_block_open_time);
  if (s.invalidated_at) return { start, end: Math.max(sec(s.invalidated_at), start), ended: true };
  return { start, end: Math.max(lastCandleSec, start), ended: false };
}

function level(step: ApiEvidenceStep | undefined, name: string): ApiEvidenceLevel | undefined {
  return step?.levels?.find((l) => l.name === name);
}

function candleTime(step: ApiEvidenceStep | undefined, role: string): string | undefined {
  return step?.candles?.find((c) => c.role === role)?.open_time ?? undefined;
}

function find(steps: ApiEvidenceStep[], kind: string, role?: string): ApiEvidenceStep | undefined {
  return steps.find((s) => s.kind === kind && (role === undefined || s.role === role));
}

function baseRows(s: ApiSetup): [string, string][] {
  return [
    ["Setup", `#${s.id}`],
    ["Timeframe", s.timeframe],
    ["State", stateWords(s.state)],
    ["Found", foundWords(s.found_mode)],
  ];
}

// targetsOf is the plan's targets with their stored R:R (rule 3).
function targetsOf(s: ApiSetup): { price: number; text: string; label: string; rr: string }[] {
  return s.plan_snapshot.targets.map((t, i) => {
    const rr = formatRR(parseRatString(t.rr));
    return { price: num(t.level), text: t.level, label: `TP${i + 1} ${t.level} · ${rr}`, rr };
  });
}

// buildLayers returns every element to draw, in paint order: other setups'
// zones first, then the selected setup's evidence and zone on top.
export function buildLayers(
  setups: readonly ApiSetup[],
  opts: { chartTimeframe: string; lastCandleSec: number; selectedId?: number; filters: LayerFilters },
): LayerElement[] {
  const out: LayerElement[] = [];
  const ordered = [...setups].sort((a, b) => (a.id === opts.selectedId ? 1 : b.id === opts.selectedId ? -1 : a.id - b.id));
  for (const s of ordered) {
    if (!setupVisible(s, opts.filters, opts.selectedId)) continue;
    const selected = s.id === opts.selectedId;
    const common = {
      setupId: s.id,
      dimmed: s.state === "invalidated",
      selected,
      foundMode: (s.found_mode ?? "unknown") as ApiFoundMode,
      direction: (s.direction === "bearish" ? "bearish" : "bullish") as "bullish" | "bearish",
      foreignTimeframe: s.timeframe !== opts.chartTimeframe,
    };
    const span = zoneSpan(s, opts.lastCandleSec);
    const steps = s.evidence?.recorded ? (s.evidence.steps ?? []) : [];
    const dir = cap(common.direction);
    const poi = s.plan_snapshot.poi;
    const words = poiWords(s.poi_type);
    const endWords = span.ended
      ? `ends ${utcTime(span.end)}, when the setup was invalidated`
      : `still active, drawn to the newest candle`;
    const evidenceNote = s.evidence?.recorded ? "" : " Evidence not recorded for this setup.";

    // The zone (every setup).
    out.push({
      ...common,
      id: `${s.id}:zone`,
      kind: "zone",
      label: selected
        ? `${dir === "Bearish" ? "Bear" : "Bull"} ${shortPoi(s.poi_type)} · ${s.timeframe}`
        : `${s.timeframe} ${common.direction === "bearish" ? "bear" : "bull"} ${shortPoi(s.poi_type)}`,
      geometry: { type: "rect", t1: span.start, t2: span.end, low: num(poi.low), high: num(poi.high) },
      storyStep: selected ? 6 : -1,
      popover: {
        title: `${dir} ${words} · ${s.timeframe}`,
        reason:
          `The ${words} ARGUS chose as the point of interest (spec sections 12 and 13), from the ${utcTime(poi.order_block_open_time)} candle; ${endWords}.` +
          evidenceNote,
        rows: [...baseRows(s), ["Zone high", poi.high], ["Zone low", poi.low]],
        candles: [`formation: ${utcTime(poi.order_block_open_time)}`],
      },
      copy: {
        kind: "rectangle",
        p1: { time: span.start as never, price: num(poi.high) },
        p2: { time: span.end as never, price: num(poi.low) },
        label: `${dir} ${words} (copy of setup #${s.id})`,
        style: { color: "#6d7bff", lineWidth: 1, lineStyle: "solid" },
      },
    });

    // The plan (every setup with targets or not; drawn for the selected one).
    if (selected) {
      const targets = targetsOf(s);
      const planFrom = s.detected_at ? sec(s.detected_at) : span.start;
      out.push({
        ...common,
        id: `${s.id}:plan`,
        kind: "plan",
        label: "Plan",
        geometry: {
          type: "plan",
          t1: Math.min(planFrom, span.end),
          t2: span.end,
          entry: num(s.plan_snapshot.entry),
          stop: num(s.plan_snapshot.stop),
          entryText: `Entry ${s.plan_snapshot.entry}`,
          stopText: `Stop ${s.plan_snapshot.stop}`,
          targets: targets.map((t) => ({ price: t.price, label: t.label })),
        },
        storyStep: 7,
        popover: {
          title: `Plan · setup #${s.id}`,
          reason:
            `Entry at the zone's near edge, stop at the protected ${common.direction === "bullish" ? "low" : "high"}, targets at the nearest opposing swings beyond the entry (spec section 14). ` +
            `R:R is the stored plan's, information only (ADR-012).`,
          rows: [
            ...baseRows(s),
            ["Entry", s.plan_snapshot.entry],
            ["Stop", s.plan_snapshot.stop],
            ...targets.map((t, i): [string, string] => [`Target ${i + 1}`, `${t.text} (${t.rr})`]),
            ...(targets.length === 0 ? [["Targets", "none beyond the entry"] as [string, string]] : []),
          ],
          candles: s.detected_at ? [`detected: ${utcTime(s.detected_at)}`] : [],
        },
        copy: {
          kind: common.direction === "bullish" ? "position-long" : "position-short",
          time: Math.min(planFrom, span.end) as never,
          rightTime: Math.max(span.end, planFrom + 60) as never,
          entry: num(s.plan_snapshot.entry),
          stop: num(s.plan_snapshot.stop),
          targets: targets.map((t) => t.price),
          style: { color: "#6d7bff", lineWidth: 1, lineStyle: "solid" },
        },
      });
    }

    if (!selected || !opts.filters.evidence || steps.length === 0) continue;
    const tfSec = TIMEFRAME_SECONDS[s.timeframe] ?? 60;
    const asOf = s.evidence?.as_of ? sec(s.evidence.as_of) : span.end;

    // Formation candle.
    const formationTime = candleTime(find(steps, "poi"), "formation") ?? poi.order_block_open_time;
    out.push({
      ...common,
      id: `${s.id}:formation`,
      kind: "formation",
      label: `${shortPoi(s.poi_type)} candle`,
      geometry: { type: "rect", t1: sec(formationTime), t2: sec(formationTime) + tfSec, low: num(poi.low), high: num(poi.high) },
      storyStep: 6,
      popover: {
        title: `Formation candle · setup #${s.id}`,
        reason: `The ${s.timeframe} candle the ${words} was taken from (spec section 12); the zone spans its range.`,
        rows: [...baseRows(s), ["Zone high", poi.high], ["Zone low", poi.low]],
        candles: [`formation: ${utcTime(formationTime)}`],
      },
      copy: {
        kind: "rectangle",
        p1: { time: sec(formationTime) as never, price: num(poi.high) },
        p2: { time: (sec(formationTime) + tfSec) as never, price: num(poi.low) },
        label: `Formation candle (copy of setup #${s.id})`,
        style: { color: "#6d7bff", lineWidth: 1, lineStyle: "solid" },
      },
    });

    // Swept swing and the sweep.
    const swept = find(steps, "swing", "swept");
    const sweep = find(steps, "sweep");
    const sweptLvl = level(swept, "swing");
    if (sweptLvl?.time) {
      const high = swept?.attrs?.swing_type === "high";
      out.push(swingElement(s, common, `${s.id}:swing:swept`, `Swing ${high ? "high" : "low"}`, sweptLvl, high, 0,
        `A confirmed swing ${high ? "high" : "low"} (spec section 1) whose liquidity the sweep took.`));
    }
    if (sweep && sweep.result === "met" && sweptLvl?.time) {
      const trig = candleTime(sweep, "trigger");
      const ext = level(sweep, "penetration_extreme");
      const rec = level(sweep, "reclaim_close");
      const side = sweep.attrs?.side === "buy" ? "Buy-side" : "Sell-side";
      if (trig) {
        out.push({
          ...common,
          id: `${s.id}:sweep`,
          kind: "sweep",
          label: "Sweep",
          geometry: { type: "segment", t1: sec(sweptLvl.time), p1: num(sweptLvl.price), t2: sec(trig), p2: num(sweptLvl.price) },
          storyStep: 1,
          popover: {
            title: `${side} sweep · setup #${s.id}`,
            reason:
              `On the ${utcTime(trig)} candle price traded ${side === "Buy-side" ? "above" : "below"} the swing at ${sweptLvl.price}` +
              (ext ? ` (to ${ext.price})` : "") +
              (rec ? ` and closed back ${side === "Buy-side" ? "below" : "above"} it at ${rec.price}` : "") +
              `: the ${side.toLowerCase()} liquidity was taken (spec section 6).`,
            rows: [...baseRows(s), ["Swept level", sweptLvl.price], ...(ext ? [["Extreme", ext.price] as [string, string]] : []), ...(rec ? [["Reclaim close", rec.price] as [string, string]] : [])],
            candles: [`swing: ${utcTime(sweptLvl.time)}`, `sweep: ${utcTime(trig)}`],
          },
          copy: {
            kind: "trendline",
            p1: { time: sec(sweptLvl.time) as never, price: num(sweptLvl.price) },
            p2: { time: sec(trig) as never, price: num(sweptLvl.price) },
            style: { color: "#6d7bff", lineWidth: 1, lineStyle: "dashed" },
          },
        });
      }
    }

    // Protected swing.
    const prot = find(steps, "swing", "protected");
    const protLvl = level(prot, "swing");
    if (protLvl?.time) {
      const high = prot?.attrs?.swing_type === "high";
      out.push(swingElement(s, common, `${s.id}:swing:protected`, `Protected ${high ? "high" : "low"}`, protLvl, high, 2,
        `The structure's protected ${high ? "high" : "low"} (spec section 7): the stop sits here, and a candle closing beyond it invalidates the setup.`));
    }

    // The break.
    const brk = find(steps, "break");
    const broken = level(brk, "broken_swing");
    const brkTime = candleTime(brk, "trigger");
    if (brk && broken?.time && brkTime) {
      const type = brk.attrs?.break_type === "choch" ? "CHoCH" : "BOS";
      out.push({
        ...common,
        id: `${s.id}:break`,
        kind: "break",
        label: type,
        geometry: { type: "segment", t1: sec(broken.time), p1: num(broken.price), t2: sec(brkTime), p2: num(broken.price) },
        storyStep: 3,
        popover: {
          title: `${type} · setup #${s.id}`,
          reason:
            `The ${utcTime(brkTime)} candle closed through the swing at ${broken.price} from ${utcTime(broken.time)}: a ${type === "BOS" ? "break of structure" : "change of character"} ` +
            `(${brk.attrs?.direction_before ?? "?"} to ${brk.attrs?.direction_after ?? "?"}), validated by the sweep (spec sections 3 and 7). This is the break the order block was anchored to.`,
          rows: [...baseRows(s), ["Broken level", broken.price], ["Structure event", `#${brk.attrs?.structure_event ?? "?"}`]],
          candles: [`broken swing: ${utcTime(broken.time)}`, `break: ${utcTime(brkTime)}`],
        },
        copy: {
          kind: "trendline",
          p1: { time: sec(broken.time) as never, price: num(broken.price) },
          p2: { time: sec(brkTime) as never, price: num(broken.price) },
          style: { color: "#6d7bff", lineWidth: 1, lineStyle: "solid" },
        },
      });
    }

    // Premium or discount of the active range (rule 2: the side named by
    // direction, never the raw in_discount flag).
    const pd = find(steps, "premium_discount");
    const lo = level(pd, "range_low");
    const hi = level(pd, "range_high");
    const eq = level(pd, "equilibrium");
    if (pd && pd.result !== "unevaluable" && lo?.time && hi?.time && eq) {
      const side = common.direction === "bearish" ? "premium" : "discount";
      const t1 = Math.min(sec(lo.time), sec(hi.time));
      out.push({
        ...common,
        id: `${s.id}:range`,
        kind: "range",
        label: cap(side),
        geometry: { type: "range", t1, t2: Math.max(asOf, t1 + tfSec), low: num(lo.price), high: num(hi.price), eq: num(eq.price), side },
        storyStep: 4,
        popover: {
          title: `${cap(side)} of the active range · setup #${s.id}`,
          reason:
            `The active range runs from ${lo.price} to ${hi.price}, equilibrium ${eq.price} (spec section 8). ` +
            `A ${common.direction} setup needs its zone in ${side}: ${pd.result === "met" ? "it was" : "it was not"}. Drawn as the range stood at the decision (${utcTime(asOf)}).`,
          rows: [...baseRows(s), ["Range high", hi.price], ["Equilibrium", eq.price], ["Range low", lo.price]],
          candles: [`range low swing: ${utcTime(lo.time)}`, `range high swing: ${utcTime(hi.time)}`],
        },
        copy: {
          kind: "fib",
          p1: { time: t1 as never, price: num(lo.price) },
          p2: { time: Math.max(asOf, t1 + tfSec) as never, price: num(hi.price) },
          style: { color: "#6d7bff", lineWidth: 1, lineStyle: "solid" },
        },
      });
    }

    // Structural liquidity.
    const sl = find(steps, "structural_liquidity");
    const slLvl = level(sl, "structural_swing");
    if (sl && slLvl?.time) {
      out.push({
        ...common,
        id: `${s.id}:structural`,
        kind: "structural",
        label: "Struct. liq.",
        geometry: { type: "segment", t1: sec(slLvl.time), p1: num(slLvl.price), t2: Math.max(asOf, sec(slLvl.time) + tfSec), p2: num(slLvl.price) },
        storyStep: 5,
        popover: {
          title: `Structural liquidity · setup #${s.id}`,
          reason: `The swing at ${slLvl.price} from ${utcTime(slLvl.time)} is the structural liquidity backing the zone (spec section 9). Drawn to the decision (${utcTime(asOf)}).`,
          rows: [...baseRows(s), ["Level", slLvl.price]],
          candles: [`swing: ${utcTime(slLvl.time)}`],
        },
        copy: {
          kind: "trendline",
          p1: { time: sec(slLvl.time) as never, price: num(slLvl.price) },
          p2: { time: Math.max(asOf, sec(slLvl.time) + tfSec) as never, price: num(slLvl.price) },
          style: { color: "#6d7bff", lineWidth: 1, lineStyle: "dashed" },
        },
      });
    }
  }
  return out;
}

type Common = Pick<LayerElement, "setupId" | "dimmed" | "selected" | "foundMode" | "direction" | "foreignTimeframe">;

function swingElement(s: ApiSetup, common: Common, id: string, label: string, lvl: ApiEvidenceLevel, high: boolean, story: number, reason: string): LayerElement {
  const t = sec(lvl.time!);
  return {
    ...common,
    id,
    kind: "swing",
    label,
    geometry: { type: "marker", t, p: num(lvl.price), above: high },
    storyStep: story,
    popover: {
      title: `${label}${id.endsWith(":swept") ? " (swept)" : ""} · setup #${s.id}`,
      reason: `${reason} Price ${lvl.price}, pivot candle ${utcTime(lvl.time!)}.`,
      rows: [...baseRows(s), ["Price", lvl.price], ...(lvl.swing_id ? [["Swing", `#${lvl.swing_id}`] as [string, string]] : [])],
      candles: [`pivot: ${utcTime(lvl.time!)}`],
    },
    copy: { kind: "text", p: { time: t as never, price: num(lvl.price) }, text: label, style: { color: "#6d7bff", lineWidth: 1, lineStyle: "solid" } },
  };
}

// STORY_STEPS are the beats of "Play the story", in order.
export const STORY_STEPS = ["Swept swing", "Sweep", "Protected swing", "Break", "Range", "Structural liquidity", "Zone", "Plan"] as const;
