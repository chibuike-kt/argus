import { useState, type ReactNode } from "react";
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
import { Wordmark } from "@/components/light/wordmark";
import { BackButton } from "@/components/light/back-button";
import { SupportButton } from "@/components/light/support-button";
import { StepProgress } from "@/components/light/step-progress";
import { TextInput } from "@/components/light/text-input";
import { PasswordInput } from "@/components/light/password-input";
import { PrimaryButton } from "@/components/light/primary-button";
import { SocialButton } from "@/components/light/social-button";
import { OrDivider } from "@/components/light/or-divider";
import { CodeInput } from "@/components/light/code-input";
import { Checkbox } from "@/components/light/checkbox";
import { GoogleIcon, AppleIcon } from "@/components/light/brand-icons";

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

// Every light-theme component, built once in components/light/ for the
// sign-in/sign-up/onboarding pages, shown here too so there is one
// place that renders the whole light library (brief's own "build once,
// show on /style" instruction). theme-light sets its own background
// and min-height:100dvh for a full page -- min-h-0 cancels the latter
// so this nests as a contained panel instead of trying to fill the
// viewport again inside the (dark) style guide page around it.
function LightPagesShowcase() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [checked, setChecked] = useState(false);

  return (
    <div className="theme-light min-h-0 space-y-8 rounded-xl border border-light-line bg-light-page p-6 sm:p-10">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Swatch name="Page" hex="#FBFBFB" cls="bg-light-page" />
        <Swatch name="Surface" hex="#FFFFFF" cls="bg-light-surface border-light-line" />
        <Swatch name="Fill" hex="#EEEEEE" cls="bg-light-fill" />
        <Swatch name="Line" hex="#E5E7EB" cls="bg-light-line" />
        <Swatch name="Text (accent-fg)" hex="#0A0B0D" cls="bg-light-text" />
        <Swatch name="Muted" hex="#6B7280" cls="bg-light-muted" />
        <Swatch name="Primary (accent)" hex="#6D7BFF" cls="bg-light-primary" />
        <Swatch name="Error" hex="#DC2626" cls="bg-light-error" />
      </div>

      <div className="flex flex-wrap items-center gap-6">
        <Wordmark />
        <BackButton onClick={() => {}} />
        <SupportButton />
        <StepProgress current={2} total={5} />
      </div>

      <div className="grid gap-8 sm:grid-cols-2">
        <div className="space-y-3.5">
          <TextInput
            label="Email address"
            id="style-guide-email"
            required
            placeholder="e.g. you@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <PasswordInput
            label="Password"
            id="style-guide-password"
            required
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <TextInput label="With an error" id="style-guide-error" error="That doesn't look right" />
          <Checkbox
            id="style-guide-checkbox"
            checked={checked}
            onChange={(e) => setChecked(e.target.checked)}
            label="I agree to the Terms and Privacy Policy"
          />
        </div>
        <div className="space-y-3.5">
          <OrDivider />
          <SocialButton icon={<GoogleIcon />} soon>
            Continue with Google
          </SocialButton>
          <SocialButton icon={<AppleIcon />} soon>
            Continue with Apple
          </SocialButton>
          <PrimaryButton disabled={!email || !password}>Continue (muted until filled)</PrimaryButton>
          <PrimaryButton loading>Continue (loading)</PrimaryButton>
          <PrimaryButton>Continue (ready)</PrimaryButton>
        </div>
      </div>

      <div className="space-y-2">
        <div className="text-sm font-medium text-light-text">Verification code</div>
        <CodeInput value={code} onChange={setCode} />
      </div>
    </div>
  );
}

export function StyleGuide() {
  return (
    <div className="mx-auto h-full max-w-[1180px] space-y-10 overflow-y-auto px-4 py-8 sm:px-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">ARGUS design system</h1>
        <p className="max-w-2xl text-fg-muted">
          A trading-intelligence terminal: true black, flat segmented cards, and colour that only
          ever means something -- the market speaks green/red, ARGUS's own marks on the chart speak
          the indigo accent, and generic chrome (tabs, the active tool, focus) stays neutral.
          Typeface is Onest for body text and Archivo for headings (replacing Montserrat), with IBM
          Plex Mono for every number unchanged, and Astron Valley reserved for the ARGUS logo mark
          and wordmark.
        </p>
      </header>

      <Section
        title="Typography"
        desc="Onest across body text, Archivo for headings -- the web light auth redesign brief's own default, confirmed against the reference screenshots rather than reversed. Every number -- prices, times, deltas -- still renders in IBM Plex Mono via the .tnum class, with tabular figures so they stay aligned under live updates."
      >
        <Panel variant="strong" pad="lg" className="space-y-6">
          <div className="font-heading text-6xl font-semibold tracking-tight">Archivo</div>
          <div className="flex flex-wrap items-end gap-x-6 gap-y-2 font-heading">
            <span className="text-2xl font-normal">Regular 400</span>
            <span className="text-2xl font-medium">Medium 500</span>
            <span className="text-2xl font-semibold">Semibold 600</span>
            <span className="text-2xl font-bold">Bold 700</span>
          </div>
          <div className="border-t border-line pt-6 text-6xl font-semibold tracking-tight">
            Onest
          </div>
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
        title="Light pages"
        desc="Sign-in, sign-up and onboarding (web light auth redesign brief): a second, entirely separate token set and component library -- white, not terminal black. --color-light-primary is literally --color-accent, the same indigo hex as above, so the two palettes share one brand accent even though they never share a surface."
      >
        <LightPagesShowcase />
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
