// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { AuthProvider } from "../auth-context";
import { RequireAuth } from "../require-auth";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderProtected(initialEntries: string[]) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <AuthProvider>
        <Routes>
          <Route element={<RequireAuth />}>
            <Route path="/setups" element={<div>SETUPS PAGE</div>} />
            <Route path="/account" element={<div>ACCOUNT PAGE</div>} />
          </Route>
          <Route path="/sign-in" element={<div>LOGIN PAGE</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("RequireAuth", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("sends a signed-out visitor to /login, never rendering the protected route", async () => {
    global.fetch = vi
      .fn()
      .mockResolvedValue(jsonResponse({ error: { code: "unauthorized", message: "no session" } }, 401));

    renderProtected(["/account"]);

    expect(await screen.findByText("LOGIN PAGE")).toBeInTheDocument();
    expect(screen.queryByText("ACCOUNT PAGE")).toBeNull();
  });

  it("renders the protected route for a signed-in visitor", async () => {
    global.fetch = vi
      .fn()
      .mockResolvedValue(
        jsonResponse({ id: 1, email: "a@example.com", email_verified: true, created_at: "2026-01-01T00:00:00Z", display_name: null, onboarding_completed: true }),
      );

    renderProtected(["/setups"]);

    expect(await screen.findByText("SETUPS PAGE")).toBeInTheDocument();
  });
});
