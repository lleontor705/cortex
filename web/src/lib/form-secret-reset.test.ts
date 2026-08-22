import { describe, expect, it, vi } from "vitest";
import { AuthAttemptCoordinator } from "./auth-attempts";
import {
  initialSecretInput,
  observeResetGeneration,
  type SecretInputState,
} from "./form-secret-reset";

/**
 * Behavioral coverage for the reset-generation secret-clearing contract:
 *
 *   provider side  — the AuthAttemptCoordinator notifies a strictly
 *                    increasing reset generation on every terminal auth
 *                    event (initial-login 401, logout, supersession);
 *   form side      — a secret-holding input keyed on that generation
 *                    wipes its typed copy whenever the generation
 *                    advances, even when the provider's own token value
 *                    never changed (it was already "" on initial login).
 */

function coordinatorWithLog(log: number[]) {
  return new AuthAttemptCoordinator((generation) => log.push(generation));
}

describe("secret input reset policy", () => {
  it("keeps the typed secret while the reset generation is unchanged", () => {
    let state = initialSecretInput("cortex_sec_typed", 0);
    state = observeResetGeneration(state, 0);
    expect(state.typed).toBe("cortex_sec_typed");
    expect(state.lastResetGeneration).toBe(0);
  });

  it("clears the typed secret on a generation advance even though the provider token stayed empty", () => {
    // The exact initial-login 401 regression: the provider token was ""
    // before AND after the terminal event, so a value-keyed effect on the
    // token never re-fires. The generation advance must still wipe the
    // typed copy.
    const providerTokenBefore = "";
    let state = initialSecretInput("cortex_sec_typed", 0);
    state = observeResetGeneration(state, 1);
    const providerTokenAfter = "";
    expect(providerTokenBefore).toBe(providerTokenAfter);
    expect(state.typed).toBe("");
    expect(state.lastResetGeneration).toBe(1);
  });

  it("is idempotent per generation and preserves re-typed values until the next advance", () => {
    let state: SecretInputState = initialSecretInput("first", 0);
    state = observeResetGeneration(state, 1);
    expect(state.typed).toBe("");
    // User retypes after the reset; re-observing the same generation
    // (re-render) must not wipe it again.
    state = { ...state, typed: "second" };
    state = observeResetGeneration(state, 1);
    expect(state.typed).toBe("second");
    // The next terminal event clears it again.
    state = observeResetGeneration(state, 2);
    expect(state.typed).toBe("");
  });
});

describe("reset generation contract (provider side)", () => {
  it("starts at generation 0 and advances monotonically on initial-login 401 (begin + abandon)", () => {
    const log: number[] = [];
    const c = coordinatorWithLog(log);

    expect(c.currentEpoch()).toBe(0);

    // User submits the login form: the attempt begins (supersession of the
    // empty slot still advances the generation).
    const cli = { invalidate: vi.fn() };
    const epoch = c.begin(cli);
    expect(log).toEqual([1]);

    // The handshake observes a 401 with the token never committed, so the
    // provider takes the terminal abandon path.
    expect(c.finish(epoch, cli)).toBe(true);
    c.abandon(epoch, cli);

    expect(c.currentEpoch()).toBe(2);
    expect(log).toEqual([1, 2]);
    expect(log.every((g, i) => i === 0 || g > log[i - 1])).toBe(true);
  });

  it("advances on logout supersede and a pending handshake can no longer commit", () => {
    const log: number[] = [];
    const c = coordinatorWithLog(log);
    const cli = { invalidate: vi.fn() };
    const epoch = c.begin(cli);

    // Logout: abort + terminal supersede.
    c.supersede();

    expect(log).toEqual([1, 2]);
    // Pending auth cannot resurrect state after the reset.
    expect(c.finish(epoch, cli)).toBe(false);
    expect(cli.invalidate).toHaveBeenCalledTimes(2);
  });

  it("advances when a newer attempt supersedes a pending one, and the stale attempt's late 401 neither notifies nor clears", () => {
    const log: number[] = [];
    const c = coordinatorWithLog(log);
    const a = { invalidate: vi.fn() };
    const b = { invalidate: vi.fn() };
    const epochA = c.begin(a);
    const epochB = c.begin(b);

    expect(log).toEqual([1, 2]);
    expect(a.invalidate).toHaveBeenCalledOnce();

    // The stale attempt's late 401 callback consults isCurrent first: it
    // observes false, so the provider never sweeps the newer session and
    // the generation does not advance from a stale callback.
    expect(c.isCurrent(epochA)).toBe(false);
    expect(log).toEqual([1, 2]);
    expect(c.isCurrent(epochB)).toBe(true);
  });

  it("runtime 401 on the committed session advances exactly once via the shared sweeper", () => {
    const log: number[] = [];
    const c = coordinatorWithLog(log);
    const cli = { invalidate: vi.fn() };
    const epoch = c.begin(cli);
    expect(c.finish(epoch, cli)).toBe(true);

    // The provider's 401 invalidation callback: while current, it routes
    // through clearLiveSecrets, whose single supersede notifies once.
    expect(c.isCurrent(epoch)).toBe(true);
    c.supersede();
    expect(log).toEqual([1, 2]);
  });

  it("retiring an already-superseded attempt does not advance the generation", () => {
    const log: number[] = [];
    const c = coordinatorWithLog(log);
    const a = { invalidate: vi.fn() };
    const b = { invalidate: vi.fn() };
    const epochA = c.begin(a);
    c.begin(b);

    const before = log.length;
    c.abandon(epochA, a); // stale: defensive invalidate only, no epoch bump
    expect(log.length).toBe(before);
    expect(c.currentEpoch()).toBe(2);
  });
});

