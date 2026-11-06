import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClientError, getJSON } from "../client";

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
});
