// label-contrast.ts: the one rule for the text colour of any chart label
// that takes a colour (fix/chart-interaction, item 6): a drawing's price
// and time axis labels, the last price label, and ARGUS's level and setup
// labels. Owner report: a white line gave a white price label with white
// text.
//
// Text is black or white, whichever has the higher WCAG 2 contrast ratio
// against the label's background, from the background's relative
// luminance -- never a per-colour table. Black wins when the luminance is
// above sqrt(1.05 * 0.05) - 0.05 (about 0.179); at that point both give
// the same ratio, and either way the ratio is at least 4.58:1 for any
// opaque colour.
//
// labelBackground also keeps a label from vanishing into the chart
// itself (a white label on a light chart, a near-black one on this dark
// chart): when the label's colour is too close to the chart background,
// it is shifted toward the opposite end until the two differ by
// MIN_LABEL_EDGE_CONTRAST. The axis labels the library draws have no
// border option, so the edge is the background itself.

export interface Rgb {
  r: number;
  g: number;
  b: number;
}

// The chart's own background (the --color-surface token; the chart
// canvas is transparent over it). The app has one, dark, chart theme.
export const CHART_BACKGROUND = "#0a0a0a";

// [U] display value: the least contrast between a label and the chart
// behind it, so its edge stays visible.
export const MIN_LABEL_EDGE_CONTRAST = 1.5;

// parseColor reads #rgb, #rrggbb, #rrggbbaa (alpha ignored) and rgb()/rgba().
export function parseColor(color: string): Rgb | null {
  const c = color.trim().toLowerCase();
  let m = /^#([0-9a-f]{3})$/.exec(c);
  if (m) {
    const [r, g, b] = m[1].split("").map((h) => parseInt(h + h, 16));
    return { r, g, b };
  }
  m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/.exec(c);
  if (m) {
    return { r: parseInt(m[1].slice(0, 2), 16), g: parseInt(m[1].slice(2, 4), 16), b: parseInt(m[1].slice(4, 6), 16) };
  }
  m = /^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/.exec(c);
  if (m) return { r: Number(m[1]), g: Number(m[2]), b: Number(m[3]) };
  return null;
}

function channel(v: number): number {
  const s = v / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

// relativeLuminance is WCAG 2's relative luminance, 0 (black) to 1 (white).
export function relativeLuminance(rgb: Rgb): number {
  return 0.2126 * channel(rgb.r) + 0.7152 * channel(rgb.g) + 0.0722 * channel(rgb.b);
}

// contrastRatio is WCAG 2's contrast ratio, 1 to 21.
export function contrastRatio(a: Rgb, b: Rgb): number {
  const la = relativeLuminance(a);
  const lb = relativeLuminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

const BLACK: Rgb = { r: 0, g: 0, b: 0 };
const WHITE: Rgb = { r: 255, g: 255, b: 255 };

function toHex({ r, g, b }: Rgb): string {
  return "#" + [r, g, b].map((v) => Math.round(v).toString(16).padStart(2, "0")).join("");
}

// labelTextColor is the text colour for a label on background: "#000000"
// or "#ffffff", whichever contrasts more. An unreadable colour string
// falls back to white text (the label colours here are all hex).
export function labelTextColor(background: string): "#000000" | "#ffffff" {
  const bg = parseColor(background);
  if (!bg) return "#ffffff";
  return contrastRatio(bg, BLACK) >= contrastRatio(bg, WHITE) ? "#000000" : "#ffffff";
}

// labelBackground is the label's own colour, shifted just enough to stand
// out from the chart background when it would otherwise blend into it.
export function labelBackground(color: string, chartBackground: string = CHART_BACKGROUND): string {
  const c = parseColor(color);
  const bg = parseColor(chartBackground);
  if (!c || !bg) return color;
  if (contrastRatio(c, bg) >= MIN_LABEL_EDGE_CONTRAST) return color;
  // Away from the chart: toward white on a dark chart, black on a light one.
  const target = relativeLuminance(bg) < 0.5 ? WHITE : BLACK;
  for (let t = 0.05; t <= 1; t += 0.05) {
    const mixed = {
      r: c.r + (target.r - c.r) * t,
      g: c.g + (target.g - c.g) * t,
      b: c.b + (target.b - c.b) * t,
    };
    if (contrastRatio(mixed, bg) >= MIN_LABEL_EDGE_CONTRAST) return toHex(mixed);
  }
  return toHex(target);
}

// labelColors is both: the background to draw and its text colour.
export function labelColors(color: string, chartBackground: string = CHART_BACKGROUND): { back: string; text: string } {
  const back = labelBackground(color, chartBackground);
  return { back, text: labelTextColor(back) };
}