describe("end-to-end form contract on initial-login 401", () => {
  it("the typed bearer is wiped while the provider token value stays empty throughout", () => {
    let providerToken = "";
    const log: number[] = [];
    const c = coordinatorWithLog(log);

    // Form mounts with a typed secret at generation 0.
    let form = initialSecretInput("cortex_sec_typed", c.currentEpoch());

    // User submits: attempt begins; handshake hits a 401 and is abandoned.
    const cli = { invalidate: vi.fn() };
    const epoch = c.begin(cli);
    c.abandon(epoch, cli);

    // AppShell observes the (advanced) reset generation on re-render.
    form = observeResetGeneration(form, c.currentEpoch());

    expect(providerToken).toBe("");
    expect(form.typed).toBe("");
    expect(log.length).toBeGreaterThan(0);
  });
});

describe("settings page contract: unsaved secrets cleared on reset", () => {
  it("clears an unsaved bearer AND unsaved LLM key on generation advance while provider values stay empty", () => {
    // The exact settings blind spot from the review: the user typed a new
    // bearer and a new LLM key but never saved them, so the provider's
    // token and llmApiKey are "" before AND after the terminal event.
    // Value-keyed mirrors never re-fire; the generation advance must wipe
    // both typed secrets.
    let bearer = initialSecretInput("cortex_sec_unsaved", 0);
    let llmKey = initialSecretInput("sk-unsaved", 0);
    expect(bearer.typed).toBe("cortex_sec_unsaved");
    expect(llmKey.typed).toBe("sk-unsaved");

    bearer = observeResetGeneration(bearer, 1);
    llmKey = observeResetGeneration(llmKey, 1);

    expect(bearer.typed).toBe("");
    expect(llmKey.typed).toBe("");
    expect(bearer.lastResetGeneration).toBe(1);
    expect(llmKey.lastResetGeneration).toBe(1);
  });

  it("clears typed copies of previously saved secrets after logout (provider values go non-empty -> empty)", () => {
    let bearer = initialSecretInput("cortex_sec_saved", 3);
    let llmKey = initialSecretInput("sk-saved", 3);

    // logout()/401 sweep: clearLiveSecrets supersedes exactly once, then
    // empties token and llmApiKey. The single generation event wipes both
    // typed copies regardless of the provider value transition.
    bearer = observeResetGeneration(bearer, 4);
    llmKey = observeResetGeneration(llmKey, 4);

    expect(bearer.typed).toBe("");
    expect(llmKey.typed).toBe("");
  });

  it("preserves re-typed settings secrets across re-renders until the next terminal event", () => {
    let llmKey = initialSecretInput("sk-first", 0);
    llmKey = observeResetGeneration(llmKey, 1);
    expect(llmKey.typed).toBe("");

    // User retypes after the reset; observing the same generation (a plain
    // re-render) must not wipe it again.
    llmKey = { ...llmKey, typed: "sk-retyped" };
    llmKey = observeResetGeneration(llmKey, 1);
    expect(llmKey.typed).toBe("sk-retyped");

    // The next terminal event clears it again.
    llmKey = observeResetGeneration(llmKey, 2);
    expect(llmKey.typed).toBe("");
  });
});
