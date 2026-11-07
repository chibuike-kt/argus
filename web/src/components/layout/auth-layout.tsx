import type { ReactNode } from "react";

// Shared layout for /login, /register and /verify-email (web auth UI
// brief, Part A3): centred ~400px column, logo mark + wordmark above
// (same visual language as the top bar's own, --font-display with its
// Montserrat fallback -- no font file added), a flat surface card with
// a hairline border, and a faint footer line. No top bar or tool rail.
export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <div className="w-full max-w-[400px] space-y-6">
        <div className="flex flex-col items-center gap-2">
          <div className="grid size-11 place-items-center rounded-xl bg-accent text-accent-foreground">
            <span className="font-display text-2xl leading-none">A</span>
          </div>
          <span className="font-display text-2xl leading-none tracking-wide">ARGUS</span>
        </div>

        <div className="glass rounded-xl p-6">{children}</div>

        <p className="text-center text-[11px] text-fg-subtle">
          Market-intelligence terminal. No real orders are ever placed.
        </p>
      </div>
    </div>
  );
}
