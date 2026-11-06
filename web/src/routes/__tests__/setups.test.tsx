import { describe, expect, it, vi, afterEach } from "vitest";
import { renderToString } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { Setups } from "../setups";

// A deliberately limited smoke test, not a full component test: no DOM
// testing library is installed in this repo today (no jsdom, no
// @testing-library/react), and this wiring brief's own rule 3 says not
// to add a new dependency unasked -- see the brief's report for that
// flag. react-dom/server's renderToString needs neither: it is already
// a dependency of react-dom, which this project already has.
//
// Its real limit: effects (including every fetch this route makes)
// never run during server rendering, so this only ever exercises the
// route's FIRST render -- the initial {loading: true} state, before any
// network response. It cannot reach the empty or error branches, which
// only appear after an effect resolves. Reaching those in a real test
// needs a DOM environment; this is recorded as a known gap, not silently
// skipped.
describe("Setups route (SSR smoke test)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the loading state on first render without throwing", () => {
    global.fetch = vi.fn().mockReturnValue(new Promise(() => {})); // never resolves
    const html = renderToString(
      <MemoryRouter>
        <Setups />
      </MemoryRouter>,
    );
    expect(html).toContain("Setups");
    expect(html).toMatch(/Loading/);
  });
});
