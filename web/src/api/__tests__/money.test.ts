import { describe, expect, it } from "vitest";
import { formatScaledInt, parseRatString, scaledIntToNumber } from "../money";

describe("formatScaledInt", () => {
  it("inserts the decimal point at the right position", () => {
    expect(formatScaledInt(9050, 2)).toBe("90.50");
  });

  it("pads with leading zeros when the scale exceeds the digit count", () => {
    expect(formatScaledInt(5, 4)).toBe("0.0005");
  });

  it("returns the bare integer string at scale 0", () => {
    expect(formatScaledInt(67880, 0)).toBe("67880");
  });

  it("handles zero", () => {
    expect(formatScaledInt(0, 2)).toBe("0.00");
  });

  it("handles negative values by formatting the magnitude and re-prefixing the sign", () => {
    expect(formatScaledInt(-150, 2)).toBe("-1.50");
  });

  it("rejects a non-safe-integer input rather than silently losing precision", () => {
    expect(() => formatScaledInt(Number.MAX_SAFE_INTEGER + 2, 2)).toThrow();
    expect(() => formatScaledInt(1.5, 2)).toThrow();
  });
});

describe("scaledIntToNumber", () => {
  it("matches formatScaledInt's own string, parsed", () => {
    expect(scaledIntToNumber(9050, 2)).toBe(90.5);
    expect(scaledIntToNumber(67880, 0)).toBe(67880);
  });
});

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
