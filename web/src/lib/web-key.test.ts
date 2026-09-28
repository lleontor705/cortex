import { describe, expect, it } from "vitest";
import {
  DEFAULT_GUARDED_PATH,
  DEFAULT_HEALTH_PATH,
  WEB_KEY_STORAGE_KEY,
  clearStoredWebKey,
  initialWebKeyGateState,
  loadStoredWebKey,
  normalizeWebKey,
  saveStoredWebKey,
  submitWebKey,
  verifyWebKey,
  webKeyGateReducer,
  type WebKeyProbeDeps,
} from "./web-key";

const BASE = "https://cortex.example";
const HEALTH_URL = `${BASE}${DEFAULT_HEALTH_PATH}`;
const GUARDED_URL = `${BASE}${DEFAULT_GUARDED_PATH}`;

class FakeStorage {
  private map = new Map<string, string>();

  getItem(key: string): string | null {
    return this.map.has(key) ? (this.map.get(key) as string) : null;
  }

  setItem(key: string, value: string): void {
    this.map.set(key, String(value));
  }

  removeItem(key: string): void {
    this.map.delete(key);
  }

  keys(): string[] {
    return [...this.map.keys()];
  }
}

interface RecordedCall {
  url: string;
  authorization: string | null;
}

function fakeFetch(
  routes: Record<string, number | "network">,
  calls: RecordedCall[] = [],
): typeof fetch {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, authorization: new Headers(init?.headers).get("Authorization") });
    const route = routes[url];
    if (route === undefined || route === "network") {
      throw new TypeError(`network error for ${url}`);
    }
    return new Response(JSON.stringify({ status: route < 400 ? "ok" : "error" }), {
      status: route,
    });
  }) as unknown as typeof fetch;
}

function deps(routes: Record<string, number | "network">, calls: RecordedCall[] = []): WebKeyProbeDeps {
  return { fetch: fakeFetch(routes, calls), baseUrl: BASE };
}

const HEALTHY = { [HEALTH_URL]: 200, [GUARDED_URL]: 200 } as const;

describe("normalizeWebKey", () => {
  it("trims surrounding whitespace and newlines deterministically", () => {
    expect(normalizeWebKey("  cortex_web_abc  ")).toBe("cortex_web_abc");
    expect(normalizeWebKey("\n\tcortex_web_abc \r\n")).toBe("cortex_web_abc");
  });

  it("collapses a whitespace-only paste to empty", () => {
    expect(normalizeWebKey("   \n\t ")).toBe("");
    expect(normalizeWebKey("")).toBe("");
  });
});

describe("stored web key persistence", () => {
  it("loads an empty string when nothing is stored", () => {
    expect(loadStoredWebKey(new FakeStorage())).toBe("");
  });

  it("stores the normalized key under the dedicated namespace", () => {
    const storage = new FakeStorage();
    saveStoredWebKey(storage, "  key-123\n");
    expect(storage.getItem(WEB_KEY_STORAGE_KEY)).toBe("key-123");
    expect(loadStoredWebKey(storage)).toBe("key-123");
  });

  it("never persists an empty or whitespace-only value", () => {
    const storage = new FakeStorage();
    saveStoredWebKey(storage, "   ");
    expect(storage.keys()).toEqual([]);
  });

  it("clears a stored key", () => {
    const storage = new FakeStorage();
    saveStoredWebKey(storage, "key-123");
    clearStoredWebKey(storage);
    expect(loadStoredWebKey(storage)).toBe("");
  });
});

