// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { LivePrice, LIVE_PRICE_HOLD_MS, changedFrom, priceDirection } from "../live-price";
import { formatDecimal } from "@/lib/decimal-display";

// The owner's live price spec (Phase 3): step one colours the whole
// price by direction, step two (after the hold) leaves only the changed
// digits coloured until the next change.

function Price({ value, resetKey }: { value: string | null; resetKey?: string }) {
  return (
    <LivePrice testId="p" value={value} text={value === null ? null : formatDecimal(value)} reason="No candle stored yet" resetKey={resetKey} />
  );
}

const el = () => screen.getByTestId("p");
const unchanged = () => el().querySelector('[data-part="unchanged"]')!;
const changed = () => el().querySelector('[data-part="changed"]')!;

describe("changedFrom (changed digits, compared from the left)", () => {
  it.each([
    ["62,540.5", "62,540.6", "62,540.", "6"],
    ["0.1510", "0.1520", "0.15", "20"],
    // A length change changes the whole number.
    ["999.9", "1,000.0", "", "1,000.0"],
    // Same first digit, longer number: still the whole number.
    ["100.0", "1,000.0", "", "1,000.0"],
  ])("%s -> %s keeps %j and changes %j", (prev, next, keep, change) => {
    const i = changedFrom(prev, next);
    expect(next.slice(0, i)).toBe(keep);
    expect(next.slice(i)).toBe(change);
  });
});

describe("priceDirection (exact, no threshold)", () => {
  it("a tiny up move is up and a tiny down move is down", () => {
    expect(priceDirection("62540.5", "62540.6")).toBe("up");
    expect(priceDirection("62540.6", "62540.5")).toBe("down");
    // Beyond float precision: still exact.
    expect(priceDirection("0.10000000000000000001", "0.10000000000000000002")).toBe("up");
    expect(priceDirection("62540.5", "62540.50")).toBeNull();
  });
});

describe("LivePrice", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("the first value is at rest: normal colour, no flash", () => {
    render(<Price value="62540.5" />);
    expect(el()).toHaveTextContent("62,540.5");
    expect(el()).not.toHaveAttribute("data-dir");
    expect(el().querySelector(".text-up, .text-down")).toBeNull();
  });

  it("step one colours the whole price, step two keeps only the changed digits", () => {
    const view = render(<Price value="62540.5" />);
    view.rerender(<Price value="62540.6" />);
    // Step one: every digit green.
    expect(el()).toHaveAttribute("data-phase", "all");
    expect(unchanged()).toHaveTextContent("62,540.");
    expect(unchanged()).toHaveClass("text-up");
    expect(changed()).toHaveTextContent(/^6$/);
    expect(changed()).toHaveClass("text-up");

    // Step two after the hold: "62,540." back to normal, "6" stays green.
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS));
    expect(el()).toHaveAttribute("data-phase", "split");
    expect(unchanged()).not.toHaveClass("text-up");
    expect(changed()).toHaveClass("text-up");

    // And stays so: no fade of the changed digits, however long.
    act(() => vi.advanceTimersByTime(60_000));
    expect(changed()).toHaveClass("text-up");
  });

  it("a down move is red", () => {
    const view = render(<Price value="62540.6" />);
    view.rerender(<Price value="62540.5" />);
    expect(unchanged()).toHaveClass("text-down");
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS));
    expect(changed()).toHaveTextContent(/^5$/);
    expect(changed()).toHaveClass("text-down");
    expect(unchanged()).not.toHaveClass("text-down");
  });

  it("an equal price changes nothing", () => {
    const view = render(<Price value="62540.5" />);
    view.rerender(<Price value="62540.6" />);
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS));
    const before = el().outerHTML;
    view.rerender(<Price value="62540.6" />);
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS));
    expect(el().outerHTML).toBe(before);
    expect(el()).toHaveAttribute("data-phase", "split");

    // An equal decimal written differently is still equal: no flash, the
    // colouring stays.
    view.rerender(<Price value="62540.60" />);
    expect(el()).toHaveAttribute("data-dir", "up");
    expect(el()).toHaveAttribute("data-phase", "split");
  });

  it("a new tick during the hold restarts from step one with the new direction", () => {
    const view = render(<Price value="62540.5" />);
    view.rerender(<Price value="62540.6" />); // up, step one
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS - 100));
    view.rerender(<Price value="62539.9" />); // down, mid-hold
    expect(el()).toHaveAttribute("data-dir", "down");
    expect(el()).toHaveAttribute("data-phase", "all");
    expect(unchanged()).toHaveClass("text-down");
    // The first tick's timer must not cut the new hold short.
    act(() => vi.advanceTimersByTime(100));
    expect(el()).toHaveAttribute("data-phase", "all");
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS - 100));
    expect(el()).toHaveAttribute("data-phase", "split");
    // 62,540.6 -> 62,539.9: "62,5" kept, "39.9" changed.
    expect(unchanged()).toHaveTextContent("62,5");
    expect(changed()).toHaveTextContent("39.9");
  });

  it("the next change after step two starts step one again", () => {
    const view = render(<Price value="0.1510" />);
    view.rerender(<Price value="0.1520" />);
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS));
    view.rerender(<Price value="0.1521" />);
    expect(el()).toHaveAttribute("data-phase", "all");
    expect(unchanged()).toHaveClass("text-up");
    act(() => vi.advanceTimersByTime(LIVE_PRICE_HOLD_MS));
    expect(changed()).toHaveTextContent(/^1$/);
  });

  it("a value after a dash, and a switch, are first values again", () => {
    const view = render(<Price value="100.0" resetKey="BTC" />);
    view.rerender(<Price value={null} resetKey="BTC" />);
    expect(el()).toHaveTextContent("—");
    expect(el()).toHaveAttribute("title", "No candle stored yet");
    view.rerender(<Price value="101.0" resetKey="BTC" />);
    expect(el()).not.toHaveAttribute("data-dir");

    view.rerender(<Price value="3000.0" resetKey="ETH" />);
    expect(el()).not.toHaveAttribute("data-dir");
    expect(el().querySelector(".text-up, .text-down")).toBeNull();
  });

  it("prefers-reduced-motion skips the transition and keeps the changed digits coloured", () => {
    const original = window.matchMedia;
    window.matchMedia = ((q: string) => ({ matches: q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
    try {
      const view = render(<Price value="999.9" />);
      view.rerender(<Price value="1000.0" />);
      expect(el()).toHaveAttribute("data-phase", "split");
      expect(changed()).toHaveTextContent("1,000.0");
      expect(changed()).toHaveClass("text-up");
    } finally {
      window.matchMedia = original;
    }
  });
});
