import { describe, expect, it } from "vitest";
import { parseRatString } from "../money";

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
