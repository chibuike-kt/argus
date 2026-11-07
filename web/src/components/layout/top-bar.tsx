import { NavLink, useNavigate } from "react-router-dom";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Bell, Gear } from "@phosphor-icons/react";
import { IconButton } from "@/components/ui/icon-button";
import { useAuth } from "@/auth/auth-context";
import { cn } from "@/lib/cn";

const NAV = [
  { to: "/setups", label: "Setups" },
  { to: "/analysis", label: "Analysis" },
  { to: "/style", label: "Design system" },
];

export function TopBar() {
  const { user, signOut } = useAuth();
  const navigate = useNavigate();
  const initial = user?.email?.[0]?.toUpperCase() ?? "?";

  async function onSignOut() {
    await signOut();
    navigate("/login", { replace: true });
  }

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
                isActive
                  ? "bg-chrome-active text-chrome-active-foreground"
                  : "text-fg-muted hover:text-fg",
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
          <Gear />
        </IconButton>
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button
              aria-label="Account menu"
              className="focus-ring ml-1 grid size-9 place-items-center rounded-full bg-surface-2 text-sm font-semibold text-fg-muted transition-colors hover:text-fg"
            >
              {initial}
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              align="end"
              sideOffset={10}
              className="glass-strong z-[60] w-56 rounded-lg p-1 shadow-xl"
            >
              <div className="truncate px-2.5 py-2 text-[12px] text-fg-subtle">{user?.email}</div>
              <DropdownMenu.Separator className="my-1 h-px bg-line" />
              <DropdownMenu.Item
                onSelect={() => navigate("/account")}
                className="cursor-pointer rounded-md px-2.5 py-1.5 text-[13px] text-fg outline-none transition-colors data-[highlighted]:bg-white/5"
              >
                Account
              </DropdownMenu.Item>
              <DropdownMenu.Item
                onSelect={onSignOut}
                className="cursor-pointer rounded-md px-2.5 py-1.5 text-[13px] text-fg outline-none transition-colors data-[highlighted]:bg-white/5"
              >
                Sign out
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
    </header>
  );
}
