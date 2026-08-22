import { describe, expect, it } from "vitest";
import { APIError, type Principal, type ServerStats } from "./api";
import {
  isUnauthorizedError,
  refreshSnapshot,
  runLoginHandshake,
  type HandshakeClient,
} from "./auth-handshake";

const principal: Principal = {
  id: "p1",
  type: "user",
  org_id: "org",
  workspaces: ["w"],
  projects: ["proj"],
  roles: ["developer"],
  scopes: ["observations:read"],
  classification_clearance: [],
  auth_method: "token",
};

const stats: ServerStats = {
  observations: 1,
  sessions: 1,
  active_sessions: 1,
  edges: 0,
  projects: 1,
};

const unauthorized = () => Promise.reject(new APIError("expired", 401));
const forbidden = () => Promise.reject(new APIError("forbidden", 403));

function fakeClient(overrides: Partial<HandshakeClient> = {}): HandshakeClient {
  return {
    health: () => Promise.resolve({ status: "ok" }),
    me: () => Promise.resolve(principal),
    stats: () => Promise.resolve(stats),
    ...overrides,
  };
}

describe("isUnauthorizedError", () => {
  it("recognizes 401 APIErrors and rejects everything else", () => {
    expect(isUnauthorizedError(new APIError("expired", 401))).toBe(true);
    expect(isUnauthorizedError(new APIError("forbidden", 403))).toBe(false);
    expect(isUnauthorizedError(new Error("network down"))).toBe(false);
    expect(isUnauthorizedError(null)).toBe(false);
    expect(isUnauthorizedError("401")).toBe(false);
  });
});

describe("runLoginHandshake", () => {
  it("returns the principal and stats on a successful handshake", async () => {
    const result = await runLoginHandshake(fakeClient({}));
    expect(result).toEqual({ ok: true, principal, stats });
  });

  it("treats a 401 from /api/me as terminal: never reports a connectable session", async () => {
    const result = await runLoginHandshake(fakeClient({ me: unauthorized }));
    expect(result).toMatchObject({ ok: false, reason: "unauthorized" });
  });

  it("treats a 401 from /api/stats as terminal: never reports a connectable session", async () => {
    const result = await runLoginHandshake(fakeClient({ stats: unauthorized }));
    expect(result).toMatchObject({ ok: false, reason: "unauthorized" });
  });

  it("degrades non-auth me/stats failures to null instead of failing the login", async () => {
    const result = await runLoginHandshake(
      fakeClient({ me: forbidden, stats: forbidden }),
    );
    expect(result).toEqual({ ok: true, principal: null, stats: null });
  });

  it("reports an error when the health probe fails before authentication", async () => {
    const result = await runLoginHandshake(
      fakeClient({ health: () => Promise.reject(new Error("connection refused")) }),
    );
    expect(result).toMatchObject({ ok: false, reason: "error" });
  });
});

describe("refreshSnapshot", () => {
  it("returns refreshed principal and stats", async () => {
    const snapshot = await refreshSnapshot(fakeClient({}));
    expect(snapshot).toEqual({ principal, stats, expired: false });
  });

  it("flags the session as expired when any refresh call returns 401", async () => {
    const snapshot = await refreshSnapshot(fakeClient({ me: unauthorized }));
    expect(snapshot.expired).toBe(true);
  });

  it("does not flag expiry on non-401 failures", async () => {
    const snapshot = await refreshSnapshot(
      fakeClient({ me: forbidden, stats: () => Promise.reject(new Error("boom")) }),
    );
    expect(snapshot).toEqual({ principal: null, stats: null, expired: false });
  });
});
