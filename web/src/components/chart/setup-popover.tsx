import { useEffect } from "react";
import type { LayerElement } from "./setup-layers";

// SetupElementPopover says what one of ARGUS's chart elements is: its
// setup, timeframe, state, the candles it came from and the reason in
// plain words (templated from recorded fields, setup-layers.ts). ARGUS
// elements cannot be moved or deleted -- a setup is an immutable decision
// (ADR-011) -- so the only action on the element itself is to copy it into
// an editable drawing of the trader's own.
export function SetupElementPopover({
  element,
  x,
  y,
  onClose,
  onCopy,
  onOpenSetup,
}: {
  element: LayerElement;
  x: number;
  y: number;
  onClose: () => void;
  onCopy: () => void;
  // Set when the element belongs to a setup other than the selected one.
  onOpenSetup?: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const p = element.popover;
  return (
    <div
      role="dialog"
      aria-label={p.title}
      data-testid="setup-element-popover"
      data-element={element.id}
      className="glass-strong absolute z-30 w-80 rounded-lg p-3 text-[12px] text-fg-muted shadow-lg"
      style={{ left: x, top: y }}
      onClick={(e) => e.stopPropagation()}
    >
      <div className="mb-1 flex items-start justify-between gap-2">
        <span className="font-semibold text-fg" data-testid="popover-title">
          {p.title}
        </span>
        <button type="button" onClick={onClose} className="focus-ring rounded px-1 text-fg-subtle hover:text-fg" aria-label="Close">
          ×
        </button>
      </div>
      <p className="mb-2 leading-snug text-fg" data-testid="popover-reason">
        {p.reason}
      </p>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
        {p.rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-fg-subtle">{k}</dt>
            <dd className="tnum" data-testid={`popover-row-${k.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`}>
              {v}
            </dd>
          </div>
        ))}
        {p.candles.length > 0 && (
          <>
            <dt className="text-fg-subtle">Candles</dt>
            <dd className="tnum" data-testid="popover-candles">
              {p.candles.map((c) => (
                <div key={c}>{c}</div>
              ))}
            </dd>
          </>
        )}
      </dl>
      <div className="mt-2 flex flex-wrap gap-1.5">
        <button
          type="button"
          onClick={onCopy}
          data-testid="copy-to-drawings"
          className="focus-ring rounded-md border border-line px-2 py-1 text-[11px] font-medium text-fg hover:bg-white/5"
        >
          Copy to my drawings
        </button>
        {onOpenSetup && (
          <button
            type="button"
            onClick={onOpenSetup}
            data-testid="open-setup"
            className="focus-ring rounded-md border border-line px-2 py-1 text-[11px] font-medium text-fg hover:bg-white/5"
          >
            Open setup #{element.setupId}
          </button>
        )}
      </div>
      <p className="mt-2 text-[11px] text-fg-subtle">
        ARGUS analysis, not a drawing: it cannot be moved or deleted, because a setup is a recorded decision that is never
        rewritten. Copy it to get an editable drawing of your own; the original stays as it is. A zone thinner than a few pixels is
        drawn at a minimum size; the values above are the stored ones.
      </p>
    </div>
  );
}
