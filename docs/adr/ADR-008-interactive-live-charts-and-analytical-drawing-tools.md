# ADR-008: Interactive live charts and analytical drawing tools

- Status: Charting technology decided (Option A); phase 2 (interaction layer) implemented
- Category: Charting and visual analysis
- Version: 0.2.0
- Last updated: 30 October 2026
- Related: [ADR-009](ADR-009-deterministic-versioned-multi-timeframe-analysis.md), [ADR-010](ADR-010-setup-state-invalidation-and-re-entry.md)

---

## Context

ARGUS must be capable of using chart-analysis tools on live charts, resembling a technical analyst working in TradingView.

The chart is not merely a visualization of indicator output. It is part of the analytical workflow and the main way users inspect the evidence behind a setup.

## Confirmed requirement

ARGUS must support both:

- Automated chart drawings created and updated by the analysis engine.

- User-created drawings that users can create, edit, move, hide, show and delete.

The two categories must be visually distinguishable and have separate ownership and change semantics.

## Required drawing tools

### LONG and SHORT

Represent proposed analytical trade plans.

A position drawing should be capable of presenting the relevant entry, stop-loss, take-profit targets and risk-to-reward information where applicable.

These drawings must not be confused with actual orders or broker execution.

### INFO LINE

Measure the price and time difference between two chart points and provide relevant measurement information.

Exact displayed measurements and formatting remain to be specified.

### TREND LINE

Connect chart anchors such as confirmed swing points, structure levels or user-selected coordinates.

Automatic trend lines must be grounded in detected structure. User-created trend lines remain user-owned visual objects.

### RECTANGLE

Represent bounded chart regions, including supply and demand zones, support/resistance regions and other points of interest.

A rectangle linked to an ARGUS POI must carry its relationship to the underlying POI and reflect its state without destroying historical evidence.

### FIB

Support Fibonacci retracement and extension drawings anchored to relevant chart points.

For automated use, the anchor-selection rule and level configuration must be explicit and reproducible. The system must not select arbitrary swings without a strategy-defined rule.

### TEXT

Display annotations for BOS, CHOCH, liquidity sweeps, displacement, order blocks, fair value gaps, confluences, invalidation and setup reasoning.

Text must be associated with its source evidence where it represents an ARGUS conclusion.

## Drawing object model

A persisted drawing object should represent, as appropriate:

- Stable drawing identifier.

- Owner: ARGUS or user.

- Drawing type.

- Instrument.

- Timeframe.

- Anchor points and coordinates.

- Creation and update timestamps.

- Visibility and styling preferences.

- Linked POI, setup or plan version.

- Source evidence and strategy version for automated drawings.

- Current drawing status.

- Revision history where required.

The exact schema remains open.

## Editing and ownership rules

Editing a user-created drawing must not change ARGUS's analytical state.

Editing an ARGUS-generated drawing must distinguish between:

- A personal visual override.

- A new user annotation.

- A proposed change to the analytical plan.

A visual adjustment must not silently rewrite the evidence, setup lifecycle or underlying strategy decision.

## Live updates and historical correctness

When the analysis engine changes a POI or setup state, associated ARGUS annotations should update according to defined rules.

When corrected market data changes an earlier analysis, ARGUS must preserve the original decision and append an amendment. Current annotations may reflect the latest state, but the timeline must retain the earlier evidence and decision.

User drawings must remain anchored consistently when users change chart timeframes or reload the chart.

## Charting technology evaluation

Two primary options remain under consideration:

Option A: TradingView Lightweight Charts with a custom drawing engine

Advantages:

- More control over the chart and analytical-object model.

- Open-source library with an Apache 2.0 license.

- Potentially lower licensing cost.

Trade-offs:

- ARGUS must build and maintain drawing tools, editing behavior, persistence, selection, layering and interactions.

- A complete TradingView-style drawing toolbar is not provided simply by adopting Lightweight Charts.

Option B: TradingView Advanced Charts or another charting solution

Advantages:

- Potential access to a more complete technical-analysis charting experience and built-in drawing tools.

- Potentially less custom drawing-interface work.

Trade-offs:

- Access, eligibility, licensing, attribution and commercial-use conditions must be verified.

- The solution must support ARGUS's intended public application and tiered access model.

## Decision rule

The charting library will not be selected until the team verifies the required drawing functionality, licensing, access conditions, editing APIs, persistence requirements and implementation cost.

The desired TradingView-like experience is confirmed. The technology used to deliver it is not.

## Decision: Option A, implemented in two phases

Option A (TradingView Lightweight Charts v5 with a custom drawing engine) is adopted. Rendering
uses v5's `ISeriesPrimitive` plugin API; everything else -- selection, editing, persistence, undo/
redo -- is ARGUS's own.

**Phase 1 (rendering).** `ISeriesPrimitive` classes, one per drawing kind, drawing to the pane's
canvas from chart/series coordinate conversions (`timeToCoordinate`/`priceToCoordinate` and their
inverses). No interaction.

**Phase 2 (interaction), implemented 30 October 2026.** Three concerns, kept separate so each is
testable and swappable on its own:

- **Store** (`web/src/components/chart/drawings/store.ts`): the drawing list, selection, and a
  snapshot-based undo/redo stack, behind a `DrawingsPersistence` adapter interface. The only
  implementation today is `localStorage`, keyed by instrument + timeframe (switching either swaps
  the visible set). A server-backed adapter implements the same two-method interface and drops in
  without changing the store's callers -- the "data swap, not a rewrite" ARGUS's own system
  annotations are already held to (see below).
- **Controller** (`drawings/controller.ts`): tool lifecycle (two-point and one-click creation,
  click-click and click-drag-release both committing, Esc to cancel), hit testing and selection,
  and drag (move the whole drawing, or reshape via a handle), all driven by pointer events plus the
  same chart coordinate conversions phase 1 already used. Hit testing and handle geometry
  (`drawings/hit-test.ts`, `drawings/geometry.ts`, `drawings/transform.ts`) are pure functions, not
  methods on the primitives -- the primitive protocol's own `hitTest` hook was not used, since
  ARGUS's selection model (handles, drag-to-reshape, a floating context toolbar) needed more state
  than that hook is shaped to carry.
- **Presentation**: a handle overlay (its own `ISeriesPrimitive`, attached only while something is
  selected) and a floating context toolbar / right-click menu as ordinary React components
  (`drawings/context-toolbar.tsx`, `drawings/context-menu.tsx`), positioned from the same handle
  geometry the controller hit-tests against.

Every anchor is stored as time + price, never a pixel -- a drawing stays correct under zoom, pan,
and as new candles arrive, mirroring ADR-009/ADR-019's point-in-time discipline on the backend.

**System annotations** (the order block, POI, and trade plan the engine produces) render through
the same primitive classes, in a distinct provenance-blue tone, but are never inserted into the
user drawing store and never hit-tested by the controller -- satisfying this ADR's "separate
ownership and change semantics" requirement structurally, not by a visibility flag. They are
fixture-fed (`web/src/mocks/fixtures.ts`) until a live engine endpoint exists; swapping that source
is the data change the store/controller split above was built to localise, not a UI rewrite.

**Open for a later batch:** the full persisted drawing object model this ADR's "Drawing object
model" section describes (owner, linked POI/setup, revision history, strategy version) --
phase 2 ships a lighter client-only shape sufficient for user drawings; a server-backed store is
where that fuller model would actually need to exist. Multi-timeframe trend-line anchoring to
detected structure (this ADR's automated-trend-line requirement) also waits on that live data.
