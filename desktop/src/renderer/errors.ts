/**
 * What a failed call carries back, and how to read it.
 *
 * Apart from `bridge.ts` so that code deciding what a status *means* — the
 * retry policy, above all — does not have to pull in a module that binds
 * `window.helios` the moment it loads.
 */

/** Errors from the main process carry the daemon's HTTP status when it had one. */
export interface BridgeError extends Error {
  status?: number
  code?: string
}

export function statusOf(err: unknown): number | undefined {
  return (err as BridgeError | undefined)?.status
}
