// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { useDrawingsStore } from "./store";

// useDrawingsStore used to call store.setScope(scopeKey) directly in
// its own render body. setScope can emit() -- synchronously notifying
// every other component subscribed to the shared store -- which is
// exactly React's "Cannot update a component while rendering a
// different component" warning (web-wiring-polish brief, item 3,
// traced by reading /analysis's own console stack). This renders a
// harness through a scope change (mirroring what actually happens on
// /analysis once the selected instrument resolves, switching the
// scopeKey this hook is given) and asserts React never logs that
// warning.
function Harness({ scopeKey }: { scopeKey: string }) {
  useDrawingsStore(scopeKey);
  return null;
}

describe("useDrawingsStore (render safety)", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("switching scopeKey across a rerender never updates another component during render", () => {
    const errors: unknown[] = [];
    vi.spyOn(console, "error").mockImplementation((...args) => {
      errors.push(args);
    });

    const { rerender } = render(<Harness scopeKey="render-safety-a:1m" />);
    rerender(<Harness scopeKey="render-safety-b:1m" />);
    rerender(<Harness scopeKey="render-safety-c:1m" />);

    const renderWarning = errors.find((args) =>
      String(args).includes("while rendering a different component"),
    );
    expect(renderWarning).toBeUndefined();
  });
});
