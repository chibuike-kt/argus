import type { ReactNode } from "react";
import { Bell, Plus, MagnifyingGlass, SlidersHorizontal } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { IconButton } from "@/components/ui/icon-button";
import { Panel } from "@/components/ui/panel";
import { Pill } from "@/components/ui/pill";
import { Stat } from "@/components/ui/stat";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { CoinIcon } from "@/components/ui/coin-icon";
import { TabsRoot, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { Provenance } from "@/types/domain";
import { cn } from "@/lib/cn";

function Section({ title, desc, children }: { title: string; desc?: string; children: ReactNode }) {
  return (
    <section className="space-y-4 border-t border-line pt-8 first:border-0 first:pt-0">
      <div>
        <h2 className="text-xs font-semibold uppercase tracking-[0.12em] text-fg-subtle">
          {title}
        </h2>
        {desc && <p className="mt-1.5 max-w-2xl text-sm text-fg-muted">{desc}</p>}
      </div>
      {children}
    </section>
  );
}

function Swatch({ name, hex, cls }: { name: string; hex: string; cls: string }) {
  return (
    <div className="space-y-2">
      <div className={cn("h-16 rounded-lg border border-line", cls)} />
      <div>
        <div className="text-xs font-medium">{name}</div>
        <div className="tnum text-[11px] uppercase text-fg-subtle">{hex}</div>
      </div>
    </div>
  );
}

const PROV: { p: Provenance; name: string }[] = [
  { p: "S", name: "Source-confirmed" },
  { p: "A", name: "Architectural" },
  { p: "R", name: "Recommendation" },
  { p: "U", name: "Unresolved" },
];

export function StyleGuide() {
  return (
    <div className="mx-auto h-full max-w-[1180px] space-y-10 overflow-y-auto px-4 py-8 sm:px-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">ARGUS design system</h1>
        <p className="max-w-2xl text-fg-muted">
          A trading-intelligence terminal: true black, flat segmented cards, and colour that only
          ever means something -- the market speaks green/red, ARGUS's own marks on the chart speak
          the indigo accent, and generic chrome (tabs, the active tool, focus) stays neutral.
          Typeface is Montserrat with IBM Plex Mono for every number, and Astron Valley reserved for
          the ARGUS logo mark and wordmark.
        </p>
      </header>

      <Section
        title="Typography"
        desc="Montserrat across the interface. Every number -- prices, times, deltas -- renders in IBM Plex Mono via the .tnum class, with tabular figures so they stay aligned under live updates."
      >
        <Panel variant="strong" pad="lg" className="space-y-6">
          <div className="text-6xl font-semibold tracking-tight">Montserrat</div>
          <div className="flex flex-wrap items-end gap-x-6 gap-y-2">
            <span className="text-2xl font-normal">Regular 400</span>
            <span className="text-2xl font-medium">Medium 500</span>
            <span className="text-2xl font-semibold">Semibold 600</span>
            <span className="text-2xl font-bold">Bold 700</span>
          </div>
          <div className="tnum text-3xl font-semibold text-accent">
            68,142.50 <span className="text-fg-subtle">/</span> +0.84%{" "}
            <span className="text-fg-subtle">/</span> 14:30 UTC
          </div>
          <div className="border-t border-line pt-4">
            <div className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
              Astron Valley, logo only
            </div>
            <div className="font-display mt-1.5 text-4xl tracking-wide">ARGUS</div>
          </div>
        </Panel>
      </Section>

      <Section
        title="Color"
        desc="Two colour languages, kept apart: emerald/coral for the market itself (candles, watchlist, performance), indigo for ARGUS's own intelligence (POI, plan, bias, structure, plus focus/active/selection). One hue per provenance tag."
      >
        <div className="space-y-6">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
            <Swatch name="Base" hex="#000000" cls="bg-base" />
            <Swatch name="Surface" hex="#0A0A0A" cls="bg-surface" />
            <Swatch name="Surface 2" hex="#111111" cls="bg-surface-2" />
            <Swatch name="Elevated" hex="#171717" cls="bg-elevated" />
            <Swatch name="Grey" hex="#3A3F47" cls="bg-grey" />
          </div>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
            <Swatch name="Accent (ARGUS)" hex="#6D7BFF" cls="bg-accent" />
            <Swatch name="Accent strong" hex="#5561E6" cls="bg-accent-strong" />
            <Swatch name="Up (market)" hex="#2EBD85" cls="bg-up" />
            <Swatch name="Down (market)" hex="#F0616D" cls="bg-down" />
            <Swatch name="Foreground" hex="#F3F5F7" cls="bg-fg" />
          </div>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Swatch name="Prov S" hex="#2EBD85" cls="bg-prov-s" />
            <Swatch name="Prov A" hex="#6D7BFF" cls="bg-prov-a" />
            <Swatch name="Prov R" hex="#F5B461" cls="bg-prov-r" />
            <Swatch name="Prov U" hex="#F0616D" cls="bg-prov-u" />
          </div>
        </div>
      </Section>

      <Section
        title="Surfaces"
        desc="Flat segmented cards -- solid fill, hairline border, no blur -- plus the indigo-tinted surface for highlighted/active panels."
      >
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Panel variant="glass" pad="lg" className="h-32">
            <div className="text-sm font-medium">glass</div>
            <div className="mt-1 text-xs text-fg-muted">Default flat panel</div>
          </Panel>
          <Panel variant="strong" pad="lg" className="h-32">
            <div className="text-sm font-medium">glass-strong</div>
            <div className="mt-1 text-xs text-fg-muted">Primary reading surface</div>
          </Panel>
          <Panel variant="accent" pad="lg" className="h-32">
            <div className="text-sm font-medium text-accent">glass-accent</div>
            <div className="mt-1 text-xs text-fg-muted">Highlighted / active</div>
          </Panel>
          <Panel variant="lime" pad="lg" className="h-32">
            <div className="text-sm font-semibold">accent fill</div>
            <div className="mt-1 text-xs text-accent-foreground/70">Hero stat panel</div>
          </Panel>
        </div>
      </Section>

      <Section title="Controls">
        <div className="flex flex-wrap items-center gap-3">
          <Button>Primary</Button>
          <Button variant="glass">Glass</Button>
          <Button variant="outline">Outline</Button>
          <Button variant="ghost">Ghost</Button>
          <Button variant="primary" size="sm">
            <Plus /> Add
          </Button>
          <IconButton aria-label="Search">
            <MagnifyingGlass />
          </IconButton>
          <IconButton variant="solid" aria-label="Filters">
            <SlidersHorizontal />
          </IconButton>
          <IconButton variant="ghost" aria-label="Alerts">
            <Bell />
          </IconButton>
        </div>
      </Section>

      <Section title="Pills and segmented control">
        <div className="flex flex-wrap items-center gap-3">
          <Pill variant="active">1m</Pill>
          <Pill>5m</Pill>
          <Pill>15m</Pill>
          <Pill variant="up" dot>
            Live
          </Pill>
          <Pill variant="up">+0.84%</Pill>
          <Pill variant="down">-1.12%</Pill>
          <Pill variant="outline">Order block</Pill>
        </div>
        <TabsRoot defaultValue="5m" className="mt-2">
          <TabsList>
            <TabsTrigger value="1m">1m</TabsTrigger>
            <TabsTrigger value="5m">5m</TabsTrigger>
            <TabsTrigger value="15m">15m</TabsTrigger>
            <TabsTrigger value="1H">1H</TabsTrigger>
            <TabsTrigger value="4H">4H</TabsTrigger>
          </TabsList>
        </TabsRoot>
      </Section>

      <Section
        title="Provenance tags"
        desc="The spec's four-category discipline, carried into the UI. Hover any tag for its meaning."
      >
        <div className="flex flex-wrap gap-6">
          {PROV.map((x) => (
            <div key={x.p} className="flex items-center gap-2">
              <ProvenanceChip p={x.p} />
              <span className="text-sm text-fg-muted">{x.name}</span>
            </div>
          ))}
        </div>
      </Section>

      <Section title="Stats">
        <Panel variant="strong" pad="lg" className="flex flex-wrap gap-x-10 gap-y-4">
          <Stat label="Entry" value="67,740" size="lg" />
          <Stat label="Stop" value="67,600" tone="down" size="lg" />
          <Stat label="R:R" value="3.1" size="lg" />
          <Stat label="Last" value="68,142.5" delta="+0.84%" size="lg" />
        </Panel>
      </Section>

      <Section
        title="Instrument icons"
        desc="Real coin logos (cryptocurrency-icons, CC0) resolve from the symbol, with a lettered fallback for forex and unknown tickers."
      >
        <div className="flex flex-wrap gap-6">
          {[
            "BTCUSDT.P",
            "ETHUSDT.P",
            "SOLUSDT.P",
            "BNBUSDT.P",
            "XRPUSDT.P",
            "ADAUSDT.P",
            "AVAXUSDT.P",
            "LINKUSDT.P",
            "DOGEUSDT.P",
            "GBPUSD",
          ].map((s) => (
            <div key={s} className="flex flex-col items-center gap-2">
              <CoinIcon symbol={s} size={40} />
              <span className="text-[11px] text-fg-subtle">
                {s.replace(/USDT.*$|USD$/, "") || s}
              </span>
            </div>
          ))}
        </div>
      </Section>
    </div>
  );
}
