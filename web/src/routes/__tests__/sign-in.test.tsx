// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { SignIn } from "../sign-in";
import { AuthProvider } from "@/auth/auth-context";

function jsonResponse(body: unknown, status = 200, headers?: Record<string, string>): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

// Every test visits /sign-in with a session that does not exist yet
// (GET /auth/me -> 401), the same as a real signed-out visitor.
function installFetchMock(loginHandler: (body: unknown) => Response) {
  const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/auth/me") {
      return Promise.resolve(jsonResponse({ error: { code: "unauthorized", message: "no session" } }, 401));
    }
    if (url.pathname === "/api/v1/auth/login") {
      return Promise.resolve(loginHandler(init?.body ? JSON.parse(String(init.body)) : undefined));
    }
    throw new Error(`unexpected fetch: ${url.toString()}`);
  });
  global.fetch = fetchMock as unknown as typeof fetch;
  return fetchMock;
}

function renderSignIn(initialEntries: string[] = ["/sign-in"]) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <AuthProvider>
        <Routes>
          <Route path="/sign-in" element={<SignIn />} />
          <Route path="/setups" element={<div>SETUPS PAGE</div>} />
          <Route path="/account" element={<div>ACCOUNT PAGE</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe("SignIn route", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("redirects to ?next= on success", async () => {
    installFetchMock(() =>
      jsonResponse({ id: 1, email: "a@example.com", email_verified: true, created_at: "2026-01-01T00:00:00Z", display_name: null, onboarding_completed: true }),
    );
    renderSignIn(["/sign-in?next=/account"]);

    await userEvent.type(await screen.findByLabelText(/Email address/), "a@example.com");
    await userEvent.type(screen.getByLabelText(/^Password/), "whatever12345");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText("ACCOUNT PAGE")).toBeInTheDocument();
  });

  it("shows the single generic message for invalid_credentials, regardless of cause", async () => {
    installFetchMock(() =>
      jsonResponse({ error: { code: "invalid_credentials", message: "invalid email or password" } }, 401),
    );
    renderSignIn();

    await userEvent.type(await screen.findByLabelText(/Email address/), "a@example.com");
    await userEvent.type(screen.getByLabelText(/^Password/), "wrong-password");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      await screen.findByText("Email or password is incorrect, or the email is not verified yet"),
    ).toBeInTheDocument();
  });

  it("shows and counts down a rate_limited response", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    installFetchMock(() =>
      jsonResponse({ error: { code: "rate_limited", message: "too many attempts" } }, 429, { "Retry-After": "5" }),
    );
    renderSignIn();

    await userEvent.type(await screen.findByLabelText(/Email address/), "a@example.com");
    await userEvent.type(screen.getByLabelText(/^Password/), "whatever12345");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText(/Too many attempts\. Try again in 5s\./)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();

    await vi.advanceTimersByTimeAsync(3000);
    expect(await screen.findByText(/Try again in 2s\./)).toBeInTheDocument();

    vi.useRealTimers();
  });

  it("renders the Google/Apple buttons disabled with a Soon tag -- they do nothing when clicked", async () => {
    const fetchMock = installFetchMock(() => jsonResponse({}, 202));
    renderSignIn();

    const google = await screen.findByRole("button", { name: /Log in with Google/ });
    const apple = screen.getByRole("button", { name: /Log in with Apple/ });
    expect(google).toBeDisabled();
    expect(apple).toBeDisabled();
    expect(screen.getAllByText("Soon")).toHaveLength(2);

    await userEvent.click(google);
    await userEvent.click(apple);
    // Only the boot /auth/me call happened -- clicking the disabled
    // social buttons fired no request at all.
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("renders a Forgot password link pointing at /forgot-password", async () => {
    installFetchMock(() => jsonResponse({}, 202));
    renderSignIn();
    await screen.findByLabelText(/Email address/);
    const link = screen.getByText("Forgot password?");
    expect(link.closest("a")).toHaveAttribute("href", "/forgot-password");
  });
});
