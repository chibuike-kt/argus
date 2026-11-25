import { describe, expect, it } from "vitest";
import { LOAD_OLDER_EDGE_BARS, historyCapNote, historyStartNote, prependShift, shouldLoadOlder } from "../history-paging";

describe("prependShift", () => {
  it("is the number of bars added in front of an unchanged series", () => {
    expect(prependShift([10, 11, 12], [7, 8, 9, 10, 11, 12])).toBe(3);
  });
  it("allows the live forming bar at the end to be missing from the new data", () => {
    expect(prependShift([10, 11, 12, 13], [8, 9, 10, 11, 12])).toBe(2);
  });
  it("is 0 when the old bars are not all there in order (a switch, a reload, a gap fill in the middle)", () => {
    expect(prependShift([10, 11, 12], [20, 21, 22, 23])).toBe(0);
    expect(prependShift([10, 11, 12], [10, 11, 12, 13])).toBe(0);
    expect(prependShift([10, 12, 13], [8, 9, 10, 11, 12, 13])).toBe(0);
    expect(prependShift([], [1, 2])).toBe(0);
  });
});

describe("shouldLoadOlder", () => {
  it("reads the next page only near the left edge, and never at the start or the cap", () => {
    expect(shouldLoadOlder(LOAD_OLDER_EDGE_BARS - 1, { exhausted: false, capped: false })).toBe(true);
    expect(shouldLoadOlder(LOAD_OLDER_EDGE_BARS, { exhausted: false, capped: false })).toBe(false);
    expect(shouldLoadOlder(-5, { exhausted: false, capped: false })).toBe(true);
    expect(shouldLoadOlder(0, { exhausted: true, capped: false })).toBe(false);
    expect(shouldLoadOlder(0, { exhausted: false, capped: true })).toBe(false);
    expect(shouldLoadOlder(null, { exhausted: false, capped: false })).toBe(false);
  });
});

describe("history notes", () => {
  it("says where the stored history starts, in plain words", () => {
    expect(historyStartNote("2024-08-13T04:00:00Z")).toBe("History starts 13 August 2024");
    // UTC date, not the browser's local one.
    expect(historyStartNote("2024-08-13T23:30:00Z")).toBe("History starts 13 August 2024");
  });
  it("uses no hyphens or em dashes", () => {
    for (const text of [historyStartNote("2024-08-13T04:00:00Z"), historyCapNote()]) {
      expect(text).not.toMatch(/[-–—]/);
    }
  });
});
