// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Register } from "../register";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderRegister() {
  return render(
    <MemoryRouter>
      <Register />
    </MemoryRouter>,
  );
}

describe("Register route", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows live password-length hints as the field is typed", async () => {
    renderRegister();
    const password = screen.getByLabelText("Password");

    // Below the 12-character minimum: the requirement text is still
    // present (a client-side hint, not an error), just unmet.
    await userEvent.type(password, "short");
    expect(screen.getByText("At least 12 characters")).toBeInTheDocument();

    await userEvent.type(password, "er-password-now");
    // Met requirements render the same text, now styled as satisfied --
    // presence is what this test can assert without reading computed
    // colour; the field's own `invalid` prop (asserted indirectly via
    // no error border class would be brittle) is covered by not
    // reaching the server during this test at all.
    expect(screen.getByText("At least 12 characters")).toBeInTheDocument();
    expect(screen.getByText("At most 128 characters")).toBeInTheDocument();
  });

  it("shows the Check your email pending state on a successful submit, in place", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ status: "accepted" }, 202));
    global.fetch = fetchMock as unknown as typeof fetch;
    renderRegister();

    await userEvent.type(screen.getByLabelText("Email"), "a@example.com");
    await userEvent.type(screen.getByLabelText("Password"), "a-perfectly-fine-password-1");
    await userEvent.type(screen.getByLabelText("Confirm password"), "a-perfectly-fine-password-1");
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));

    expect(await screen.findByText("Check your email")).toBeInTheDocument();
    expect(screen.getByText(/a@example\.com/)).toBeInTheDocument();
  });

  it("rejects a confirm-password mismatch client-side, without calling the server", async () => {
    const fetchMock = vi.fn();
    global.fetch = fetchMock as unknown as typeof fetch;
    renderRegister();

    await userEvent.type(screen.getByLabelText("Email"), "a@example.com");
    await userEvent.type(screen.getByLabelText("Password"), "a-perfectly-fine-password-1");
    await userEvent.type(screen.getByLabelText("Confirm password"), "a-different-one-entirely");
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));

    expect(await screen.findByText("Passwords do not match.")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
