import { describe, expect, it } from "vitest";
import { formatRR, parseRatString } from "../money";

describe("parseRatString", () => {
  it("parses an exact fraction", () => {
    expect(parseRatString("3/2")).toBe(1.5);
  });

  it("parses a bare integer with no slash", () => {
    expect(parseRatString("0")).toBe(0);
    expect(parseRatString("2")).toBe(2);
  });

  it("throws on a malformed ratio", () => {
    expect(() => parseRatString("3/0")).toThrow();
    expect(() => parseRatString("abc")).toThrow();
  });
});

// web-wiring-polish brief, item 3: a small target ratio like 1/27 (the
// real example the architect's review flagged) rounded to "0.0R" at one
// decimal place, reading as no reward at all.
describe("formatRR", () => {
  it("shows two decimals below 0.1", () => {
    expect(formatRR(parseRatString("1/27"))).toBe("0.04R");
  });

  it("keeps one decimal at or above 0.1", () => {
    expect(formatRR(0.1)).toBe("0.1R");
    expect(formatRR(1.5)).toBe("1.5R");
    expect(formatRR(2)).toBe("2.0R");
  });
});
