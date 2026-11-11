// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { AuthProvider, useAuth } from "../auth-context";
import { getJSON } from "@/api/client";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function Probe() {
  const { status, user } = useAuth();
  return (
    <div>
      status: {status}
      {user && <span> user: {user.email}</span>}
    </div>
  );
}

describe("AuthProvider", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("bypasses auth entirely in fixtures mode -- no network call, always signed in", async () => {
    vi.stubEnv("VITE_USE_FIXTURES", "true");
    const fetchMock = vi.fn();
    global.fetch = fetchMock as unknown as typeof fetch;

    render(
      <MemoryRouter>
        <AuthProvider>
          <Probe />
        </AuthProvider>
      </MemoryRouter>,
    );

    expect(await screen.findByText(/status: signedIn/)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("the global 401 handler clears state and redirects to /login once registered", async () => {
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/auth/me") {
        return Promise.resolve(
          jsonResponse({ id: 1, email: "a@example.com", email_verified: true, created_at: "2026-01-01T00:00:00Z", display_name: null, onboarding_completed: true }),
        );
      }
      if (url.pathname === "/api/v1/setups") {
        return Promise.resolve(jsonResponse({ error: { code: "unauthorized", message: "session expired" } }, 401));
      }
      throw new Error(`unexpected fetch: ${url.toString()}`);
    });
    global.fetch = fetchMock as unknown as typeof fetch;

    render(
      <MemoryRouter initialEntries={["/setups"]}>
        <AuthProvider>
          <Routes>
            <Route path="/setups" element={<Probe />} />
            <Route path="/sign-in" element={<div>LOGIN PAGE</div>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>,
    );

    // Signed in once the boot /auth/me resolves -- the handler is only
    // registered after this, so it cannot fire for this call itself.
    await screen.findByText(/status: signedIn/);

    // A later call from elsewhere in the app (not the login call) gets
    // a 401 -- e.g. the session expired mid-use.
    await getJSON("/setups").catch(() => undefined);

    await waitFor(() => expect(screen.getByText("LOGIN PAGE")).toBeInTheDocument());
  });
});
