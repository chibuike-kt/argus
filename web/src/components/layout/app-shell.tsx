import { Outlet, useLocation } from "react-router-dom";
import { TooltipProvider } from "@/components/ui/tooltip";
import { CommandPaletteProvider } from "@/components/workspace/command-palette";
import { SymbolSearchProvider } from "@/components/workspace/symbol-search";
import { TopBar } from "./top-bar";

export function AppShell() {
  const { pathname } = useLocation();
  // The analysis workspace is full-screen: it carries its own top bar (the
  // chart toolbar), so the global top bar is hidden there.
  const fullBleed = pathname === "/analysis";

  return (
    <TooltipProvider delayDuration={150}>
      <CommandPaletteProvider>
        <SymbolSearchProvider>
        <div className="flex h-full flex-col">
          {!fullBleed && <TopBar />}
          <main className="min-h-0 flex-1">
            <Outlet />
          </main>
        </div>
        </SymbolSearchProvider>
      </CommandPaletteProvider>
    </TooltipProvider>
  );
}
