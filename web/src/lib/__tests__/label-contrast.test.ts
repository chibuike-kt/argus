import { describe, expect, it } from "vitest";
import {
  CHART_BACKGROUND,
  MIN_LABEL_EDGE_CONTRAST,
  contrastRatio,
  labelBackground,
  labelColors,
  labelTextColor,
  parseColor,
} from "../label-contrast";
import { PALETTE } from "@/components/chart/drawings/context-toolbar";

// One text-colour rule for every chart label (fix/chart-interaction,
// item 6), from WCAG 2 relative luminance.

const rgb = (c: string) => parseColor(c)!;

describe("labelTextColor", () => {
  it.each([
    ["#ffffff", "#000000"], // white -> black text
    ["#000000", "#ffffff"], // black -> white text
    ["#ffff00", "#000000"], // yellow -> black text
    ["#f5b461", "#000000"], // the palette's amber
    ["#ff0000", "#000000"], // pure red: L 0.2126 is above the 0.179 threshold
    ["#f0616d", "#000000"], // the palette's red
    ["#0000ff", "#ffffff"], // blue -> white text
    ["#6d7bff", "#000000"], // the palette's indigo (L 0.24)
    ["#1c2027", "#ffffff"], // a dark grey
  ])("%s -> %s", (bg, want) => {
    expect(labelTextColor(bg)).toBe(want);
  });

  it("mid greys either side of the threshold", () => {
    // L(#767676) = 0.1812, L(#757575) = 0.1779; the switch is at 0.1791.
    expect(labelTextColor("#767676")).toBe("#000000");
    expect(labelTextColor("#757575")).toBe("#ffffff");
    // At the threshold both colours give about the same ratio (~4.58).
    for (const g of ["#767676", "#757575"]) {
      const c = rgb(g);
      expect(Math.abs(contrastRatio(c, rgb("#000")) - contrastRatio(c, rgb("#fff")))).toBeLessThan(0.15);
    }
  });

  it("reads the colour forms the chart uses", () => {
    expect(labelTextColor("#fff")).toBe("#000000");
    expect(labelTextColor("rgba(255,255,255,0.3)")).toBe("#000000");
    expect(labelTextColor("#ffffff80")).toBe("#000000");
  });
});

describe("contrast for every palette colour", () => {
  const extra = ["#ffff00", "#d9d9d9", "#9e9e9e", "#3a4150", "#1c2027", "#000000", "#2ebd85"];
  it.each([...PALETTE, ...extra])("%s: text is at least 4.5:1 and the label stands out from the chart", (color) => {
    for (const chart of [CHART_BACKGROUND, "#ffffff"]) {
      const { back, text } = labelColors(color, chart);
      expect(contrastRatio(rgb(back), rgb(text))).toBeGreaterThanOrEqual(4.5);
      expect(contrastRatio(rgb(back), rgb(chart))).toBeGreaterThanOrEqual(MIN_LABEL_EDGE_CONTRAST);
    }
  });
});

describe("labelBackground", () => {
  it("leaves a colour that already stands out alone", () => {
    expect(labelBackground("#6d7bff")).toBe("#6d7bff");
    expect(labelBackground("#ffffff")).toBe("#ffffff");
  });
  it("lifts a near-black label off the dark chart, and darkens a white one on a light chart", () => {
    expect(labelBackground("#0a0a0a")).not.toBe("#0a0a0a");
    expect(labelBackground("#ffffff", "#ffffff")).not.toBe("#ffffff");
  });
});
