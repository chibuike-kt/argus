// @vitest-environment jsdom
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { VerifyEmail } from "../verify-email";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <VerifyEmail />
    </MemoryRouter>,
  );
}

describe("VerifyEmail route", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("posts the token from the URL exactly once, even under StrictMode's double effect-fire", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ status: "verified" }, 200));
    global.fetch = fetchMock as unknown as typeof fetch;

    // React 18 StrictMode (same as main.tsx's own root) deliberately
    // fires every effect, its cleanup, then the effect again on the
    // same mounted component instance, specifically to surface a
    // missing-cleanup bug -- triedRef's guard inside VerifyEmail is
    // what this test actually exercises.
    render(
      <StrictMode>
        <MemoryRouter initialEntries={["/verify-email?token=abc123"]}>
          <VerifyEmail />
        </MemoryRouter>
      </StrictMode>,
    );

    expect(await screen.findByText("Email verified")).toBeInTheDocument();
    const verifyCalls = fetchMock.mock.calls.filter((args: unknown[]) =>
      String(args[0]).includes("/verify-email"),
    );
    expect(verifyCalls).toHaveLength(1);
  });

  it("shows a clear failure message and a resend form for an invalid token", async () => {
    global.fetch = vi
      .fn()
      .mockResolvedValue(jsonResponse({ error: { code: "invalid_argument", message: "bad token" } }, 400));

    renderAt("/verify-email?token=expired-or-reused");

    expect(
      await screen.findByText("This link is invalid, was already used, or has expired."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Resend verification email" })).toBeInTheDocument();
  });

  it("offers a manual token field when opened with no token at all", () => {
    renderAt("/verify-email");
    expect(screen.getByLabelText("Verification token")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Verify" })).toBeInTheDocument();
  });
});
