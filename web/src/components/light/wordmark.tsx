// A small ARGUS wordmark, navy, --font-display -- deliberately added to
// the screenshots' own layout (which show no brand mark at all) so the
// light pages still read as ARGUS (web light auth redesign brief: "one
// addition to the screenshots, deliberately"). AuthShell places this
// in the page margin, outside the centred column, on wide screens, and
// above the column on narrow ones -- see auth-shell.tsx.
export function Wordmark() {
  return (
    <span className="font-display text-base font-medium tracking-wide text-light-text">
      ARGUS
    </span>
  );
}
