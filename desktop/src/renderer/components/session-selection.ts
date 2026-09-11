import { canResume, type Session } from '../../shared/models.ts'

/**
 * Which sessions a bulk action applies to.
 *
 * A selection spans hosts — the list interleaves them — so a member is a host
 * and a session together, and every action has to be issued per host. What is
 * pure here is the arithmetic: what a click adds, what a group header covers,
 * and which actions the selection can carry. The calls themselves are the
 * store's.
 */

export interface SessionRef {
  hostId: string
  sessionId: string
}

/** One member, as a string, so a Set can hold it. */
export function refKey(hostId: string, sessionId: string): string {
  return hostId + ':' + sessionId
}

/**
 * Split at the first colon only.
 *
 * A host id never holds one; a session id can — a shell terminal is
 * `sess-1:sh2` — so splitting on every colon would hand the daemon half an id.
 */
export function parseRef(key: string): SessionRef {
  const at = key.indexOf(':')
  if (at === -1) return { hostId: key, sessionId: '' }
  return { hostId: key.slice(0, at), sessionId: key.slice(at + 1) }
}

/** In or out. The click that does this is ⌘-click, or a tick in the box. */
export function toggle(selection: readonly string[], key: string): string[] {
  return selection.includes(key) ? selection.filter((held) => held !== key) : [...selection, key]
}

/**
 * Everything between the last click and this one, added to what is held.
 *
 * The order is the order the list is drawn in, which is the order a reader
 * means by "these". Shift-clicking backwards selects the same rows as
 * shift-clicking forwards, so the two ends are sorted rather than assumed.
 */
export function extend(
  selection: readonly string[],
  ordered: readonly string[],
  anchor: string | null,
  target: string,
): string[] {
  const to = ordered.indexOf(target)
  const from = anchor === null ? -1 : ordered.indexOf(anchor)
  if (to === -1) return [...selection]
  if (from === -1) return toggle(selection, target)

  const [start, end] = from <= to ? [from, to] : [to, from]
  const range = ordered.slice(start, end + 1)
  return [...selection, ...range.filter((key) => !selection.includes(key))]
}

/**
 * A group header's tick covers the rows under it.
 *
 * Ticking a group whose rows are already all held clears them instead, so the
 * header is a toggle rather than a one-way gate — otherwise the only way to
 * undo it is to untick each row.
 */
export function toggleAll(selection: readonly string[], keys: readonly string[]): string[] {
  const all = keys.length > 0 && keys.every((key) => selection.includes(key))
  if (all) return selection.filter((held) => !keys.includes(held))
  return [...selection, ...keys.filter((key) => !selection.includes(key))]
}

/** Whether a header should draw as ticked, half-ticked, or empty. */
export function coverage(
  selection: readonly string[],
  keys: readonly string[],
): 'none' | 'some' | 'all' {
  if (keys.length === 0) return 'none'
  const held = keys.filter((key) => selection.includes(key)).length
  if (held === 0) return 'none'
  return held === keys.length ? 'all' : 'some'
}

/**
 * Every session a group header covers, at any depth under it.
 *
 * Structural rather than importing GroupNode: the tree is built by the
 * grouping module, which has no business knowing about selection, and this has
 * no business knowing about trees beyond the two fields it walks.
 */
interface TreeNode {
  sessions: { session_id: string }[]
  children: TreeNode[]
}

export function keysInNode(hostId: string, node: TreeNode): string[] {
  const keys = node.sessions.map((session) => refKey(hostId, session.session_id))
  for (const child of node.children) keys.push(...keysInNode(hostId, child))
  return keys
}

/** The members, grouped by the host that has to be asked. */
export function byHost(selection: readonly string[]): Map<string, string[]> {
  const hosts = new Map<string, string[]>()
  for (const key of selection) {
    const { hostId, sessionId } = parseRef(key)
    const held = hosts.get(hostId)
    if (held) held.push(sessionId)
    else hosts.set(hostId, [sessionId])
  }
  return hosts
}

/**
 * What the bar can offer for what is held.
 *
 * Pin reads as one thing or the other rather than both: a mixed selection
 * pins, because the reader who picked ten rows and pressed Pin meant all ten
 * to end up pinned.
 *
 * Groups belong to a host, so filing is only offered when everything held is
 * on one host — there is no group that means the same thing on two daemons.
 */
export function actionsFor(
  selection: readonly string[],
  sessions: readonly { hostId: string; session: Session }[],
): {
  count: number
  hosts: number
  pin: 'pin' | 'unpin'
  canFile: boolean
  canTerminate: boolean
} {
  const held = sessions.filter(({ hostId, session }) =>
    selection.includes(refKey(hostId, session.session_id)),
  )
  const hosts = new Set(held.map(({ hostId }) => hostId))
  return {
    count: held.length,
    hosts: hosts.size,
    pin: held.length > 0 && held.every(({ session }) => session.pinned) ? 'unpin' : 'pin',
    canFile: hosts.size === 1,
    // Nothing to end that has already ended.
    canTerminate: held.some(({ session }) => !canResume(session)),
  }
}
