// Pure concurrency coordinator for the auth provider's login attempts.
//
// Extracted from React so the security-critical terminal-auth semantics are
// directly testable in the node test environment:
//   * starting a new attempt, a logout, or a 401 invalidation supersedes every
//     earlier attempt (epoch bump + pending client abort);
//   * an in-flight handshake may only commit if it still owns the session
//     slot when it settles — a stale attempt can never resurrect a session
//     after logout, a 401, or a newer attempt;
//   * after a successful commit the client keeps the slot so runtime 401s
//     stay authoritative, while late callbacks from superseded attempts
//     observe `isCurrent === false` and must not touch newer state;
//   * every terminal supersede advances the reset generation and notifies
//     the observer, giving secret-holding components an explicit reset
//     EVENT (not a value mirror) to clear locally typed secrets even when
//     the provider's own token value was already empty.

/** Anything with CortexClient's invalidation surface (token clear + abort). */
export interface InvalidationTarget {
  invalidate(): void;
}

/** Notified once with the new epoch every time the generation advances. */
export type ResetGenerationObserver = (generation: number) => void;

export class AuthAttemptCoordinator {
  private epoch = 0;
  private slot: { client: InvalidationTarget; epoch: number } | null = null;

  constructor(private readonly onEpochAdvance?: ResetGenerationObserver) {}

  /** Current reset generation (starts at 0, strictly monotonic). */
  currentEpoch(): number {
    return this.epoch;
  }

  /**
   * Starts a new attempt for `client`. Any pending attempt is superseded
   * first: its client is aborted and its epoch becomes permanently stale.
   */
  begin(client: InvalidationTarget): number {
    this.supersede();
    this.slot = { client, epoch: this.epoch };
    return this.epoch;
  }

  /**
   * Terminal transition used by logout and 401 invalidation: bumps the
   * epoch, aborts the slot client (pending handshake or committed session),
   * clears the slot so nothing in flight can commit afterwards, and emits
   * the reset-generation event so secret-holding forms wipe typed copies.
   */
  supersede(): void {
    this.epoch += 1;
    this.slot?.client.invalidate();
    this.slot = null;
    this.onEpochAdvance?.(this.epoch);
  }

  /** True iff `epoch` still owns the session slot (un-superseded). */
  isCurrent(epoch: number): boolean {
    return this.slot !== null && this.slot.epoch === epoch;
  }

  /** True iff `client` is the slot holder (gates stale snapshot writes). */
  owns(client: InvalidationTarget): boolean {
    return this.slot?.client === client;
  }

  /**
   * Ends the in-flight phase of a handshake. Returns whether the attempt
   * may commit session state; a superseded attempt is invalidated again
   * (defense in depth) and must commit nothing.
   */
  finish(epoch: number, client: InvalidationTarget): boolean {
    if (!this.isCurrent(epoch)) {
      client.invalidate();
      return false;
    }
    return true;
  }

  /**
   * Retires a failed attempt: aborts the client and, if it still held the
   * slot, drops it so no late callback or retry can resurrect it.
   */
  abandon(epoch: number, client: InvalidationTarget): void {
    if (this.isCurrent(epoch)) {
      // supersede() aborts the slot client (this one) exactly once.
      this.supersede();
      return;
    }
    // Stale already: its superseders aborted it, but invalidate again
    // defensively in case it was never registered in the slot.
    client.invalidate();
  }
}
