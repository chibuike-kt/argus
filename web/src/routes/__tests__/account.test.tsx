// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { Account } from "../account";
import { AuthProvider } from "@/auth/auth-context";
import type { ApiAuthSession } from "@/api/types";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const ME = { id: 1, email: "a@example.com", email_verified: true, created_at: "2026-01-01T00:00:00Z" };

const SESSIONS: ApiAuthSession[] = [
  {
    id: 1,
    created_at: "2026-01-01T00:00:00Z",
    last_seen_at: "2026-01-02T00:00:00Z",
    user_agent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0",
    ip: "203.0.113.1",
    current: true,
  },
  {
    id: 2,
    created_at: "2026-01-01T00:00:00Z",
    last_seen_at: "2026-01-02T00:00:00Z",
    user_agent: "Mozilla/5.0 (iPhone; CPU iPhone OS) Safari/604.1",
    ip: "203.0.113.2",
    current: false,
  },
];

function installFetchMock(sessions: ApiAuthSession[]) {
  const live = [...sessions];
  const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/auth/me") return Promise.resolve(jsonResponse(ME));
    if (url.pathname === "/api/v1/auth/sessions") return Promise.resolve(jsonResponse(live));
    if (url.pathname.startsWith("/api/v1/auth/sessions/") && init?.method === "DELETE") {
      const id = Number(url.pathname.split("/").pop());
      const idx = live.findIndex((s) => s.id === id);
      if (idx >= 0) live.splice(idx, 1);
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    throw new Error(`unexpected fetch: ${url.toString()}`);
  });
  global.fetch = fetchMock as unknown as typeof fetch;
  return fetchMock;
}

function renderAccount() {
  return render(
    <MemoryRouter initialEntries={["/account"]}>
      <AuthProvider>
        <Routes>
          <Route path="/account" element={<Account />} />
          <Route path="/login" element={<div>LOGIN PAGE</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("Account route", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("lists sessions with a readable user agent and a This device marker on the current one", async () => {
    installFetchMock(SESSIONS);
    renderAccount();

    expect(await screen.findByText("Chrome on Windows")).toBeInTheDocument();
    expect(await screen.findByText("Safari on iOS")).toBeInTheDocument();
    expect(screen.getByText("This device")).toBeInTheDocument();
  });

  it("revoking another session removes it from the list and does not touch the current one", async () => {
    installFetchMock(SESSIONS);
    renderAccount();

    await screen.findByText("Safari on iOS");
    const revokeButtons = screen.getAllByRole("button", { name: "Revoke" });
    expect(revokeButtons).toHaveLength(1); // only the non-current session has one
    await userEvent.click(revokeButtons[0]);

    await screen.findByText("Chrome on Windows");
    expect(screen.queryByText("Safari on iOS")).toBeNull();
  });
});
