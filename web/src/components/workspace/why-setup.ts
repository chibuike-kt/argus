// why-setup.ts: the "Why this setup" panel's text (Phase 6.4), templated
// only from recorded fields -- the evidence chain and the stored plan. No
// generated prose: every sentence is a fixed template with recorded values.
import type { ApiEvidenceStep, ApiSetup, ApiSetupEvidence } from "@/api/types";
import { formatRR, parseRatString } from "@/api/money";
import { poiWords, utcTime } from "@/components/chart/setup-layers";

export interface WhyStep {
  key: string;
  // The chart element this row highlights (setup-layers.ts ids); null for
  // a step with nothing drawn (the QMR check).
  elementId: string | null;
  title: string;
  detail: string;
  result: ApiEvidenceStep["result"];
  section: string;
  // "Play the story" step this row belongs to.
  storyStep: number;
}

const lvl = (s: ApiEvidenceStep | undefined, name: string) => s?.levels?.find((l) => l.name === name);
const cand = (s: ApiEvidenceStep | undefined, role: string) => s?.candles?.find((c) => c.role === role)?.open_time ?? undefined;
const words = (code: string) => code.replace(/_/g, " ");

function stepFor(setup: ApiSetup, s: ApiEvidenceStep, i: number): WhyStep {
  const id = setup.id;
  const base = { key: `${i}:${s.kind}:${s.role ?? ""}`, result: s.result, section: s.section };
  const reason = s.reason ? ` (${words(s.reason)})` : "";
  switch (s.kind) {
    case "swing": {
      const l = lvl(s, "swing");
      const high = s.attrs?.swing_type === "high";
      const protectedSwing = s.role === "protected";
      return {
        ...base,
        elementId: `${id}:swing:${s.role ?? "swept"}`,
        title: protectedSwing ? `Protected ${high ? "high" : "low"}` : `Swing ${high ? "high" : "low"} (swept)`,
        detail: l ? `${l.price} · pivot ${utcTime(l.time ?? "")}` : `not recorded${reason}`,
        storyStep: protectedSwing ? 2 : 0,
      };
    }
    case "sweep": {
      const swept = lvl(s, "swept_level");
      const ext = lvl(s, "penetration_extreme");
      const rec = lvl(s, "reclaim_close");
      const t = cand(s, "trigger");
      const side = s.attrs?.side === "buy" ? "Buy-side" : "Sell-side";
      return {
        ...base,
        elementId: `${id}:sweep`,
        title: s.result === "unevaluable" ? "Sweep" : `${side} sweep`,
        detail:
          s.result === "unevaluable"
            ? `could not be evaluated${reason}`
            : `through ${swept?.price ?? "?"}${ext ? ` to ${ext.price}` : ""}${rec ? `, closed back at ${rec.price}` : ""}${t ? ` · ${utcTime(t)}` : ""}`,
        storyStep: 1,
      };
    }
    case "break": {
      const b = lvl(s, "broken_swing");
      const t = cand(s, "trigger");
      const type = s.attrs?.break_type === "choch" ? "CHoCH" : "BOS";
      return {
        ...base,
        elementId: `${id}:break`,
        title: `${type} (${s.attrs?.direction_before ?? "?"} to ${s.attrs?.direction_after ?? "?"})`,
        detail: `through ${b?.price ?? "?"}${b?.time ? ` (swing of ${utcTime(b.time)})` : ""}${t ? ` · ${utcTime(t)}` : ""}`,
        storyStep: 3,
      };
    }
    case "premium_discount": {
      // Owner decision 2: the side is named by direction.
      const side = setup.direction === "bearish" ? "premium" : "discount";
      const lo = lvl(s, "range_low");
      const hi = lvl(s, "range_high");
      const eq = lvl(s, "equilibrium");
      return {
        ...base,
        elementId: s.result === "unevaluable" ? null : `${id}:range`,
        title: `Zone in ${side}`,
        detail:
          s.result === "unevaluable"
            ? `no active range${reason}`
            : `range ${lo?.price ?? "?"} to ${hi?.price ?? "?"}, equilibrium ${eq?.price ?? "?"}`,
        storyStep: 4,
      };
    }
    case "structural_liquidity": {
      const l = lvl(s, "structural_swing");
      return {
        ...base,
        elementId: `${id}:structural`,
        title: "Structural liquidity",
        detail: l ? `${l.price} · swing of ${utcTime(l.time ?? "")}` : `not recorded${reason}`,
        storyStep: 5,
      };
    }
    case "qmr":
      return {
        ...base,
        elementId: null,
        title: "QMR check",
        detail: s.result === "met" ? "the order block failed: the zone is a QMR" : `${s.result === "not_met" ? "not met" : "not evaluated"}${reason}`,
        storyStep: 5,
      };
    case "poi": {
      const lo = lvl(s, "zone_low");
      const hi = lvl(s, "zone_high");
      const t = cand(s, "formation");
      return {
        ...base,
        elementId: `${id}:zone`,
        title: `${poiWords(s.attrs?.poi_type ?? setup.poi_type).replace(/^./, (c) => c.toUpperCase())}`,
        detail: `${lo?.price ?? "?"} to ${hi?.price ?? "?"}${t ? ` · formation candle ${utcTime(t)}` : ""}`,
        storyStep: 6,
      };
    }
    case "plan": {
      const p = setup.plan_snapshot;
      const tps = p.targets.map((t, k) => `TP${k + 1} ${t.level} (${formatRR(parseRatString(t.rr))})`).join(", ");
      return {
        ...base,
        elementId: `${id}:plan`,
        title: "Plan",
        detail: `entry ${p.entry}, stop ${p.stop}${tps ? `, ${tps}` : ", no target beyond the entry"}`,
        storyStep: 7,
      };
    }
  }
}

