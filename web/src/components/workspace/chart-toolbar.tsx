import { type ReactNode } from "react";
import { NavLink, Link } from "react-router-dom";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  ChevronDown,
  CandlestickChart,
  SlidersHorizontal,
  Bell,
  History,
  Undo2,
  Redo2,
  Camera,
  Settings,
  Check,
} from "lucide-react";
import { IconButton } from "@/components/ui/icon-button";
import { Button } from "@/components/ui/button";
import { CoinIcon } from "@/components/ui/coin-icon";
import { instrument } from "@/mocks/fixtures";
import { cn } from "@/lib/cn";
import { useDrawing } from "@/components/chart/drawing-context";

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

const NAV = [
  { to: "/analysis", label: "Analysis" },
  { to: "/setups", label: "Setups" },
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
          <ChevronDown className="size-3.5 text-fg-subtle" />
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
                    value === tf ? "text-accent" : "text-fg-muted data-[highlighted]:text-fg",
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
  return (
    <div className="glass relative z-30 flex items-center gap-1.5 rounded-xl px-2 py-1.5">
      <Link to="/analysis" className="flex items-center gap-2 pl-1">
        <span className="grid size-8 place-items-center rounded-md bg-accent text-accent-foreground">
          <span className="font-display text-lg leading-none">A</span>
        </span>
        <span className="font-display hidden text-lg leading-none tracking-wide sm:inline">
          ARGUS
        </span>
      </Link>

      <nav className="ml-1 hidden items-center gap-0.5 xl:flex">
        {NAV.map((nv) => (
          <NavLink
            key={nv.to}
            to={nv.to}
            className={({ isActive }) =>
              cn(
                "inline-flex h-7 items-center rounded-md px-2.5 text-[13px] font-medium transition-colors",
                isActive ? "bg-white/10 text-fg" : "text-fg-muted hover:text-fg",
              )
            }
          >
            {nv.label}
          </NavLink>
        ))}
      </nav>

      <Divider />

      <button className="focus-ring flex items-center gap-2 rounded-md px-2 py-1 hover:bg-white/5">
        <CoinIcon symbol={instrument.symbol} size={20} />
        <span className="text-sm font-semibold">{instrument.symbol}</span>
        <ChevronDown className="size-3.5 text-fg-subtle" />
      </button>

      <Divider />

      <IntervalMenu tf={tf} onSelect={setTf} />

      <Divider />

      <ToolbarBtn icon={<CandlestickChart />} label="Chart type" />
      <ToolbarBtn icon={<SlidersHorizontal />} label="Indicators" text="Indicators" />
      <ToolbarBtn icon={<Bell />} label="Alert" text="Alert" />

      <div className="ml-auto flex items-center gap-1.5">
        <Button variant="glass" size="sm" className="gap-1.5">
          <History className="size-4 text-accent" /> Replay
        </Button>
        <IconButton
          variant="ghost"
          size="sm"
          aria-label="Undo"
          disabled={!drawings.canUndo}
          onClick={() => drawings.undo()}
        >
          <Undo2 />
        </IconButton>
        <IconButton
          variant="ghost"
          size="sm"
          aria-label="Redo"
          disabled={!drawings.canRedo}
          onClick={() => drawings.redo()}
        >
          <Redo2 />
        </IconButton>
        <IconButton variant="ghost" size="sm" aria-label="Screenshot">
          <Camera />
        </IconButton>
        <Divider />
        <IconButton variant="ghost" size="sm" aria-label="Settings">
          <Settings />
        </IconButton>
        <span className="ml-0.5 grid size-8 place-items-center rounded-full bg-surface-2 text-[13px] font-semibold text-fg-muted">
          K
        </span>
      </div>
    </div>
  );
}
