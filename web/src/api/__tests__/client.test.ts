import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiClientError,
  deleteRequest,
  getJSON,
  postJSON,
  setUnauthorizedHandler,
} from "../client";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("getJSON", () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it("returns the parsed body on a 200", async () => {
    global.fetch = vi.fn().mockResolvedValue(jsonResponse([{ id: 1 }]));
    const result = await getJSON<{ id: number }[]>("/instruments");
    expect(result).toEqual([{ id: 1 }]);
  });

  it("builds query params from the given object, skipping undefined values", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}));
    global.fetch = fetchMock;
    await getJSON("/setups", { instrument_id: 7, state: undefined, limit: 50 });
    const calledUrl = String(fetchMock.mock.calls[0][0]);
    expect(calledUrl).toContain("instrument_id=7");
    expect(calledUrl).toContain("limit=50");
    expect(calledUrl).not.toContain("state=");
  });

  it("parses the shared {error:{code,message,request_id}} shape into ApiClientError on a non-2xx response", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      jsonResponse({ error: { code: "not_found", message: "instrument not found", request_id: "r-1" } }, 404),
    );
    await expect(getJSON("/instruments/999")).rejects.toMatchObject({
      status: 404,
      code: "not_found",
      message: "instrument not found",
      requestId: "r-1",
    });
  });

  it("falls back to a generic message when a non-2xx body is not the shared error shape", async () => {
    global.fetch = vi.fn().mockResolvedValue(new Response("<html>502</html>", { status: 502 }));
    await expect(getJSON("/setups")).rejects.toMatchObject({ status: 502 });
  });

  it("throws an ApiClientError with status 0 when the request is manually aborted", async () => {
    global.fetch = vi.fn().mockImplementation((_url: URL, init?: RequestInit) => {
      return new Promise((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => {
          const err = new Error("aborted");
          err.name = "AbortError";
          reject(err);
        });
      });
    });
    const controller = new AbortController();
    const promise = getJSON("/setups", undefined, { signal: controller.signal });
    controller.abort();
    await expect(promise).rejects.toBeInstanceOf(ApiClientError);
    await expect(promise).rejects.toMatchObject({ status: 0 });
  });

  it("times out and rejects with status 0 when the server never responds", async () => {
    global.fetch = vi.fn().mockImplementation((_url: URL, init?: RequestInit) => {
      return new Promise((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => {
          const err = new Error("aborted");
          err.name = "AbortError";
          reject(err);
        });
      });
    });
    await expect(getJSON("/setups", undefined, { timeoutMs: 5 })).rejects.toMatchObject({ status: 0 });
  });

  it("sends credentials same-origin on every request", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}));
    global.fetch = fetchMock;
    await getJSON("/setups");
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ credentials: "same-origin" });
  });
});

describe("postJSON", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sends Content-Type: application/json and the JSON body", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ id: 1 }, 202));
    global.fetch = fetchMock;
    await postJSON("/auth/register", { email: "a@example.com", password: "whatever12345" });

    const [, init] = fetchMock.mock.calls[0];
    expect(init.method).toBe("POST");
    expect(init.headers).toMatchObject({ "Content-Type": "application/json" });
    expect(JSON.parse(init.body)).toEqual({ email: "a@example.com", password: "whatever12345" });
  });

  it("reads Retry-After into ApiClientError.retryAfterSeconds on a 429", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "rate_limited", message: "too many" } }), {
        status: 429,
        headers: { "Content-Type": "application/json", "Retry-After": "42" },
      }),
    );
    await expect(postJSON("/auth/login", { email: "a@example.com", password: "x" })).rejects.toMatchObject({
      status: 429,
      code: "rate_limited",
      retryAfterSeconds: 42,
    });
  });

  it("falls back to a status-derived code when the body isn't the shared error shape", async () => {
    global.fetch = vi.fn().mockResolvedValue(new Response("<html>", { status: 401 }));
    await expect(postJSON("/auth/logout")).rejects.toMatchObject({ status: 401, code: "unauthorized" });
  });
});

describe("deleteRequest", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sends DELETE with Content-Type: application/json and no body", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    global.fetch = fetchMock;
    await deleteRequest("/auth/sessions/7");

    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain("/auth/sessions/7");
    expect(init.method).toBe("DELETE");
    expect(init.headers).toMatchObject({ "Content-Type": "application/json" });
    expect(init.body).toBeUndefined();
  });
});

describe("global 401 handler", () => {
  afterEach(() => {
    setUnauthorizedHandler(null);
    vi.restoreAllMocks();
  });

  it("fires on a 401 from getJSON", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "unauthorized", message: "no session" } }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    await expect(getJSON("/auth/me")).rejects.toMatchObject({ status: 401 });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it("does not fire for a call that suppresses it (the login call itself)", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "invalid_credentials", message: "bad" } }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    await expect(
      postJSON("/auth/login", { email: "a@example.com", password: "x" }, { suppressUnauthorizedHandler: true }),
    ).rejects.toMatchObject({ status: 401 });
    expect(handler).not.toHaveBeenCalled();
  });
});
