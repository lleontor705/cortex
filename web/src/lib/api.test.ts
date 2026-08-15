import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError, CortexClient } from "./api";

afterEach(() => {
  vi.unstubAllGlobals();
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("CortexClient request plumbing", () => {
  it("sends the bearer token and strips a trailing slash from the base URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ observations: 0, sessions: 0, active_sessions: 0, edges: 0, projects: 0 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new CortexClient("https://cortex.example/", "secret");
    await client.stats();

    expect(fetchMock).toHaveBeenCalledWith(
      "https://cortex.example/api/stats",
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: "Bearer secret",
          "Content-Type": "application/json",
        }),
      }),
    );
  });

  it("omits the Authorization header when no token is configured", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);

    await new CortexClient("https://cortex.example", "").health();

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
  });

  it("expires the client session and throws APIError on 401", async () => {
    const unauthorized = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ error: { message: "valid bearer token required" } }, 401),
      ),
    );

    const client = new CortexClient("https://cortex.example", "wrong", unauthorized);
    await expect(client.me()).rejects.toMatchObject({
      name: "APIError",
      status: 401,
      message: "valid bearer token required",
    });
    expect(unauthorized).toHaveBeenCalledOnce();
  });

  it("falls back to a generic message when the error body is not JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("gateway crashed", { status: 502 })),
    );

    await expect(new CortexClient("https://cortex.example", "t").projects()).rejects.toMatchObject(
      new APIError("Error 502: Request failed", 502),
    );
  });

  it("resolves undefined for 204 responses", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));

    await expect(
      new CortexClient("https://cortex.example", "t").deleteObservation("obs-1"),
    ).resolves.toBeUndefined();
  });

  it("URL-encodes search query and optional project filter", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ value: [], Count: 0 }));
    vi.stubGlobal("fetch", fetchMock);

    await new CortexClient("https://cortex.example", "t").search("a b&c", "proj x");

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(
      "https://cortex.example/api/search?q=a%20b%26c&project=proj%20x",
    );
  });
});
