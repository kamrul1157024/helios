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

/**
 * Every release the reader has not got yet, newest first.
 *
 * A reader three releases behind is owed all three sets of notes, not the
 * newest one — the versions in between are what they are about to install.
 * Sorted here rather than trusted from the API, so a repo with a hand-made tag
 * order still reads newest to oldest.
 */
export function releasesSince<T extends { version: string }>(releases: T[], current: string): T[] {
  return releases
    .filter((release) => isNewer(release.version, current))
    .sort((a, b) => (isNewer(a.version, b.version) ? -1 : isNewer(b.version, a.version) ? 1 : 0))
}
