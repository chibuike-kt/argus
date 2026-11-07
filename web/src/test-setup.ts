// Registers @testing-library/jest-dom's matchers (toBeInTheDocument, etc)
// on vitest's own expect -- harmless for node-environment tests that
// never touch the DOM (web-wiring-followup brief, Part 6: jsdom/RTL are
// newly approved dependencies, this is the one global wire-up they need).
import "@testing-library/jest-dom/vitest";
