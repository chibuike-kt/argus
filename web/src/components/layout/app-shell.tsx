import { Outlet, useLocation } from "react-router-dom";
import { TooltipProvider } from "@/components/ui/tooltip";
import { CommandPaletteProvider } from "@/components/workspace/command-palette";
import { SampleDataBadge } from "@/components/ui/sample-data-badge";
import { fixturesEnabled } from "@/mocks/fixtures-as-api";
import { TopBar } from "./top-bar";

export function AppShell() {
  const { pathname } = useLocation();
  // The analysis workspace is full-screen: it carries its own top bar (the
  // chart toolbar), so the global top bar is hidden there.
  const fullBleed = pathname === "/analysis";

  return (
    <TooltipProvider delayDuration={150}>
      <CommandPaletteProvider>
        <div className="flex h-full flex-col">
          {!fullBleed && <TopBar />}
          <main className="min-h-0 flex-1">
            <Outlet />
          </main>
          {fixturesEnabled() && (
            <div className="pointer-events-none fixed bottom-3 right-3 z-50">
              <SampleDataBadge label="VITE_USE_FIXTURES: all data is sample" />
            </div>
          )}
        </div>
      </CommandPaletteProvider>
    </TooltipProvider>
  );
}
