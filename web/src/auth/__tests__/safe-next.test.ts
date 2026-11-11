import { describe, expect, it } from "vitest";
import { safeNextPath } from "../safe-next";

describe("safeNextPath", () => {
  it("allows a same-origin relative path", () => {
    expect(safeNextPath("/account")).toBe("/account");
    expect(safeNextPath("/setups?instrumentId=1")).toBe("/setups?instrumentId=1");
  });

  it("rejects a scheme-relative path (the open-redirect vector)", () => {
    expect(safeNextPath("//evil.example.com")).toBe("/setups");
  });

  it("rejects an absolute external URL", () => {
    expect(safeNextPath("https://evil.example.com")).toBe("/setups");
    expect(safeNextPath("http://evil.example.com/account")).toBe("/setups");
  });

  it("rejects a non-path value", () => {
    expect(safeNextPath("javascript:alert(1)")).toBe("/setups");
    expect(safeNextPath("account")).toBe("/setups");
  });

  it("falls back to the given default, or /setups, when next is missing", () => {
    expect(safeNextPath(null)).toBe("/setups");
    expect(safeNextPath(null, "/login")).toBe("/login");
  });
});
