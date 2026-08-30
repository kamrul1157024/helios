import { QueryClient } from '@tanstack/react-query'

import { statusOf } from './errors.ts'

/**
 * How stale a read may be before mounting it fetches again.
 *
 * The daemon pushes: a session, a group or a notification changing announces
 * itself over SSE and invalidates the key it touched. So a remount is not a
 * reason to ask again, and the default is generous rather than zero. Reads that
 * want a different answer say so at the hook.
 */
export const DEFAULT_STALE_TIME = 60_000

/** Reads whose answer must not move under something the user is holding. */
export const NEVER_STALE = Number.POSITIVE_INFINITY

const MAX_ATTEMPTS = 3

/**
 * Whether a second attempt could plausibly answer differently.
 *
 * A 4xx is the daemon's considered answer and repeating the question gets the
 * same one — and here it is often not even a failure: `readFile` 404s routinely
 * because the agent deletes files, and `listGroups` 404s on a daemon older than
 * grouping. Retrying those spends three round trips to arrive back where we
 * started, and delays the error the view wants to render.
 *
 * No status at all means the call never reached a daemon — the IPC bridge or
 * the network — which is exactly the case a retry is for.
 */
export function retryable(error: unknown): boolean {
  const status = statusOf(error)
  if (status === undefined) return true
  return status >= 500
}

export function shouldRetry(failures: number, error: unknown): boolean {
  return failures < MAX_ATTEMPTS && retryable(error)
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // On in a browser tab, wrong in a desktop app: every alt-tab back to
        // Helios would refetch every mounted query against every paired host.
        refetchOnWindowFocus: false,
        retry: shouldRetry,
        staleTime: DEFAULT_STALE_TIME,
      },
      // A write is not idempotent here. Repeating a failed createSession is how
      // you get two sessions.
      mutations: { retry: false },
    },
  })
}

/**
 * The window's one cache.
 *
 * A module singleton rather than something the root component makes, because
 * the store's SSE handler invalidates against it and the store lives outside
 * React.
 */
export const queryClient = createQueryClient()
