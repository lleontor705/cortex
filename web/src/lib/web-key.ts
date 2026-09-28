// Client half of D0 first-boot onboarding for the embedded web surface.
//
// The web access key is a low-privilege UI credential in its OWN namespace
// (REQ-KEY-004): it is deliberately persisted so the paste-once gate never
// re-prompts, unlike bearer tokens and LLM keys which stay memory-only in
// `auth-context`. Verification probes `/health` (unauthenticated) and then one
// web-key-guarded surface; while that guarded surface does not exist yet, a
// 404 deterministically degrades to `verified: false` instead of blocking.
// All I/O is injected so the policy runs in the node Vitest environment.

export const WEB_KEY_STORAGE_KEY = "cortex_web_key";
export const DEFAULT_HEALTH_PATH = "/health";

/**
 * The web-key-guarded UI surface. The Go integration guards the UI surface
 * with bearer-style web-key middleware (design.md D1); the probe sends the key
 * as `Authorization: Bearer <key>` and treats absent surfaces (404/405/501) as
 * degraded-but-acceptable so first boot works before the mount lands.
 */
export const DEFAULT_GUARDED_PATH = "/";

export type WebKeyFailureKind = "empty" | "unreachable" | "unavailable" | "invalid";

const MESSAGES: Record<WebKeyFailureKind, string> = {
  empty: "Paste the web key printed by 'cortex serve' when it first started.",
  unreachable:
    "Could not reach the Cortex server. Confirm 'cortex serve' is running, then paste the web key again.",
  unavailable: "The Cortex server responded but is not ready yet. Try again in a moment.",
  invalid:
    "That web key was not accepted. Run 'cortex web key regenerate' on the server, then paste the new key.",
};

export interface WebKeyProbeDeps {
  fetch: typeof fetch;
  baseUrl: string;
  healthPath?: string;
  guardedPath?: string;
}

export type WebKeyVerification =
  | { ok: true; key: string; verified: boolean }
  | { ok: false; kind: WebKeyFailureKind; status?: number; message: string };

export function normalizeWebKey(raw: string): string {
  return raw.trim();
}

export function loadStoredWebKey(storage: Pick<Storage, "getItem">): string {
  return normalizeWebKey(storage.getItem(WEB_KEY_STORAGE_KEY) ?? "");
}

export function saveStoredWebKey(storage: Pick<Storage, "setItem">, key: string): void {
  const normalized = normalizeWebKey(key);
  if (!normalized) return;
  storage.setItem(WEB_KEY_STORAGE_KEY, normalized);
}

export function clearStoredWebKey(storage: Pick<Storage, "removeItem">): void {
  storage.removeItem(WEB_KEY_STORAGE_KEY);
}

function joinPath(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/+$/, "")}${path}`;
}

async function probe(
  deps: WebKeyProbeDeps,
  url: string,
  headers?: Record<string, string>,
): Promise<Response | null> {
  try {
    return await deps.fetch(url, { method: "GET", cache: "no-store", headers });
  } catch {
    return null;
  }
}

export async function verifyWebKey(
  deps: WebKeyProbeDeps,
  rawKey: string,
): Promise<WebKeyVerification> {
  const key = normalizeWebKey(rawKey);
  if (!key) {
    return { ok: false, kind: "empty", message: MESSAGES.empty };
  }

  const healthUrl = joinPath(deps.baseUrl, deps.healthPath ?? DEFAULT_HEALTH_PATH);
  const guardedUrl = joinPath(deps.baseUrl, deps.guardedPath ?? DEFAULT_GUARDED_PATH);

  const health = await probe(deps, healthUrl);
  if (!health) {
    return { ok: false, kind: "unreachable", message: MESSAGES.unreachable };
  }
  if (!health.ok) {
    return { ok: false, kind: "unavailable", status: health.status, message: MESSAGES.unavailable };
  }

  const guarded = await probe(deps, guardedUrl, { Authorization: `Bearer ${key}` });
  if (!guarded) {
    return { ok: false, kind: "unreachable", message: MESSAGES.unreachable };
  }
  if (guarded.status === 401 || guarded.status === 403) {
    return { ok: false, kind: "invalid", status: guarded.status, message: MESSAGES.invalid };
  }
  if (guarded.status === 404 || guarded.status === 405 || guarded.status === 501) {
    return { ok: true, key, verified: false };
  }
  if (!guarded.ok) {
    return {
      ok: false,
      kind: "unavailable",
      status: guarded.status,
      message: MESSAGES.unavailable,
    };
  }
  return { ok: true, key, verified: true };
}

export interface WebKeySubmitDeps extends WebKeyProbeDeps {
  storage: Pick<Storage, "setItem">;
}

/** Verifies, and ONLY on success persists the normalized key. */
export async function submitWebKey(
  deps: WebKeySubmitDeps,
  rawKey: string,
): Promise<WebKeyVerification> {
  const result = await verifyWebKey(deps, rawKey);
  if (result.ok) {
    saveStoredWebKey(deps.storage, result.key);
  }
  return result;
}

export type WebKeyGateState =
  | { status: "unauthenticated" }
  | { status: "verifying"; key: string }
  | { status: "ready"; key: string; verified: boolean }
  | { status: "failed"; kind: WebKeyFailureKind; message: string };

export type WebKeyGateEvent =
  | { type: "submit"; key: string }
  | { type: "verified"; key: string; verified: boolean }
  | { type: "rejected"; kind: WebKeyFailureKind; message: string }
  | { type: "logout" };

export function initialWebKeyGateState(storedKey: string): WebKeyGateState {
  const key = normalizeWebKey(storedKey);
  return key ? { status: "ready", key, verified: true } : { status: "unauthenticated" };
}

export function webKeyGateReducer(
  state: WebKeyGateState,
  event: WebKeyGateEvent,
): WebKeyGateState {
  switch (event.type) {
    case "submit": {
      const key = normalizeWebKey(event.key);
      return key
        ? { status: "verifying", key }
        : { status: "failed", kind: "empty", message: MESSAGES.empty };
    }
    case "verified":
      return state.status === "verifying"
        ? { status: "ready", key: event.key, verified: event.verified }
        : state;
    case "rejected":
      return state.status === "verifying"
        ? { status: "failed", kind: event.kind, message: event.message }
        : state;
    case "logout":
      return { status: "unauthenticated" };
  }
}
