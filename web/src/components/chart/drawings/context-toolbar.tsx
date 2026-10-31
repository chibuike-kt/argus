import { useState } from "react";
import { Copy, Trash, GearSix, Minus } from "@phosphor-icons/react";
import { cn } from "@/lib/cn";
import type { Drawing, LineStyle } from "./types";

const PALETTE = ["#6d7bff", "#f0616d", "#2ebd85", "#f5b461", "#ffffff", "#9aa1ab"];
const WIDTHS = [1, 1.5, 2, 3];

export interface ContextToolbarProps {
  drawing: Drawing;
  // Position in container-relative pixels, anchored above the drawing's
  // own bounding box by the caller (chart-surface knows the geometry).
  x: number;
  y: number;
  onStyleChange: (patch: { color?: string; lineWidth?: number; lineStyle?: LineStyle }) => void;
  onLabelChange?: (text: string) => void;
  onClone: () => void;
  onDelete: () => void;
}

const LABELLABLE: Drawing["kind"][] = ["rectangle", "text"];

// The small floating toolbar that appears above a selected drawing: colour,
// line style, width, an inline label editor for kinds that carry text, and
// clone/delete. Kept in its own module per the brief's "handle rendering and
// the context toolbar as their own concerns."
export function ContextToolbar({
  drawing,
  x,
  y,
  onStyleChange,
  onLabelChange,
  onClone,
  onDelete,
}: ContextToolbarProps) {
  const [editingLabel, setEditingLabel] = useState(false);
  const canLabel = LABELLABLE.includes(drawing.kind) && onLabelChange;
  const currentLabel =
    drawing.kind === "text"
      ? drawing.text
      : drawing.kind === "rectangle"
        ? (drawing.label ?? "")
        : "";

  return (
    <div
      className="glass-strong absolute z-20 flex -translate-x-1/2 -translate-y-full items-center gap-1 rounded-lg px-1.5 py-1 shadow-xl"
      style={{ left: x, top: y - 10 }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <div className="flex items-center gap-0.5 pr-1">
        {PALETTE.map((c) => (
          <button
            key={c}
            aria-label={`Colour ${c}`}
            onClick={() => onStyleChange({ color: c })}
            className={cn(
              "size-4.5 rounded-full ring-1 ring-inset ring-white/20 transition-transform hover:scale-110",
              drawing.style.color === c && "ring-2 ring-white/70",
            )}
            style={{ backgroundColor: c }}
          />
        ))}
      </div>

      <span className="h-5 w-px bg-line" />

      <button
        aria-label="Toggle dashed"
        title="Line style"
        onClick={() =>
          onStyleChange({ lineStyle: drawing.style.lineStyle === "dashed" ? "solid" : "dashed" })
        }
        className={cn(
          "focus-ring grid size-6 place-items-center rounded-md hover:bg-white/5",
          drawing.style.lineStyle === "dashed" ? "text-fg" : "text-fg-muted",
        )}
      >
        <Minus className={cn("size-4", drawing.style.lineStyle === "dashed" && "opacity-60")} />
        <span className="sr-only">{drawing.style.lineStyle}</span>
      </button>

      <div className="flex items-center gap-0.5 px-0.5">
        {WIDTHS.map((w) => (
          <button
            key={w}
            aria-label={`Width ${w}`}
            onClick={() => onStyleChange({ lineWidth: w })}
            className={cn(
              "grid size-6 place-items-center rounded-md hover:bg-white/5",
              drawing.style.lineWidth === w ? "text-fg" : "text-fg-subtle",
            )}
          >
            <span className="w-3 rounded-full bg-current" style={{ height: Math.max(1, w) }} />
          </button>
        ))}
      </div>

      {canLabel && (
        <>
          <span className="h-5 w-px bg-line" />
          <button
            aria-label="Edit label"
            title="Edit label"
            onClick={() => setEditingLabel((v) => !v)}
            className={cn(
              "focus-ring grid size-6 place-items-center rounded-md hover:bg-white/5",
              editingLabel ? "text-fg" : "text-fg-muted",
            )}
          >
            <GearSix className="size-4" />
          </button>
        </>
      )}

      <span className="h-5 w-px bg-line" />

      <button
        aria-label="Clone"
        title="Clone"
        onClick={onClone}
        className="focus-ring grid size-6 place-items-center rounded-md text-fg-muted hover:bg-white/5 hover:text-fg"
      >
        <Copy className="size-4" />
      </button>
      <button
        aria-label="Delete"
        title="Delete"
        onClick={onDelete}
        className="focus-ring grid size-6 place-items-center rounded-md text-fg-muted hover:bg-down/15 hover:text-down"
      >
        <Trash className="size-4" />
      </button>

      {editingLabel && canLabel && (
        <input
          autoFocus
          value={currentLabel}
          onChange={(e) => onLabelChange?.(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === "Escape") setEditingLabel(false);
          }}
          placeholder="Label"
          className="glass absolute left-0 top-full mt-1 w-40 rounded-md px-2 py-1 text-[12px] text-fg outline-none"
        />
      )}
    </div>
  );
}
