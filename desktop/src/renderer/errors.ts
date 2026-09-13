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

/**
 * What `api.call` answers with, instead of throwing across the bridge.
 *
 * contextBridge clones an Error by its message and stack and drops everything
 * else, so a status set on one in the preload arrives as undefined here. Every
 * reader of `statusOf` was therefore being told "no status", which the retry
 * policy reads as "the call never landed" — three retries and a backoff before
 * a plain 404 reached the view.
 *
 * So the failure crosses as data and becomes an Error on this side, where the
 * properties survive.
 */
export interface CallResult<T> {
  ok: boolean
  value?: T
  error?: string
  status?: number
  code?: string
}

/** Rebuilds the failure as an Error in this realm, with its status intact. */
export function unwrap<T>(result: CallResult<T>, what: string): T {
  if (result.ok) return result.value as T
  const error = new Error(result.error ?? `${what} failed`) as BridgeError
  error.status = result.status
  error.code = result.code
  throw error
}

export function statusOf(err: unknown): number | undefined {
  return (err as BridgeError | undefined)?.status
}
