import { type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  CaretDown,
  ChartBar,
  SlidersHorizontal,
  Bell,
  ClockCounterClockwise,
  ArrowUUpLeft,
  ArrowUUpRight,
  Camera,
  Gear,
  Check,
  ArrowLeft,
  MagnifyingGlass,
} from "@phosphor-icons/react";
import { IconButton } from "@/components/ui/icon-button";
import { Button } from "@/components/ui/button";
import { CoinIcon } from "@/components/ui/coin-icon";
import { instrument } from "@/mocks/fixtures";
import { cn } from "@/lib/cn";
import { useDrawing } from "@/components/chart/drawing-context";
import { useCommandPalette } from "@/components/workspace/command-palette";
import { useArgusActivity } from "@/components/workspace/argus-activity-feed";

const INTERVAL_GROUPS: { label: string; items: [string, string][] }[] = [
  {
    label: "Minutes",
    items: [
      ["1m", "1 minute"],
      ["3m", "3 minutes"],
      ["5m", "5 minutes"],
      ["15m", "15 minutes"],
      ["30m", "30 minutes"],
    ],
  },
  {
    label: "Hours",
    items: [
      ["1H", "1 hour"],
      ["4H", "4 hours"],
    ],
  },
  {
    label: "Days",
    items: [
      ["1D", "1 day"],
      ["1W", "1 week"],
    ],
  },
];

function Divider() {
  return <span className="mx-0.5 h-5 w-px bg-line" />;
}

function ToolbarBtn({ icon, label, text }: { icon: ReactNode; label: string; text?: string }) {
  return (
    <button
      aria-label={label}
      title={label}
      className="focus-ring inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[13px] font-medium text-fg-muted transition-colors hover:bg-white/5 hover:text-fg [&_svg]:size-4"
    >
      {icon}
      {text && <span className="hidden lg:inline">{text}</span>}
    </button>
  );
}

function IntervalMenu({ tf, onSelect }: { tf: string; onSelect: (v: string) => void }) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          aria-label="Interval"
          title="Interval"
          className="focus-ring inline-flex h-7 items-center gap-1 rounded-md px-2 text-[13px] font-semibold text-fg outline-none transition-colors hover:bg-white/5 data-[state=open]:bg-white/5"
        >
          {tf}
          <CaretDown className="size-3.5 text-fg-subtle" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={6}
          className="glass-strong z-[60] min-w-[190px] rounded-lg p-1 shadow-xl"
        >
          {INTERVAL_GROUPS.map((g, gi) => (
            <DropdownMenu.Group key={g.label}>
              {gi > 0 && <DropdownMenu.Separator className="my-1 h-px bg-line" />}
              <DropdownMenu.Label className="px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-fg-subtle">
                {g.label}
              </DropdownMenu.Label>
              {g.items.map(([value, label]) => (
                <DropdownMenu.Item
                  key={value}
                  onSelect={() => onSelect(value)}
                  className={cn(
                    "flex cursor-pointer items-center justify-between rounded-md px-2.5 py-1.5 text-[13px] outline-none transition-colors",
                    "data-[highlighted]:bg-white/5",
                    value === tf ? "text-fg" : "text-fg-muted data-[highlighted]:text-fg",
                  )}
                >
                  <span className="flex items-center gap-2">
                    <Check className={cn("size-3.5", value === tf ? "opacity-100" : "opacity-0")} />
                    <span className="font-medium">{value}</span>
                  </span>
                  <span className="text-[11px] text-fg-subtle">{label}</span>
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.Group>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

// The analysis workspace's single top bar: ARGUS identity and nav on the left,
// then the chart controls, then the chart actions and account on the right.
export function ChartToolbar() {
  const { timeframe: tf, setTimeframe: setTf, drawings } = useDrawing();
  const navigate = useNavigate();
  const palette = useCommandPalette();
  const activity = useArgusActivity();
  return (
    <div className="argus-in glass relative z-30 flex items-center gap-1.5 rounded-xl px-2 py-1.5">
      <IconButton
        variant="ghost"
        size="sm"
        aria-label="Back to setups"
        onClick={() => navigate("/setups")}
      >
        <ArrowLeft />
      </IconButton>

      <Divider />

      <button className="focus-ring flex items-center gap-2 rounded-md px-2 py-1 hover:bg-white/5">
        <CoinIcon symbol={instrument.symbol} size={20} />
        <span className="text-sm font-semibold">{instrument.symbol}</span>
        <CaretDown className="size-3.5 text-fg-subtle" />
      </button>

      <Divider />

      <IntervalMenu tf={tf} onSelect={setTf} />

      <Divider />

      <ToolbarBtn icon={<ChartBar />} label="Chart type" />
      <ToolbarBtn icon={<SlidersHorizontal />} label="Indicators" text="Indicators" />
      <ToolbarBtn icon={<Bell />} label="Alert" text="Alert" />

      <div className="ml-auto flex items-center gap-1.5">
        <button
          onClick={palette.open}
          aria-label="Search (Ctrl+K)"
          title="Jump to a symbol or page"
          className="focus-ring inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[13px] text-fg-subtle transition-colors hover:bg-white/5 hover:text-fg"
        >
          <MagnifyingGlass className="size-4" />
          <kbd className="hidden rounded border border-line px-1.5 py-0.5 text-[10px] lg:inline">
            Ctrl K
          </kbd>
        </button>
        <Divider />
        <Button
          variant="glass"
          size="sm"
          className="gap-1.5"
          onClick={activity.replay}
          title="Replay ARGUS's analysis steps"
        >
          <ClockCounterClockwise className="size-4 text-accent" /> Replay
        </Button>
        <IconButton
          variant="ghost"
          size="sm"
          aria-label="Undo"
          disabled={!drawings.canUndo}
          onClick={() => drawings.undo()}
        >
          <ArrowUUpLeft />
        </IconButton>
        <IconButton
          variant="ghost"
          size="sm"
          aria-label="Redo"
          disabled={!drawings.canRedo}
          onClick={() => drawings.redo()}
        >
          <ArrowUUpRight />
        </IconButton>
        <IconButton variant="ghost" size="sm" aria-label="Screenshot">
          <Camera />
        </IconButton>
        <Divider />
        <IconButton variant="ghost" size="sm" aria-label="Settings">
          <Gear />
        </IconButton>
        <span className="ml-0.5 grid size-8 place-items-center rounded-full bg-surface-2 text-[13px] font-semibold text-fg-muted">
          K
        </span>
      </div>
    </div>
  );
}
