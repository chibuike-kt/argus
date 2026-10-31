import { useState } from "react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  Cursor,
  Crosshair,
  Circle,
  Eraser,
  TrendUp,
  TrendDown,
  ArrowUpRight,
  ArrowsLeftRight,
  ArrowsDownUp,
  Minus,
  LineVertical,
  Rows,
  TextAlignJustify,
  Clock,
  Path,
  Square,
  Triangle,
  PenNib,
  Target,
  ChartBar,
  TextT,
  Note,
  ChatCircle,
  Tag,
  Ruler,
  MagnetStraight,
  Lock,
  Eye,
  EyeSlash,
  Trash,
  Star,
  type Icon,
} from "@phosphor-icons/react";
import { cn } from "@/lib/cn";
import { useDrawing } from "@/components/chart/drawing-context";
import { TOOL_TO_KIND } from "@/components/chart/drawings/types";

type Variant = { id: string; label: string; icon: Icon; fav?: boolean; disabled?: boolean };
type Group = { id: string; variants: Variant[] };

// Grouped drawing tools, TradingView-style. The rail shows the group's
// current variant; the corner arrow opens the full flyout of variants.
// Variants not wired to a drawing kind (see drawings/types.ts TOOL_TO_KIND)
// are disabled rather than hidden, per the brief's latitude on out-of-V1 tools.
const GROUPS: Group[] = [
  {
    id: "cursor",
    variants: [
      { id: "cross", label: "Cursor", icon: Crosshair },
      { id: "dot", label: "Dot", icon: Circle },
      { id: "arrow", label: "Arrow", icon: Cursor },
      { id: "eraser", label: "Eraser", icon: Eraser },
    ],
  },
  {
    id: "lines",
    variants: [
      { id: "trend", label: "Trend line", icon: TrendUp, fav: true },
      { id: "ray", label: "Ray", icon: ArrowUpRight },
      { id: "extended", label: "Extended line", icon: ArrowsLeftRight },
      { id: "hline", label: "Horizontal line", icon: Minus, fav: true },
      { id: "vline", label: "Vertical line", icon: LineVertical },
    ],
  },
  {
    id: "fib",
    variants: [
      { id: "fib-retr", label: "Fib retracement", icon: Rows, fav: true },
      { id: "fib-ext", label: "Fib extension", icon: TextAlignJustify, disabled: true },
      { id: "fib-time", label: "Fib time zone", icon: Clock, disabled: true },
      { id: "fib-chan", label: "Fib channel", icon: Path, disabled: true },
    ],
  },
  {
    id: "shapes",
    variants: [
      { id: "rect", label: "Rectangle", icon: Square, fav: true },
      { id: "ellipse", label: "Ellipse", icon: Circle, disabled: true },
      { id: "triangle", label: "Triangle", icon: Triangle, disabled: true },
      { id: "path", label: "Path", icon: PenNib, disabled: true },
    ],
  },
  {
    id: "forecast",
    variants: [
      { id: "long", label: "Long position", icon: TrendUp, fav: true },
      { id: "short", label: "Short position", icon: TrendDown, fav: true },
      { id: "forecast", label: "Position forecast", icon: Target, disabled: true },
      { id: "bars", label: "Bars pattern", icon: ChartBar, disabled: true },
      { id: "price-range", label: "Price range", icon: ArrowsDownUp, disabled: true },
    ],
  },
  {
    id: "text",
    variants: [
      { id: "text", label: "Text", icon: TextT, fav: true },
      { id: "note", label: "Note", icon: Note, disabled: true },
      { id: "callout", label: "Callout", icon: ChatCircle, disabled: true },
      { id: "label", label: "Price label", icon: Tag, disabled: true },
    ],
  },
  { id: "measure", variants: [{ id: "measure", label: "Measure", icon: Ruler }] },
];

