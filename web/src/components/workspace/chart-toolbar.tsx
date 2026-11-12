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
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { useAuth } from "@/auth/auth-context";
import { useDrawing } from "@/components/chart/drawing-context";
import { useAnalysisWorkspace } from "@/components/workspace/analysis-workspace";
import { useCommandPalette } from "@/components/workspace/command-palette";
import { useArgusActivity } from "@/components/workspace/argus-activity-feed";
import { fixturesEnabled } from "@/mocks/fixtures-as-api";
import type { ApiInstrument } from "@/api/types";
import type { CandleInterval } from "@/lib/live-socket";

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

const TIMEFRAMES: CandleInterval[] = ["1m", "5m", "15m", "1h", "4h", "1d"];

// Real timeframe controls (live-chart brief, phase 4, Part 4, item 1):
// useLiveCandles now subscribes with whatever interval is selected, not
// a fixed "1m" (analysis-workspace.tsx's own doc comment on `timeframe`
// explains why that state lives there, not in DrawingProvider). Which
// buttons are enabled comes from the instrument's own
// supported_intervals -- the backend's authoritative list (ADR-006),
// never a client-side crypto/non-crypto guess -- so a non-crypto
// instrument that only has ["1m"] shows every other button disabled,
// without this component needing to know why. No instrument resolved
// yet: nothing is known to be supported, so everything but 1m is
// disabled (matching this control's own pre-Part-4 default).
function TimeframePicker({ tf, instrument }: { tf: CandleInterval; instrument?: ApiInstrument }) {
  const { setTimeframe } = useDrawing();
  const supported = instrument?.supported_intervals ?? ["1m"];
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          aria-label={`Timeframe: ${tf}`}
          className="focus-ring inline-flex h-7 items-center gap-1 rounded-md px-2 text-[13px] font-semibold text-fg transition-colors hover:bg-white/5 data-[state=open]:bg-white/5"
        >
          {tf}
          <CaretDown className="size-3.5 text-fg-subtle" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={6}
          className="glass-strong z-[60] min-w-[120px] overflow-y-auto rounded-lg p-1 shadow-xl"
        >
          {TIMEFRAMES.map((candidate) => {
            const disabled = !supported.includes(candidate);
            const item = (
              <DropdownMenu.Item
                key={candidate}
                disabled={disabled}
                onSelect={() => setTimeframe(candidate)}
                className={cn(
                  "flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none transition-colors",
                  "data-[highlighted]:bg-white/5",
                  disabled
                    ? "cursor-not-allowed text-fg-subtle opacity-50 data-[highlighted]:bg-transparent"
                    : candidate === tf
                      ? "text-fg"
                      : "text-fg-muted data-[highlighted]:text-fg",
                )}
              >
                <Check className={cn("size-3.5 shrink-0", candidate === tf ? "opacity-100" : "opacity-0")} />
                <span className="font-medium">{candidate}</span>
              </DropdownMenu.Item>
            );
            // Radix disables pointer events on a disabled Item, which
            // also suppresses this Tooltip's own hover trigger if it
            // wraps the Item directly -- wrapping in a span keeps the
            // explanation reachable on hover even though the item
            // itself cannot be selected.
            return disabled ? (
              <Tooltip key={candidate} content="This instrument's own backend data does not support this timeframe">
                <span>{item}</span>
              </Tooltip>
            ) : (
              item
            );
          })}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

// Lists every real instrument (GET /instruments, via the workspace) and
// switches the selected one on click -- navigating to
// /analysis?instrumentId=<id> re-enters exactly the URL-driven resolution
// analysis-workspace.tsx's own setup-selection effect already implements
// (clearing setupId falls through to that instrument's own most-recently-
// updated setup), so no separate instrument-switching logic is needed
// here.
function SymbolPicker({ instrument, instruments }: { instrument?: ApiInstrument; instruments: ApiInstrument[] }) {
  const navigate = useNavigate();
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button className="focus-ring flex items-center gap-2 rounded-md px-2 py-1 outline-none transition-colors hover:bg-white/5 data-[state=open]:bg-white/5">
          <CoinIcon symbol={instrument?.native_symbol ?? "?"} size={20} />
          <span className="text-sm font-semibold">{instrument?.native_symbol ?? "Select instrument"}</span>
          <CaretDown className="size-3.5 text-fg-subtle" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={6}
          className="glass-strong z-[60] max-h-[320px] min-w-[220px] overflow-y-auto rounded-lg p-1 shadow-xl"
        >
          {instruments.map((inst) => (
            <DropdownMenu.Item
              key={inst.id}
              onSelect={() => navigate(`/analysis?instrumentId=${inst.id}`)}
              className={cn(
                "flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none transition-colors",
                "data-[highlighted]:bg-white/5",
                inst.id === instrument?.id ? "text-fg" : "text-fg-muted data-[highlighted]:text-fg",
              )}
            >
              <Check className={cn("size-3.5 shrink-0", inst.id === instrument?.id ? "opacity-100" : "opacity-0")} />
              <CoinIcon symbol={inst.native_symbol} size={16} />
              <span className="font-medium">{inst.native_symbol}</span>
              <span className="ml-auto text-[11px] text-fg-subtle">{inst.provider}</span>
            </DropdownMenu.Item>
          ))}
          {instruments.length === 0 && (
            <div className="px-2.5 py-2 text-[12px] text-fg-subtle">No instruments registered.</div>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

// The analysis workspace's single top bar: ARGUS identity and nav on the left,
// then the chart controls, then the chart actions and account on the right.
export function ChartToolbar() {
  const { timeframe: tf, drawings } = useDrawing();
  const { instrument, instruments } = useAnalysisWorkspace();
  const { user } = useAuth();
  const navigate = useNavigate();
  const palette = useCommandPalette();
  const activity = useArgusActivity();
  const initial = (user?.display_name || user?.email)?.[0]?.toUpperCase() ?? "?";
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

      <SymbolPicker instrument={instrument} instruments={instruments} />

      <Divider />

      <TimeframePicker tf={tf} instrument={instrument} />

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
        {fixturesEnabled() && (
          <>
            <Divider />
            <Button
              variant="glass"
              size="sm"
              className="gap-1.5"
              onClick={activity.replay}
              title="Replay ARGUS's analysis steps (sample data)"
            >
              <ClockCounterClockwise className="size-4 text-accent" /> Replay
            </Button>
          </>
        )}
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
          {initial}
        </span>
      </div>
    </div>
  );
}
