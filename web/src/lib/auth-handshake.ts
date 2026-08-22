// Pure login/refresh handshake logic for the auth provider.
//
// Extracted from React so the security-critical semantics are directly
// testable:
//   * a 401 anywhere during the handshake is TERMINAL — the caller must
//     never restore the token or connected state afterwards;
//   * non-auth failures (e.g. 403, missing endpoints) degrade gracefully;
//   * refreshes that observe a 401 flag the session as expired so the
//     caller skips writing stale snapshots over the cleared state.

import type { Principal, ServerStats } from "./api";

/** Anything the API client can call during login/refresh. */
export interface HandshakeClient {
  health(): Promise<unknown>;
  me(): Promise<Principal>;
  stats(): Promise<ServerStats>;
}

export type HandshakeResult =
  | { ok: true; principal: Principal | null; stats: ServerStats | null }
  | { ok: false; reason: "unauthorized" | "error"; message: string };

export interface RefreshSnapshot {
  principal: Principal | null;
  stats: ServerStats | null;
  expired: boolean;
}

export function isUnauthorizedError(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    (err as { status?: unknown }).status === 401
  );
}

function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}

/**
 * Runs the login sequence: health probe, then /api/me and /api/stats.
 * A 401 from me/stats is terminal (`reason: "unauthorized"`) so the caller
 * can never resurrect the token/connected state the client just cleared.
 */
export async function runLoginHandshake(cli: HandshakeClient): Promise<HandshakeResult> {
  try {
    await cli.health();
  } catch (err) {
    return { ok: false, reason: "error", message: messageOf(err) };
  }

  let principal: Principal | null = null;
  let stats: ServerStats | null = null;
  try {
    principal = await cli.me();
  } catch (err) {
    if (isUnauthorizedError(err)) {
      return { ok: false, reason: "unauthorized", message: messageOf(err) };
    }
    principal = null;
  }
  try {
    stats = await cli.stats();
  } catch (err) {
    if (isUnauthorizedError(err)) {
      return { ok: false, reason: "unauthorized", message: messageOf(err) };
    }
    stats = null;
  }
  return { ok: true, principal, stats };
}

/**
 * Refreshes the principal/stats snapshot. Any observed 401 flags the
 * session as expired; the caller must not write the (stale) snapshot.
 */
export async function refreshSnapshot(cli: HandshakeClient): Promise<RefreshSnapshot> {
  let expired = false;
  const swallow = (err: unknown): null => {
    if (isUnauthorizedError(err)) {
      expired = true;
    }
    return null;
  };
  const [principal, stats] = await Promise.all([
    cli.me().catch(swallow),
    cli.stats().catch(swallow),
  ]);
  return { principal, stats, expired };
}
