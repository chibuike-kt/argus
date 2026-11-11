// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider } from "@/auth/auth-context";
import { TooltipProvider } from "@/components/ui/tooltip";
import { DrawingProvider } from "@/components/chart/drawing-context";
import { AnalysisWorkspaceProvider } from "@/components/workspace/analysis-workspace";
import { CommandPaletteProvider } from "@/components/workspace/command-palette";
import { ArgusActivityProvider } from "@/components/workspace/argus-activity-feed";
import { ChartToolbar } from "../chart-toolbar";
import type { ApiAuthUser } from "@/api/types";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const BASE_USER: ApiAuthUser = {
  id: 1,
  email: "a@example.com",
  email_verified: true,
  created_at: "2026-01-01T00:00:00Z",
  display_name: null,
  onboarding_completed: true,
};

function installFetchMock(me: ApiAuthUser) {
  global.fetch = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/auth/me") return Promise.resolve(jsonResponse(me));
    if (url.pathname === "/api/v1/instruments") return Promise.resolve(jsonResponse([]));
    throw new Error(`unexpected fetch: ${url.toString()}`);
  }) as unknown as typeof fetch;
}

function renderToolbar() {
  return render(
    <MemoryRouter initialEntries={["/analysis"]}>
      <TooltipProvider delayDuration={150}>
        <AuthProvider>
          <AnalysisWorkspaceProvider>
            <DrawingProvider>
              <CommandPaletteProvider>
                <ArgusActivityProvider>
                  <ChartToolbar />
                </ArgusActivityProvider>
              </CommandPaletteProvider>
            </DrawingProvider>
          </AnalysisWorkspaceProvider>
        </AuthProvider>
      </TooltipProvider>
    </MemoryRouter>,
  );
}

describe("ChartToolbar account avatar", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows the first letter of display_name, uppercased, when set", async () => {
    installFetchMock({ ...BASE_USER, display_name: "kevyn" });
    renderToolbar();
    expect(await screen.findByText("K")).toBeInTheDocument();
  });

  it("falls back to the first letter of the email when there is no display_name", async () => {
    installFetchMock({ ...BASE_USER, display_name: null, email: "zed@example.com" });
    renderToolbar();
    expect(await screen.findByText("Z")).toBeInTheDocument();
  });

  it("shows a placeholder before the user loads", () => {
    global.fetch = vi.fn(() => new Promise<Response>(() => {})) as unknown as typeof fetch;
    const { container } = renderToolbar();
    const avatar = container.querySelector(".bg-surface-2.rounded-full, .rounded-full.bg-surface-2");
    expect(avatar).toHaveTextContent("?");
  });
});
