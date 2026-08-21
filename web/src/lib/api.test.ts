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

describe("CortexClient bearer transport policy", () => {
  it("rejects bearer requests to plain-HTTP non-loopback hosts before fetching", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const client = new CortexClient("http://10.0.0.5:7438", "secret");
    await expect(client.me()).rejects.toThrow(/HTTPS/i);
    await expect(client.health()).rejects.toThrow(/HTTPS/i);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects bearer requests to lookalike loopback hostnames before fetching", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const client = new CortexClient("http://localhost.evil.example:7438", "secret");
    await expect(client.me()).rejects.toThrow(/HTTPS/i);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("still sends the bearer token over plain HTTP to strict loopback hosts", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ id: "p1", type: "user", org_id: "o", workspaces: [], projects: [], roles: [], scopes: [], classification_clearance: [], auth_method: "token" }));
    vi.stubGlobal("fetch", fetchMock);

    await new CortexClient("http://127.0.0.1:7438", "secret").me();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://127.0.0.1:7438/api/me");
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer secret");
  });

  it("still sends the bearer token over HTTPS", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ id: "p1", type: "user", org_id: "o", workspaces: [], projects: [], roles: [], scopes: [], classification_clearance: [], auth_method: "token" }));
    vi.stubGlobal("fetch", fetchMock);

    await new CortexClient("https://cortex.example", "secret").me();

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer secret");
  });

  it("anonymous requests are not blocked by the bearer transport policy", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);

    await new CortexClient("http://10.0.0.5:7438", "").health();

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect((init.headers as Record<string, string>).Authorization).toBeUndefined();
  });
});

describe("CortexClient session invalidation", () => {
  it("on 401 clears the live token, aborts in-flight siblings and notifies", async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/me")) {
        return Promise.resolve(jsonResponse({ error: { message: "expired" } }, 401));
      }
      // Any other endpoint hangs until its request signal is aborted.
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () =>
          reject(new Error("aborted by session invalidation")),
        );
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    const unauthorized = vi.fn();
    const client = new CortexClient("https://cortex.example", "secret", unauthorized);

    const hanging = client.stats();
    await expect(client.me()).rejects.toMatchObject({
      name: "APIError",
      status: 401,
      message: "expired",
    });
    await expect(hanging).rejects.toThrow("aborted by session invalidation");
    expect(unauthorized).toHaveBeenCalledOnce();

    // The live token reference is gone: subsequent requests are anonymous.
    fetchMock.mockImplementationOnce(() =>
      Promise.resolve(jsonResponse({ status: "ok" })),
    );
    await client.health();
    const [, init] = fetchMock.mock.calls.at(-1) as [string, RequestInit];
    expect((init.headers as Record<string, string>).Authorization).toBeUndefined();
  });

  it("invalidate() (logout path) aborts in-flight requests and drops the token", async () => {
    const fetchMock = vi.fn((_url: string, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () =>
          reject(new Error("aborted by logout")),
        );
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new CortexClient("https://cortex.example", "secret");
    const hanging = client.stats();

    client.invalidate();

    await expect(hanging).rejects.toThrow("aborted by logout");

    fetchMock.mockImplementationOnce(() =>
      Promise.resolve(jsonResponse({ status: "ok" })),
    );
    await client.health();
    const [, init] = fetchMock.mock.calls.at(-1) as [string, RequestInit];
    expect((init.headers as Record<string, string>).Authorization).toBeUndefined();
  });
});