// Reverse lookup, built once: which group + variant index a tool id belongs
// to, so the rail's highlight can be *derived* from the single activeTool
// in context rather than kept in a second, independently-updated place --
// that second copy is exactly what let the rail and the chart disagree
// about the active tool before.
const TOOL_INDEX: Record<string, { groupId: string; index: number }> = {};
for (const g of GROUPS) {
  g.variants.forEach((v, i) => (TOOL_INDEX[v.id] = { groupId: g.id, index: i }));
}

function ToolGroup({
  group,
  activeVariantIndex,
  isActiveGroup,
  onSelect,
  onPick,
}: {
  group: Group;
  activeVariantIndex: number;
  isActiveGroup: boolean;
  onSelect: () => void;
  onPick: (i: number) => void;
}) {
  const v = group.variants[activeVariantIndex];
  const VariantIcon = v.icon;
  const hasFlyout = group.variants.length > 1;
  return (
    <div className="relative">
      <button
        onClick={onSelect}
        aria-label={v.label}
        title={v.label}
        className={cn(
          "focus-ring grid size-9 place-items-center rounded-md transition-colors [&_svg]:size-[19px]",
          isActiveGroup
            ? "bg-accent text-accent-foreground"
            : "text-fg-muted hover:bg-white/5 hover:text-fg",
        )}
      >
        <VariantIcon weight={isActiveGroup ? "fill" : "regular"} />
      </button>

      {hasFlyout && (
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button
              aria-label={`${v.label} variants`}
              className="focus-ring absolute bottom-0 right-0 grid size-3.5 place-items-center rounded-sm outline-none"
            >
              <span
                className={cn(
                  "h-0 w-0 border-b-[5px] border-l-[5px] border-l-transparent",
                  isActiveGroup ? "border-b-accent-foreground/80" : "border-b-fg-subtle",
                )}
              />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              side="right"
              align="start"
              sideOffset={10}
              className="glass-strong z-[60] min-w-[220px] rounded-lg p-1 shadow-xl"
            >
              {group.variants.map((variant, i) => {
                const VI = variant.icon;
                return (
                  <DropdownMenu.Item
                    key={variant.id}
                    disabled={variant.disabled}
                    onSelect={() => !variant.disabled && onPick(i)}
                    className={cn(
                      "flex cursor-pointer items-center justify-between gap-8 rounded-md px-2.5 py-1.5 text-[13px] outline-none transition-colors data-[highlighted]:bg-white/5",
                      variant.disabled
                        ? "cursor-not-allowed text-fg-subtle/40"
                        : i === activeVariantIndex
                          ? "text-accent"
                          : "text-fg-muted data-[highlighted]:text-fg",
                    )}
                  >
                    <span className="flex items-center gap-2.5">
                      <VI
                        weight={i === activeVariantIndex && !variant.disabled ? "fill" : "regular"}
                        className={cn(
                          "size-4",
                          i === activeVariantIndex && !variant.disabled
                            ? "text-accent"
                            : "text-fg-subtle",
                        )}
                      />
                      {variant.label}
                    </span>
                    <Star
                      weight={variant.fav ? "fill" : "regular"}
                      className={cn("size-3.5", variant.fav ? "text-accent" : "text-fg-subtle/30")}
                    />
                  </DropdownMenu.Item>
                );
              })}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      )}
    </div>
  );
}

