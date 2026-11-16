import { describe, expect, it } from "vitest";
import { EM_DASH, addOneMinuteISO, direction, formatAge, formatDecimal, formatPercent } from "../decimal-display";

describe("decimal-display", () => {
  it("formats exact decimal strings without changing a digit", () => {
    expect(formatDecimal("95123.4")).toBe("95,123.4");
    expect(formatDecimal("1234567.12345678")).toBe("1,234,567.12345678");
    expect(formatDecimal("0.00001")).toBe("0.00001");
    // A value past float64's exact range keeps every digit.
    expect(formatDecimal("9007199254740993.01")).toBe("9,007,199,254,740,993.01");
  });

  it("signs changes; zero has no sign", () => {
    expect(formatDecimal("12.50", { signed: true })).toBe("+12.50");
    expect(formatDecimal("-1234.50", { signed: true })).toBe("-1,234.50");
    expect(formatDecimal("0.00", { signed: true })).toBe("0.00");
    expect(formatPercent("-1.28")).toBe("-1.28%");
    expect(formatPercent("14.40")).toBe("+14.40%");
    expect(formatPercent("0.00")).toBe("0.00%");
  });

  it("absent is an em dash, never zero", () => {
    expect(formatDecimal(null)).toBe(EM_DASH);
    expect(formatDecimal(undefined)).toBe(EM_DASH);
    expect(formatPercent(null)).toBe(EM_DASH);
    expect(direction(null)).toBe("none");
  });

  it("reads direction from the sign only", () => {
    expect(direction("0.01")).toBe("up");
    expect(direction("-0.01")).toBe("down");
    expect(direction("0.00")).toBe("flat");
  });

  it("ages are plain numbers with a unit", () => {
    expect(formatAge(12_400)).toBe("12s ago");
    expect(formatAge(5 * 60_000 + 3000)).toBe("5m ago");
    expect(formatAge(3 * 3_600_000)).toBe("3h ago");
    expect(formatAge(41 * 86_400_000)).toBe("41d ago");
    expect(formatAge(-5000)).toBe("0s ago");
  });

  it("finalized through is open time plus one minute", () => {
    expect(addOneMinuteISO("2026-11-16T18:16:00Z")).toBe("2026-11-16T18:17:00.000Z");
  });
});
