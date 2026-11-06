import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  Broadcast,
  ChartLineUp,
  Scales,
  ClipboardText,
  CheckCircle,
  ArrowsClockwise,
  type Icon,
} from "@phosphor-icons/react";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { annotations, setup, dataQuality } from "@/mocks/fixtures";
import { formatUTC } from "@/lib/format";
import type { Provenance } from "@/types/domain";
import { cn } from "@/lib/cn";

const fmt = (p: number) => Math.round(p).toLocaleString("en-US");

// Narrates ARGUS's own evaluation steps as a sequence of toasts -- built
// from the same fixtures already drawn on the chart (structure, the sweep
// and CHOCH events, the order block, equilibrium, the plan), not invented
// copy, so what scrolls past always matches what is actually on screen.
// This is ARGUS's own voice (the same role as its chart marks), which is
// why the tray reads in the indigo accent, not the neutral chrome v3 just
// pulled everything else into.
interface Step {
  icon: Icon;
  prov: Provenance;
  text: ReactNode;
}

function buildScript(): Step[] {
  const sweep = annotations.events.find((e) => e.label === "Sweep");
  const choch = annotations.events.find((e) => e.label === "CHOCH");
  const lastSwing = annotations.swings[annotations.swings.length - 1];
  const bull = setup.direction === "bullish";

  const steps: Step[] = [
    {
      icon: Broadcast,
      prov: "S",
      text: (
        <>
          Data verified — finalized through{" "}
          <span className="tnum">{formatUTC(dataQuality.finalizedThrough)}</span>
        </>
      ),
    },
    {
      icon: ChartLineUp,
      prov: "S",
      text: (
        <>
          Structure read — {annotations.swings.length} confirmed swings, latest {lastSwing.kind} at{" "}
          <span className="tnum">{fmt(lastSwing.price)}</span>
        </>
      ),
    },
  ];

  if (sweep) {
    steps.push({
      icon: ArrowsClockwise,
      prov: "S",
      text: (
        <>
          Liquidity swept at <span className="tnum">{fmt(sweep.price)}</span> and reclaimed
        </>
      ),
    });
  }
  if (choch) {
    steps.push({
      icon: ChartLineUp,
      prov: "S",
      text: (
        <>
          CHOCH confirmed at <span className="tnum">{fmt(choch.price)}</span> — bias flips{" "}
          {bull ? "bullish" : "bearish"}
        </>
      ),
    });
  }

  steps.push(
    {
      icon: Scales,
      prov: "A",
      text: (
        <>
          Order block marked{" "}
          <span className="tnum">
            {fmt(annotations.orderBlock.low)}–{fmt(annotations.orderBlock.high)}
          </span>
        </>
      ),
    },
    {
      icon: Scales,
      prov: "A",
      text: (
        <>
          Equilibrium set at <span className="tnum">{fmt(annotations.range.equilibrium)}</span> —
          premium/discount split
        </>
      ),
    },
    {
      icon: ClipboardText,
      prov: "A",
      text: (
        <>
          Plan drafted — {bull ? "long" : "short"} entry{" "}
          <span className="tnum">{fmt(setup.entry)}</span>, stop{" "}
          <span className="tnum">{fmt(setup.stop)}</span>,{" "}
          <span className="tnum">{setup.rr.toFixed(1)}R</span>
        </>
      ),
    },
    {
      icon: CheckCircle,
      prov: "S",
      text: <>Analysis complete — setup is live</>,
    },
  );

  return steps;
}

interface Toast extends Step {
  id: number;
  leaving: boolean;
}

const STEP_DELAY_MS = 1100;
const TOAST_LIFETIME_MS = 4500;
const EXIT_DURATION_MS = 280;

interface ActivityContextValue {
  replay: () => void;
}

const ActivityContext = createContext<ActivityContextValue | null>(null);

export function ArgusActivityProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(0);
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);

  const clearTimers = () => {
    timers.current.forEach(clearTimeout);
    timers.current = [];
  };

  const dismiss = useCallback((id: number) => {
    setToasts((ts) => ts.map((t) => (t.id === id ? { ...t, leaving: true } : t)));
    const removeAt = setTimeout(() => {
      setToasts((ts) => ts.filter((t) => t.id !== id));
    }, EXIT_DURATION_MS);
    timers.current.push(removeAt);
  }, []);

  const replay = useCallback(() => {
    clearTimers();
    setToasts([]);
    const script = buildScript();
    script.forEach((step, i) => {
      const addAt = setTimeout(() => {
        const id = nextId.current++;
        setToasts((ts) => [...ts, { ...step, id, leaving: false }]);
        const dismissAt = setTimeout(() => dismiss(id), TOAST_LIFETIME_MS);
        timers.current.push(dismissAt);
      }, i * STEP_DELAY_MS);
      timers.current.push(addAt);
    });
  }, [dismiss]);

  // Plays once on mount -- the analysis workspace feels like ARGUS is
  // already working the moment you open it, not just when asked to.
  // React 18 StrictMode double-invokes this in dev (mount, cleanup,
  // mount again); the cleanup clears the pending timer so only the
  // surviving mount's sequence actually plays.
  useEffect(() => {
    const startAt = setTimeout(replay, 500); // deferred so it reads as "connecting", not instant
    return () => {
      clearTimeout(startAt);
      // Also clears every step/dismiss timer still pending, so navigating
      // away mid-sequence never fires a state update after unmount.
      clearTimers();
    };
    // replay is intentionally omitted: it is a stable useCallback (its own
    // dep, dismiss, is itself stable), so this only runs on mount/unmount.
  }, []);

  return (
    <ActivityContext.Provider value={{ replay }}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-[340px] flex-col-reverse gap-2">
        {toasts.map((t) => {
          const Icon = t.icon;
          return (
            <div
              key={t.id}
              className={cn(
                "glass-accent pointer-events-auto flex items-start gap-2.5 rounded-lg px-3 py-2.5 shadow-xl",
                t.leaving ? "argus-out" : "argus-in",
              )}
            >
              <Icon weight="duotone" className="mt-0.5 size-4 shrink-0 text-accent" />
              <p className="flex-1 text-[12px] leading-snug text-fg">{t.text}</p>
              <ProvenanceChip p={t.prov} className="mt-0.5" />
            </div>
          );
        })}
      </div>
    </ActivityContext.Provider>
  );
}

export function useArgusActivity(): ActivityContextValue {
  const ctx = useContext(ActivityContext);
  if (!ctx) throw new Error("useArgusActivity must be used within ArgusActivityProvider");
  return ctx;
}
