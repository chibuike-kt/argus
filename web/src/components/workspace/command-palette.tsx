import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useNavigate } from "react-router-dom";
import { MagnifyingGlass, ArrowBendDownLeft } from "@phosphor-icons/react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { markets } from "@/mocks/fixtures";
import { cn } from "@/lib/cn";

interface Command {
  id: string;
  label: string;
  hint?: string;
  symbol?: string;
  onRun: (navigate: ReturnType<typeof useNavigate>) => void;
}

// Static nav commands + the watchlist's own symbols -- a real symbol swap
// (routing the analysis view to a chosen instrument) is a follow-up once
// the chart reads its instrument from route state rather than a fixture
// constant; for now, selecting a symbol here goes to Analysis.
const NAV_COMMANDS: Command[] = [
  { id: "nav-setups", label: "Go to Setups", hint: "Setups", onRun: (nav) => nav("/setups") },
  {
    id: "nav-analysis",
    label: "Go to Analysis",
    hint: "Analysis",
    onRun: (nav) => nav("/analysis"),
  },
  { id: "nav-style", label: "Go to Design system", hint: "Style", onRun: (nav) => nav("/style") },
];

const SYMBOL_COMMANDS: Command[] = markets.map((m) => ({
  id: `sym-${m.symbol}`,
  label: m.name,
  hint: m.symbol.replace(".P", ""),
  symbol: m.symbol,
  onRun: (nav) => nav("/analysis"),
}));

const ALL_COMMANDS = [...NAV_COMMANDS, ...SYMBOL_COMMANDS];

interface PaletteContextValue {
  open: () => void;
}

const PaletteContext = createContext<PaletteContextValue | null>(null);

// Global ⌘K / Ctrl+K navigation and symbol search -- the analysis workspace
// has no nav tabs or logo header, so this (plus the toolbar's own search
// trigger and a back-to-setups control) is how the terminal is navigated.
export function CommandPaletteProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState(0);
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (open) {
      setQuery("");
      setIndex(0);
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return ALL_COMMANDS;
    return ALL_COMMANDS.filter(
      (c) =>
        c.label.toLowerCase().includes(q) ||
        c.hint?.toLowerCase().includes(q) ||
        c.symbol?.toLowerCase().includes(q),
    );
  }, [query]);

  const run = (c: Command) => {
    c.onRun(navigate);
    setOpen(false);
  };

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
      else if (e.key === "ArrowDown") {
        e.preventDefault();
        setIndex((i) => Math.min(i + 1, results.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setIndex((i) => Math.max(i - 1, 0));
      } else if (e.key === "Enter" && results[index]) {
        e.preventDefault();
        run(results[index]);
      }
    };
    window.addEventListener("pointerdown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("pointerdown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open, results, index]);

  return (
    <PaletteContext.Provider value={{ open: () => setOpen(true) }}>
      {children}
      {open && (
        <div className="fixed inset-0 z-[100] flex items-start justify-center bg-black/50 pt-[12vh]">
          <div
            ref={rootRef}
            className="argus-in glass-strong w-full max-w-[480px] overflow-hidden rounded-xl shadow-2xl"
          >
            <div className="flex items-center gap-2.5 border-b border-line px-3.5 py-2.5">
              <MagnifyingGlass className="size-4 text-fg-subtle" />
              <input
                ref={inputRef}
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setIndex(0);
                }}
                placeholder="Jump to a symbol or page…"
                className="flex-1 bg-transparent text-[13px] text-fg outline-none placeholder:text-fg-subtle"
              />
              <kbd className="rounded border border-line px-1.5 py-0.5 text-[10px] text-fg-subtle">
                Esc
              </kbd>
            </div>

            <div className="max-h-[320px] overflow-y-auto p-1.5">
              {results.length === 0 && (
                <p className="px-2.5 py-3 text-[13px] text-fg-subtle">No matches</p>
              )}
              {results.map((c, i) => (
                <button
                  key={c.id}
                  onClick={() => run(c)}
                  onMouseEnter={() => setIndex(i)}
                  className={cn(
                    "flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-[13px] transition-colors",
                    i === index ? "bg-white/10 text-fg" : "text-fg-muted hover:bg-white/5",
                  )}
                >
                  {c.symbol ? (
                    <CoinIcon symbol={c.symbol} size={18} />
                  ) : (
                    <span className="size-[18px]" />
                  )}
                  <span className="flex-1 font-medium">{c.label}</span>
                  <span className="text-[11px] text-fg-subtle">{c.hint}</span>
                  {i === index && <ArrowBendDownLeft className="size-3.5 text-fg" />}
                </button>
              ))}
            </div>
          </div>
        </div>
      )}
    </PaletteContext.Provider>
  );
}

export function useCommandPalette(): PaletteContextValue {
  const ctx = useContext(PaletteContext);
  if (!ctx) throw new Error("useCommandPalette must be used within CommandPaletteProvider");
  return ctx;
}
