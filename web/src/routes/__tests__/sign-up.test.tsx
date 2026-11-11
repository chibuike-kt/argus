// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { SignUp } from "../sign-up";
import { AuthProvider } from "@/auth/auth-context";
import { clearSignupState } from "@/lib/signup-state";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const ME_UNAUTHORIZED = () =>
  Promise.resolve(jsonResponse({ error: { code: "unauthorized", message: "no session" } }, 401));

function installFetchMock(handlers: {
  start?: (body: unknown) => Response;
  verify?: (body: unknown) => Response;
  complete?: (body: unknown) => Response;
}) {
  const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    if (url.pathname === "/api/v1/auth/me") return ME_UNAUTHORIZED();
    if (url.pathname === "/api/v1/auth/signup/start" && handlers.start) {
      return Promise.resolve(handlers.start(body));
    }
    if (url.pathname === "/api/v1/auth/signup/verify" && handlers.verify) {
      return Promise.resolve(handlers.verify(body));
    }
    if (url.pathname === "/api/v1/auth/signup/complete" && handlers.complete) {
      return Promise.resolve(handlers.complete(body));
    }
    throw new Error(`unexpected fetch: ${url.toString()}`);
  });
  global.fetch = fetchMock as unknown as typeof fetch;
  return fetchMock;
}

function renderSignUp() {
  return render(
    <MemoryRouter initialEntries={["/sign-up"]}>
      <AuthProvider>
        <Routes>
          <Route path="/sign-up" element={<SignUp />} />
          <Route path="/onboarding" element={<div>ONBOARDING PAGE</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

async function typeCode(code: string) {
  const boxes = screen.getAllByLabelText(/^Digit \d of 6$/);
  for (let i = 0; i < code.length; i++) {
    await userEvent.type(boxes[i], code[i]);
  }
}

describe("SignUp route", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    clearSignupState();
  });

  it("walks email -> code -> password -> /onboarding on the happy path", async () => {
    installFetchMock({
      start: () => jsonResponse({ status: "accepted" }, 202),
      verify: () => jsonResponse({ ticket: "the-ticket" }),
      complete: () =>
        jsonResponse({
          id: 1,
          email: "a@example.com",
          email_verified: true,
          created_at: "2026-01-01T00:00:00Z",
          display_name: null,
          onboarding_completed: false,
        }),
    });
    renderSignUp();

    await userEvent.type(await screen.findByLabelText(/Email address/), "a@example.com");
    await userEvent.click(screen.getByLabelText(/I agree to ARGUS's/));
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    await screen.findByText(/Check your email/);
    await typeCode("123456");

    await screen.findByText(/Create a password/);
    await userEvent.type(screen.getByLabelText(/^Password/), "a-perfectly-fine-password-1");
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));

    expect(await screen.findByText("ONBOARDING PAGE")).toBeInTheDocument();
  });

  it("requires the terms checkbox before Continue is enabled on step 1", async () => {
    installFetchMock({});
    renderSignUp();

    await userEvent.type(await screen.findByLabelText(/Email address/), "a@example.com");
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();

    await userEvent.click(screen.getByLabelText(/I agree to ARGUS's/));
    expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();
  });

  it("shows one generic message and clears the code on invalid_code", async () => {
    installFetchMock({
      start: () => jsonResponse({ status: "accepted" }, 202),
      verify: () =>
        jsonResponse({ error: { code: "invalid_code", message: "invalid, expired, unknown, or exhausted" } }, 400),
    });
    renderSignUp();

    await userEvent.type(await screen.findByLabelText(/Email address/), "a@example.com");
    await userEvent.click(screen.getByLabelText(/I agree to ARGUS's/));
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    await screen.findByText(/Check your email/);
    await typeCode("000000");

    expect(
      await screen.findByText("That code didn't work. Check it and try again, or request a new one."),
    ).toBeInTheDocument();
    const boxes = screen.getAllByLabelText(/^Digit \d of 6$/) as HTMLInputElement[];
    expect(boxes.every((b) => b.value === "")).toBe(true);
  });
});