describe("verifyWebKey", () => {
  it("accepts a key when /health and the guarded surface succeed", async () => {
    const calls: RecordedCall[] = [];
    const result = await verifyWebKey(deps(HEALTHY, calls), "  key-abc\n");

    expect(result).toEqual({ ok: true, key: "key-abc", verified: true });
    expect(calls.find((c) => c.url === HEALTH_URL)?.authorization).toBeNull();
    expect(calls.find((c) => c.url === GUARDED_URL)?.authorization).toBe("Bearer key-abc");
  });

  it("rejects a wrong key and points to regenerate", async () => {
    const result = await verifyWebKey(deps({ ...HEALTHY, [GUARDED_URL]: 401 }), "wrong-key");

    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.kind).toBe("invalid");
      expect(result.message).toContain("cortex web key regenerate");
    }
  });

  it("degrades to unverified when the guarded surface is not deployed yet (404)", async () => {
    const result = await verifyWebKey(deps({ ...HEALTHY, [GUARDED_URL]: 404 }), "key-abc");
    expect(result).toEqual({ ok: true, key: "key-abc", verified: false });
  });

  it("reports an unreachable server when /health cannot be reached", async () => {
    const result = await verifyWebKey(deps({ [HEALTH_URL]: "network" }), "key-abc");
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.kind).toBe("unreachable");
  });

  it("reports degraded readiness when /health is not ok", async () => {
    const result = await verifyWebKey(deps({ ...HEALTHY, [HEALTH_URL]: 503 }), "key-abc");
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.kind).toBe("unavailable");
      expect(result.status).toBe(503);
    }
  });

  it("rejects an empty paste before any network call", async () => {
    const calls: RecordedCall[] = [];
    const result = await verifyWebKey(deps(HEALTHY, calls), "   \n");
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.kind).toBe("empty");
    expect(calls).toEqual([]);
  });
});

describe("submitWebKey persists only after verification", () => {
  it("stores the normalized key on success", async () => {
    const storage = new FakeStorage();
    const result = await submitWebKey({ ...deps(HEALTHY), storage }, "  key-abc\n");

    expect(result.ok).toBe(true);
    expect(storage.getItem(WEB_KEY_STORAGE_KEY)).toBe("key-abc");
  });

  it("stores nothing and surfaces regenerate guidance when the key is rejected", async () => {
    const storage = new FakeStorage();
    const result = await submitWebKey(
      { ...deps({ ...HEALTHY, [GUARDED_URL]: 403 }), storage },
      "wrong-key",
    );

    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.message).toContain("cortex web key regenerate");
    expect(storage.keys()).toEqual([]);
  });

  it("stores an unverified key when the guarded probe degrades, so first boot can proceed", async () => {
    const storage = new FakeStorage();
    const result = await submitWebKey({ ...deps({ ...HEALTHY, [GUARDED_URL]: 404 }), storage }, "key-abc");

    expect(result.ok).toBe(true);
    expect(storage.getItem(WEB_KEY_STORAGE_KEY)).toBe("key-abc");
  });
});

describe("first-boot gate state machine", () => {
  it("starts unauthenticated with no stored key and ready with one", () => {
    expect(initialWebKeyGateState("")).toEqual({ status: "unauthenticated" });
    expect(initialWebKeyGateState("  key-abc \n")).toEqual({
      status: "ready",
      key: "key-abc",
      verified: true,
    });
  });

  it("walks unauthenticated -> verifying -> ready", () => {
    let state = webKeyGateReducer(initialWebKeyGateState(""), { type: "submit", key: " key-abc " });
    expect(state).toEqual({ status: "verifying", key: "key-abc" });

    state = webKeyGateReducer(state, { type: "verified", key: "key-abc", verified: true });
    expect(state).toEqual({ status: "ready", key: "key-abc", verified: true });
  });

  it("walks verifying -> failed and allows a retry", () => {
    let state = webKeyGateReducer(initialWebKeyGateState(""), { type: "submit", key: "wrong" });
    state = webKeyGateReducer(state, {
      type: "rejected",
      kind: "invalid",
      message: "Run cortex web key regenerate",
    });
    expect(state.status).toBe("failed");

    state = webKeyGateReducer(state, { type: "submit", key: "right" });
    expect(state).toEqual({ status: "verifying", key: "right" });
  });

  it("treats an empty submit as an empty failure without leaving the gate", () => {
    const state = webKeyGateReducer(initialWebKeyGateState(""), { type: "submit", key: " \n " });
    expect(state).toMatchObject({ status: "failed", kind: "empty" });
  });

  it("ignores verification events that do not belong to the current state", () => {
    const state = initialWebKeyGateState("");
    expect(webKeyGateReducer(state, { type: "verified", key: "k", verified: true })).toBe(state);
  });

  it("logout returns the gate to unauthenticated", () => {
    const state = webKeyGateReducer(initialWebKeyGateState("key-abc"), { type: "logout" });
    expect(state).toEqual({ status: "unauthenticated" });
  });
});
