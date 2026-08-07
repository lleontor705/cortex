import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError, CortexAPI } from "./api";
import { normalizeServerURL } from "./auth";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("CortexAPI authentication", () => {
  it("verifies access against the protected stats endpoint", async () => {
    const request = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ observations: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", request);

    const client = new CortexAPI("https://cortex.example", "secret");
    await client.verifyAccess();

    expect(request).toHaveBeenCalledWith(
      "https://cortex.example/api/stats",
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: "Bearer secret" }),
      }),
    );
  });

  it("reports unauthorized access and expires the client session", async () => {
    const unauthorized = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: { message: "valid bearer token required" },
          }),
          {
            status: 401,
            headers: { "Content-Type": "application/json" },
          },
        ),
      ),
    );

    const client = new CortexAPI(
      "https://cortex.example",
      "wrong",
      unauthorized,
    );

    await expect(client.verifyAccess()).rejects.toEqual(
      expect.objectContaining<Partial<APIError>>({
        message: "valid bearer token required",
        status: 401,
      }),
    );
    expect(unauthorized).toHaveBeenCalledOnce();
  });

  it("keeps the session active when an operation is forbidden", async () => {
    const unauthorized = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: { message: "administrative role required" },
          }),
          {
            status: 403,
            headers: { "Content-Type": "application/json" },
          },
        ),
      ),
    );

    const client = new CortexAPI(
      "https://cortex.example",
      "member-token",
      unauthorized,
    );
    await expect(client.users()).rejects.toEqual(
      expect.objectContaining<Partial<APIError>>({ status: 403 }),
    );
    expect(unauthorized).not.toHaveBeenCalled();
  });
});

describe("server URL validation", () => {
  it("normalizes HTTPS origins", () => {
    expect(normalizeServerURL("https://cortex.example/anything")).toBe(
      "https://cortex.example",
    );
  });

  it("allows HTTP only for loopback development", () => {
    expect(normalizeServerURL("http://localhost:7438")).toBe(
      "http://localhost:7438",
    );
    expect(() => normalizeServerURL("http://cortex.example")).toThrow(
      "Use HTTPS",
    );
  });

  it("rejects URLs containing credentials or request components", () => {
    expect(() =>
      normalizeServerURL("https://user:pass@cortex.example"),
    ).toThrow("only the Cortex server origin");
    expect(() =>
      normalizeServerURL("https://cortex.example?token=secret"),
    ).toThrow("only the Cortex server origin");
  });
});
