// Pure reset-generation policy for secret-holding form inputs.
//
// A value-keyed mirror effect (clear the typed copy when the provider's
// value is "") cannot clear independently typed state when the provider
// value is ALREADY empty — the exact initial-login 401 case, where the
// handshake rejects the credentials before any token is ever committed.
// Secret-holding forms therefore key their reset on the auth provider's
// reset generation, which advances on every terminal auth event
// (logout, 401 invalidation, or a superseded/failed attempt).
//
// Extracted from React so the policy is directly unit-testable in the
// node test environment, and consumed verbatim by AppShell's input state.

/** Typed secret input plus the reset generation it last settled on. */
export interface SecretInputState {
  typed: string;
  lastResetGeneration: number;
}

/** Initial form state: the current typed value at the current generation. */
export function initialSecretInput(typed: string, generation: number): SecretInputState {
  return { typed, lastResetGeneration: generation };
}

/**
 * Applies the reset-generation policy to a secret input: when the
 * provider's generation advanced since the input last settled, the typed
 * secret is wiped — even if the provider's own token value never changed.
 * Re-observing an unchanged generation (re-render) preserves typing.
 */
export function observeResetGeneration(
  state: SecretInputState,
  generation: number,
): SecretInputState {
  if (generation === state.lastResetGeneration) {
    return state;
  }
  return { typed: "", lastResetGeneration: generation };
}
