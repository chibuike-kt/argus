// A semicircle bias gauge, TradingView's "Strong Sell -> Strong Buy" pattern
// recast in ARGUS's own terms (bearish -> bullish). The needle and its active
// arc render in the intelligence accent, not the market's up/down colours --
// this is ARGUS's own read on structure, the same voice as its plan and POI
// marks, not a raw price tick. Purely presentational: it derives its needle
// position from the setup's existing direction + rr (no new data field), so
// it reads a stronger offered R:R as a more convicted bias.
function polarPoint(cx: number, cy: number, r: number, angleDeg: number) {
  const a = (angleDeg * Math.PI) / 180;
  return { x: cx + r * Math.cos(a), y: cy - r * Math.sin(a) };
}

function describeArc(cx: number, cy: number, r: number, startAngle: number, endAngle: number) {
  const start = polarPoint(cx, cy, r, startAngle);
  const end = polarPoint(cx, cy, r, endAngle);
  const sweepFlag = endAngle < startAngle ? 1 : 0;
  return `M ${start.x} ${start.y} A ${r} ${r} 0 0 ${sweepFlag} ${end.x} ${end.y}`;
}

const CX = 100;
const CY = 82;
const R = 70;

export function BiasGauge({ direction, rr }: { direction: "bullish" | "bearish"; rr: number }) {
  // magnitude: how far the offered R:R sits above a bare 1R, clamped to a
  // [0,1] "conviction" scale -- 1R -> 0, 3R+ -> 1.
  const magnitude = Math.max(0, Math.min(1, (rr - 1) / 2));
  const t = direction === "bullish" ? 0.5 + magnitude * 0.5 : 0.5 - magnitude * 0.5;
  const needleAngle = 180 - t * 180;
  const tip = polarPoint(CX, CY, R - 10, needleAngle);

  const label =
    magnitude > 0.7
      ? `Strong ${direction === "bullish" ? "bullish" : "bearish"}`
      : direction === "bullish"
        ? "Bullish"
        : "Bearish";

  return (
    <div className="flex flex-col items-center">
      <svg viewBox="0 0 200 100" className="w-full max-w-[220px]">
        <path
          d={describeArc(CX, CY, R, 180, 0)}
          fill="none"
          stroke="var(--color-line-strong)"
          strokeWidth={10}
          strokeLinecap="round"
        />
        <path
          d={describeArc(CX, CY, R, 90, needleAngle)}
          fill="none"
          stroke="var(--color-accent)"
          strokeWidth={10}
          strokeLinecap="round"
        />
        <line
          x1={CX}
          y1={CY}
          x2={tip.x}
          y2={tip.y}
          stroke="var(--color-accent)"
          strokeWidth={2.5}
          strokeLinecap="round"
        />
        <circle cx={CX} cy={CY} r={5} fill="var(--color-accent)" />
        <text
          x={CX - R - 2}
          y={CY + 16}
          textAnchor="start"
          className="fill-fg-subtle text-[10px] font-medium uppercase tracking-wide"
        >
          Bearish
        </text>
        <text
          x={CX + R + 2}
          y={CY + 16}
          textAnchor="end"
          className="fill-fg-subtle text-[10px] font-medium uppercase tracking-wide"
        >
          Bullish
        </text>
      </svg>
      <p className="-mt-2 text-[13px] font-semibold text-accent">{label}</p>
    </div>
  );
}
