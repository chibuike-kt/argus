import { NavLink } from "react-router-dom";
import { Bell, Settings } from "lucide-react";
import { IconButton } from "@/components/ui/icon-button";
import { cn } from "@/lib/cn";

const NAV = [
  { to: "/setups", label: "Setups" },
  { to: "/analysis", label: "Analysis" },
  { to: "/style", label: "Design system" },
];

export function TopBar() {
  return (
    <header className="z-40 flex h-14 shrink-0 items-center gap-4 border-b border-line bg-base/60 px-4 backdrop-blur-xl">
      <div className="flex items-center gap-3">
        <div className="grid size-9 place-items-center rounded-xl bg-accent text-accent-foreground">
          <span className="font-display text-xl leading-none">A</span>
        </div>
        <span className="font-display text-xl leading-none tracking-wide">ARGUS</span>
      </div>

      <nav className="glass ml-2 hidden items-center gap-1 rounded-full p-1 md:flex">
        {NAV.map((nv) => (
          <NavLink
            key={nv.to}
            to={nv.to}
            className={({ isActive }) =>
              cn(
                "inline-flex h-8 items-center rounded-full px-4 text-[13px] font-medium transition-colors",
                isActive ? "bg-accent text-accent-foreground" : "text-fg-muted hover:text-fg",
              )
            }
          >
            {nv.label}
          </NavLink>
        ))}
      </nav>

      <div className="ml-auto flex items-center gap-2">
        <IconButton variant="ghost" aria-label="Notifications">
          <Bell />
        </IconButton>
        <IconButton variant="ghost" aria-label="Settings">
          <Settings />
        </IconButton>
        <span className="ml-1 grid size-9 place-items-center rounded-full bg-surface-2 text-sm font-semibold text-fg-muted">
          K
        </span>
      </div>
    </header>
  );
}