export function ToolRail() {
  const {
    activeTool,
    setActiveTool,
    magnetOn,
    setMagnetOn,
    locked,
    setLocked,
    hidden,
    setHidden,
    drawings,
  } = useDrawing();
  // "Last picked variant" per group -- a UI convenience, not the source of
  // truth for what's active (that is always TOOL_INDEX[activeTool]).
  const [lastPicked, setLastPicked] = useState<Record<string, number>>({});
  const [confirmingClear, setConfirmingClear] = useState(false);

  const activeEntry = TOOL_INDEX[activeTool];

  const pick = (groupId: string, index: number) => {
    const tool = GROUPS.find((g) => g.id === groupId)!.variants[index].id;
    setLastPicked((c) => ({ ...c, [groupId]: index }));
    setActiveTool(tool);
  };

  const count = drawings.drawings.length;

  return (
    <div
      className="argus-in glass flex w-12 shrink-0 flex-col items-center gap-0.5 rounded-xl py-1.5"
      style={{ "--argus-in-delay": "40ms" } as React.CSSProperties}
    >
      {GROUPS.map((g) => {
        const isActiveGroup = activeEntry?.groupId === g.id;
        const variantIndex = isActiveGroup ? activeEntry.index : (lastPicked[g.id] ?? 0);
        return (
          <ToolGroup
            key={g.id}
            group={g}
            activeVariantIndex={variantIndex}
            isActiveGroup={isActiveGroup}
            onSelect={() => pick(g.id, variantIndex)}
            onPick={(i) => pick(g.id, i)}
          />
        );
      })}

      <span className="my-1 h-px w-6 bg-line" />

      <button
        onClick={() => setMagnetOn(!magnetOn)}
        aria-label="Magnet"
        title="Magnet: snap to candle O/H/L/C"
        className={cn(
          "focus-ring grid size-9 place-items-center rounded-md transition-colors [&_svg]:size-[18px]",
          magnetOn ? "bg-accent/15 text-accent" : "text-fg-muted hover:bg-white/5 hover:text-fg",
        )}
      >
        <MagnetStraight weight={magnetOn ? "fill" : "regular"} />
      </button>
      <button
        onClick={() => setLocked(!locked)}
        aria-label="Lock drawings"
        title="Lock existing drawings"
        className={cn(
          "focus-ring grid size-9 place-items-center rounded-md transition-colors [&_svg]:size-[18px]",
          locked ? "bg-accent/15 text-accent" : "text-fg-muted hover:bg-white/5 hover:text-fg",
        )}
      >
        <Lock weight={locked ? "fill" : "regular"} />
      </button>
      <button
        onClick={() => setHidden(!hidden)}
        aria-label="Hide drawings"
        title="Hide drawings"
        className={cn(
          "focus-ring grid size-9 place-items-center rounded-md transition-colors [&_svg]:size-[18px]",
          hidden ? "bg-accent/15 text-accent" : "text-fg-muted hover:bg-white/5 hover:text-fg",
        )}
      >
        {hidden ? <EyeSlash weight="fill" /> : <Eye />}
      </button>

      <DropdownMenu.Root open={confirmingClear} onOpenChange={setConfirmingClear}>
        <DropdownMenu.Trigger asChild>
          <button
            aria-label="Remove all drawings"
            title="Remove all drawings"
            disabled={count === 0}
            className="focus-ring grid size-9 place-items-center rounded-md text-fg-muted transition-colors hover:bg-white/5 hover:text-fg disabled:opacity-30 [&_svg]:size-[18px]"
          >
            <Trash />
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            side="right"
            align="end"
            sideOffset={10}
            className="glass-strong z-[60] w-56 rounded-lg p-3 shadow-xl"
          >
            <p className="text-[13px] text-fg-muted">
              Delete all {count} drawing{count === 1 ? "" : "s"}? This cannot be undone.
            </p>
            <div className="mt-2.5 flex justify-end gap-1.5">
              <DropdownMenu.Item
                onSelect={(e) => e.preventDefault()}
                onClick={() => setConfirmingClear(false)}
                className="cursor-pointer rounded-md px-2.5 py-1 text-[12px] font-medium text-fg-muted outline-none hover:bg-white/5"
              >
                Cancel
              </DropdownMenu.Item>
              <DropdownMenu.Item
                onSelect={() => drawings.clear()}
                className="cursor-pointer rounded-md bg-down/15 px-2.5 py-1 text-[12px] font-medium text-down outline-none hover:bg-down/25"
              >
                Delete all
              </DropdownMenu.Item>
            </div>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </div>
  );
}

// Re-exported so other surfaces (e.g. a future keyboard-shortcut help panel)
// can know which rail tool ids actually draw something today.
export const WIRED_TOOL_IDS = Object.keys(TOOL_TO_KIND);
