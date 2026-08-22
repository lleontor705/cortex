import { describe, expect, it, vi } from "vitest";
import { AuthAttemptCoordinator } from "./auth-attempts";

/** Injectable stand-in for CortexClient's invalidation surface. */
function fakeClient() {
  return { invalidate: vi.fn() };
}

describe("AuthAttemptCoordinator", () => {
  it("registers a new attempt as current and owned by its client", () => {
    const c = new AuthAttemptCoordinator();
    const cli = fakeClient();
    const epoch = c.begin(cli);
    expect(c.isCurrent(epoch)).toBe(true);
    expect(c.owns(cli)).toBe(true);
  });

  it("a newer attempt supersedes the pending one: old client aborted, old epoch terminal", () => {
    const c = new AuthAttemptCoordinator();
    const a = fakeClient();
    const b = fakeClient();
    const epochA = c.begin(a);
    const epochB = c.begin(b);

    expect(a.invalidate).toHaveBeenCalledOnce();
    expect(c.isCurrent(epochA)).toBe(false);
    expect(c.owns(a)).toBe(false);
    expect(c.isCurrent(epochB)).toBe(true);

    // The stale attempt must not be allowed to commit.
    expect(c.finish(epochA, a)).toBe(false);
    expect(a.invalidate).toHaveBeenCalledTimes(2);
    // The newer attempt still commits.
    expect(c.finish(epochB, b)).toBe(true);
    expect(b.invalidate).not.toHaveBeenCalled();
  });

  it("logout/401 supersede makes a pending handshake terminal and aborts its client", () => {
    const c = new AuthAttemptCoordinator();
    const cli = fakeClient();
    const epoch = c.begin(cli);

    c.supersede();

    expect(cli.invalidate).toHaveBeenCalledOnce();
    expect(c.isCurrent(epoch)).toBe(false);
    expect(c.owns(cli)).toBe(false);
    expect(c.finish(epoch, cli)).toBe(false);
  });

  it("a late 401 callback from a superseded attempt cannot clear newer state", () => {
    const c = new AuthAttemptCoordinator();
    const a = fakeClient();
    const epochA = c.begin(a);
    const b = fakeClient();
    c.begin(b);

    // The provider's 401 callback consults isCurrent before sweeping state:
    // a stale callback must observe false and leave the newer slot alone.
    expect(c.isCurrent(epochA)).toBe(false);
    expect(c.owns(b)).toBe(true);
  });

  it("abandon retires a failed attempt: slot dropped, client aborted, late callbacks inert", () => {
    const c = new AuthAttemptCoordinator();
    const cli = fakeClient();
    const epoch = c.begin(cli);

    c.abandon(epoch, cli);

    expect(cli.invalidate).toHaveBeenCalledOnce();
    expect(c.isCurrent(epoch)).toBe(false);
    expect(c.owns(cli)).toBe(false);
  });

  it("abandon of an already-superseded attempt does not disturb the newer slot", () => {
    const c = new AuthAttemptCoordinator();
    const a = fakeClient();
    const epochA = c.begin(a);
    const b = fakeClient();
    const epochB = c.begin(b);

    c.abandon(epochA, a);

    expect(c.isCurrent(epochB)).toBe(true);
    expect(c.owns(b)).toBe(true);
    expect(c.finish(epochB, b)).toBe(true);
  });

  it("finish keeps the slot for the committed session so runtime 401s stay authoritative", () => {
    const c = new AuthAttemptCoordinator();
    const cli = fakeClient();
    const epoch = c.begin(cli);

    expect(c.finish(epoch, cli)).toBe(true);
    // Post-commit, the client still owns the slot (runtime refresh 401s and
    // snapshot writes are gated on this).
    expect(c.owns(cli)).toBe(true);
    expect(c.isCurrent(epoch)).toBe(true);
  });
});
