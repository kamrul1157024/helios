import type { Schedule } from '../../shared/models.ts'

/**
 * The after-chain as a tree, and which of it is showing.
 *
 * A job that follows another is drawn under it, indented. A long chain — a
 * rebrand run split into thirty steps, each waiting on the last — then walks
 * off the right edge of the sidebar and buries every other schedule on the
 * host. Folding a root hides what follows it; the count on the row says how
 * much.
 */

/** How deep in the chain each schedule sits, so a grandchild indents twice. */
export function depthOf(schedules: Schedule[]): Record<string, number> {
  const parent: Record<string, string> = {}
  for (const sc of schedules) parent[sc.id] = sc.after_id ?? ''
  const depth: Record<string, number> = {}
  for (const sc of schedules) {
    let n = 0
    // Bounded: after_id is editable, so a cycle in it must not become a hang.
    for (let at = sc.after_id ?? ''; at && n < 16; at = parent[at] ?? '') n++
    depth[sc.id] = n
  }
  return depth
}

/** How many jobs hang off each one, at any depth below it. */
export function followerCounts(schedules: Schedule[]): Record<string, number> {
  const children: Record<string, string[]> = {}
  for (const sc of schedules) {
    const parent = sc.after_id ?? ''
    if (!parent) continue
    ;(children[parent] ??= []).push(sc.id)
  }

  const counts: Record<string, number> = {}
  const count = (id: string, seen: Set<string>): number => {
    if (counts[id] !== undefined) return counts[id]
    if (seen.has(id)) return 0
    seen.add(id)
    let total = 0
    for (const child of children[id] ?? []) total += 1 + count(child, seen)
    counts[id] = total
    return total
  }
  for (const sc of schedules) count(sc.id, new Set())
  return counts
}

/**
 * The rows to draw, given which roots are folded.
 *
 * A schedule is hidden when anything above it in the chain is folded — folding
 * a root has to take its grandchildren with it, or the chain reappears one
 * level down with nothing above it to explain the indent.
 */
export function visibleSchedules(schedules: Schedule[], folded: ReadonlySet<string>): Schedule[] {
  const byId = new Map(schedules.map((sc) => [sc.id, sc]))
  const shown = (sc: Schedule): boolean => {
    const seen = new Set<string>([sc.id])
    for (let at = sc.after_id ?? ''; at && !seen.has(at); ) {
      if (folded.has(at)) return false
      seen.add(at)
      at = byId.get(at)?.after_id ?? ''
    }
    return true
  }
  return schedules.filter(shown)
}

/**
 * Past this depth the indent stops growing.
 *
 * The nesting is worth showing; the pixels are not. A thirty-deep chain
 * indented all the way leaves no width for the name, which is the one thing
 * the row exists to say.
 */
export const MAX_INDENT_DEPTH = 4

export function indentFor(depth: number): number {
  return 10 + Math.min(depth, MAX_INDENT_DEPTH) * 12
}