export function whySteps(setup: ApiSetup, ev: ApiSetupEvidence | undefined): WhyStep[] {
  if (!ev?.recorded) return [];
  return (ev.steps ?? []).map((s, i) => stepFor(setup, s, i));
}

// biasSentence is the bias in one templated sentence from the recorded
// sweep, break and zone; parts that were not recorded are left out.
export function biasSentence(setup: ApiSetup, ev: ApiSetupEvidence | undefined): string {
  const dir = setup.direction === "bearish" ? "Bearish" : "Bullish";
  const side = setup.direction === "bearish" ? "premium" : "discount";
  if (!ev?.recorded) return `${dir} ${poiWords(setup.poi_type)} on ${setup.timeframe}. Evidence not recorded for this setup.`;
  const steps = ev.steps ?? [];
  const sweep = steps.find((s) => s.kind === "sweep" && s.result === "met");
  const brk = steps.find((s) => s.kind === "break");
  const parts: string[] = [];
  if (sweep) {
    const l = lvl(sweep, "swept_level");
    const t = cand(sweep, "trigger");
    const buy = sweep.attrs?.side === "buy";
    parts.push(`${buy ? "buy-side" : "sell-side"} liquidity ${buy ? "above" : "below"} ${l?.price ?? "?"} was swept${t ? ` at ${utcTime(t)}` : ""}`);
  }
  if (brk) {
    const b = lvl(brk, "broken_swing");
    const t = cand(brk, "trigger");
    parts.push(`then a ${brk.attrs?.break_type === "choch" ? "CHoCH" : "BOS"} closed through ${b?.price ?? "?"}${t ? ` at ${utcTime(t)}` : ""}`);
  }
  const pd = steps.find((s) => s.kind === "premium_discount");
  if (pd && pd.result !== "unevaluable") parts.push(`the ${poiWords(setup.poi_type)} sits in ${side}`);
  return `${dir}: ${parts.join(", ")}.`;
}

// invalidationWords says what would invalidate the setup (spec section
// 15.2: a finalized candle closing strictly through its own stop).
export function invalidationWords(setup: ApiSetup): string {
  const bear = setup.direction === "bearish";
  return `A finalized ${setup.timeframe} candle closing ${bear ? "above" : "below"} ${setup.plan_snapshot.stop}, the protected ${bear ? "high" : "low"} and the stop (spec sections 14 and 15.2).`;
}
