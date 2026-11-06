import { useState, type ReactNode } from "react";
import { Plus, Columns, DotsThree, CaretDown, CircleNotch } from "@phosphor-icons/react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { SampleDataBadge } from "@/components/ui/sample-data-badge";
import { useInstruments } from "@/api/hooks";
import { cn } from "@/lib/cn";

// No forex instrument exists in the dev-fixture dataset this is verified
// against, and the API has no forex-specific endpoint beyond the same
// GET /instruments every asset class shares -- this group stays fixture
// data entirely, marked.
const forex = [
  { symbol: "GBPUSD", last: 1.34132, changePct: 0.29 },
  { symbol: "AUDUSD", last: 0.70236, changePct: -0.07 },
  { symbol: "EURGBP", last: 0.8461, changePct: -0.08 },
];

const COLS = "grid grid-cols-[1.3fr_1fr_0.9fr_0.8fr] items-center gap-2";

function fmtNum(v: number) {
  const d = Math.abs(v) < 10 ? 4 : Math.abs(v) < 1000 ? 2 : 1;
  return v.toLocaleString("en-US", { minimumFractionDigits: d, maximumFractionDigits: d });
}

function IconBtn({ children }: { children: ReactNode }) {
  return (
    <button className="focus-ring grid size-7 place-items-center rounded-md transition-colors hover:bg-white/5 hover:text-fg">
      {children}
    </button>
  );
}

// last/changePct are optional: GET /instruments carries no price, and
// deriving "last" per watchlist row would mean one extra candles
// request per instrument, which is not "trivial" for a list of any real
// size (the wiring brief's own bar for when to derive it instead of
// marking it sample). undefined renders as an em dash, never a
// fabricated 0.
function Row({ symbol, last, changePct }: { symbol: string; last?: number; changePct?: number }) {
  const up = (changePct ?? 0) >= 0;
  const chg = last !== undefined && changePct !== undefined ? (last * changePct) / 100 : undefined;
  const tone = changePct === undefined ? "text-fg-subtle" : up ? "text-up" : "text-down";
  return (
    <button
      className={cn(
        "focus-ring w-full rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-white/5",
        COLS,
      )}
    >
      <span className="flex items-center gap-2 overflow-hidden">
        <CoinIcon symbol={symbol} size={20} />
        <span className="truncate text-[13px] font-medium">{symbol.replace(".P", "")}</span>
      </span>
      <span className="tnum text-right text-[13px]">{last !== undefined ? fmtNum(last) : "—"}</span>
      <span className={cn("tnum text-right text-[12px]", tone)}>
        {chg !== undefined ? `${chg >= 0 ? "+" : ""}${fmtNum(chg)}` : "—"}
      </span>
      <span className={cn("tnum text-right text-[12px]", tone)}>
        {changePct !== undefined ? `${up ? "+" : ""}${changePct.toFixed(2)}%` : "—"}
      </span>
    </button>
  );
}

function Group({ title, sample, children }: { title: string; sample?: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(true);
  return (
    <div>
      <div className="flex items-center justify-between px-2">
        <button
          onClick={() => setOpen(!open)}
          className="focus-ring flex items-center gap-1 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle transition-colors hover:text-fg-muted"
        >
          <CaretDown className={cn("size-3 transition-transform", !open && "-rotate-90")} />
          {title}
        </button>
        {sample && <SampleDataBadge />}
      </div>
      {open && <div className="space-y-0.5">{children}</div>}
    </div>
  );
}

export function Watchlist() {
  const { data: instruments, loading, error } = useInstruments();

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* title row: pinned */}
      <div className="flex shrink-0 items-center justify-between border-b border-line px-3 py-2.5">
        <button className="focus-ring flex items-center gap-1 text-sm font-semibold">
          Watchlist <CaretDown className="size-3.5 text-fg-subtle" />
        </button>
        <div className="flex items-center gap-0.5 text-fg-subtle">
          <IconBtn>
            <Plus className="size-4" />
          </IconBtn>
          <IconBtn>
            <Columns className="size-4" />
          </IconBtn>
          <IconBtn>
            <DotsThree className="size-4" />
          </IconBtn>
        </div>
      </div>

      {/* column header: pinned */}
      <div
        className={cn(
          "shrink-0 border-b border-line px-2 py-1.5 text-[11px] font-medium text-fg-subtle",
          COLS,
        )}
      >
        <span>Symbol</span>
        <span className="text-right">Last</span>
        <span className="text-right">Chg</span>
        <span className="text-right">Chg%</span>
      </div>

      {/* rows: the only scroll region */}
      <div className="min-h-0 flex-1 overflow-y-auto py-1">
        <Group title="Crypto">
          {loading && (
            <div className="flex items-center gap-2 px-2 py-2 text-[12px] text-fg-subtle">
              <CircleNotch className="size-3.5 animate-spin" />
              Loading…
            </div>
          )}
          {error && <div className="px-2 py-2 text-[12px] text-down">{error.message}</div>}
          {!loading &&
            !error &&
            (instruments ?? []).map((inst) => (
              // Symbols are real (GET /instruments); last/Chg/Chg% are
              // not shown at all for them -- no fabricated price, an em
              // dash instead (Row's own default when last is omitted).
              <Row key={inst.id} symbol={inst.native_symbol} />
            ))}
          {!loading && !error && (instruments ?? []).length === 0 && (
            <div className="px-2 py-2 text-[12px] text-fg-subtle">No instruments registered.</div>
          )}
        </Group>
        <Group title="Forex" sample>
          {forex.map((f) => (
            <Row key={f.symbol} symbol={f.symbol} last={f.last} changePct={f.changePct} />
          ))}
        </Group>
      </div>
    </div>
  );
}
