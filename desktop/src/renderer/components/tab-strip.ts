// The rules behind the file tabs, kept out of the component so they can be
// asserted: which folder suffix tells two `index.ts` apart, which tab takes the
// front when one closes, and what the reopen and recent lists hold.

/** What a tab shows: its file name, and the folder suffix when the name repeats. */
export interface TabLabel {
  name: string
  /** Empty unless another tab has the same name. */
  suffix: string
}

/**
 * The shortest trailing run of folders that sets each path apart from every
 * other open path with the same file name.
 *
 * `desktop/src/index.ts` beside `mobile/src/index.ts` reads `src` for both
 * until it is one more folder up — so the two get `desktop/src` and
 * `mobile/src`. A name nobody else has gets no suffix at all.
 */
export function tabLabels(paths: string[]): Record<string, TabLabel> {
  const out: Record<string, TabLabel> = {}
  const byName = new Map<string, string[]>()
  for (const path of paths) {
    const name = baseName(path)
    byName.set(name, [...(byName.get(name) ?? []), path])
  }
  for (const [name, group] of byName) {
    if (group.length === 1) {
      out[group[0] as string] = { name, suffix: '' }
      continue
    }
    const folders = group.map((path) => path.split('/').filter(Boolean).slice(0, -1))
    group.forEach((path, index) => {
      const own = folders[index] as string[]
      let depth = 1
      while (depth < own.length) {
        const tail = own.slice(-depth).join('/')
        const clash = folders.some((other, at) => at !== index && other.slice(-depth).join('/') === tail)
        if (!clash) break
        depth += 1
      }
      out[path] = { name, suffix: own.slice(-depth).join('/') }
    })
  }
  return out
}

/**
 * Which tab takes the front once `closing` goes.
 *
 * The one to its right, as an editor does, and the one to its left when it was
 * last. Closing a tab that is not in front changes nothing about which is.
 */
export function nextActive(paths: string[], closing: string, active: string | null): string | null {
  if (active !== closing) return active
  const at = paths.indexOf(closing)
  const remaining = paths.filter((path) => path !== closing)
  if (remaining.length === 0) return null
  return remaining[Math.min(Math.max(at, 0), remaining.length - 1)] ?? null
}

/** How many closed tabs ⌥⇧T can bring back. */
export const CLOSED_LIMIT = 20

/** How many recently opened files quick open offers on an empty query. */
export const RECENT_LIMIT = 20

/**
 * Puts one item at the front of a most-recent-first list, without repeating
 * it, and drops what falls off the end.
 */
export function pushRecent<T>(list: T[], item: T, limit: number, same: (a: T, b: T) => boolean = Object.is): T[] {
  return [item, ...list.filter((held) => !same(held, item))].slice(0, limit)
}

export function baseName(path: string): string {
  return path.split('/').pop() || path
}
