/**
 * Compares dotted numeric versions, as the release tags carry them.
 *
 * Here rather than beside the update checker so it can be tested without
 * Electron: the checker imports `app` for the running version, and importing
 * that outside a running app fails.
 */
export function isNewer(candidate: string, current: string): boolean {
  const left = candidate.split('.').map((part) => parseInt(part, 10) || 0)
  const right = current.split('.').map((part) => parseInt(part, 10) || 0)
  for (let i = 0; i < Math.max(left.length, right.length); i++) {
    const a = left[i] ?? 0
    const b = right[i] ?? 0
    if (a !== b) return a > b
  }
  return false
}
