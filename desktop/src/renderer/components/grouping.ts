// How the sidebar turns a flat list of sessions into a tree.
//
// Kept out of the component so it can be tested: this is the one part of the
// sidebar that changes what the list contains rather than how it looks.

import type { Session, SessionGroup } from '../../shared/models.ts'

/** A level a session does not reach. Sorts after every real position, which
 *  puts subgroups above loose sessions and ungrouped sessions at the end. */
export const UNRANKED = Number.MAX_SAFE_INTEGER

/**
 * A session's place, as a vector: the position of each group it holds, its own
 * order last, padded to the depth being rendered.
 *
 * Comparing these element by element is the whole ordering model. Moving a
 * group changes one number in the groups table and every session under it moves
 * with it; moving a session changes only its own `sort_order`.
 */
export function rankOf(session: Session, depth: number): number[] {
  const path = (session.group_path ?? []).map((group) => group.position)
  while (path.length < depth) path.push(UNRANKED)
  return [...path.slice(0, depth), session.sort_order ?? 0]
}

export function byRank(a: number[], b: number[]): number {
  for (let i = 0; i < a.length; i += 1) {
    const left = a[i] ?? 0
    const right = b[i] ?? 0
    if (left !== right) return left - right
  }
  return 0
}

/** How deep the tree goes for these sessions: the most groups any one holds. */
export function depthOf(sessions: Session[]): number {
  return sessions.reduce((deepest, session) => Math.max(deepest, session.group_path?.length ?? 0), 0)
}

/** One node of the sidebar tree: a group, the groups inside it, and the
 *  sessions that stop here rather than going deeper. */
export interface GroupNode {
  /** Empty for the synthetic Ungrouped node, which is not a stored group. */
  key: string
  name: string
  position: number
  /** Every key from the root down to here — what a drop target compares. */
  path: string[]
  children: GroupNode[]
  sessions: Session[]
  /** Sessions here and everywhere below, which is what the header counts. */
  total: number
}

const UNGROUPED = 'Ungrouped'

function emptyNode(group: SessionGroup | null, path: string[]): GroupNode {
  return {
    key: group?.key ?? '',
    name: group?.name ?? UNGROUPED,
    position: group?.position ?? UNRANKED,
    path,
    children: [],
    sessions: [],
    total: 0,
  }
}

/**
 * Builds the tree from the group catalogue, then hangs the sessions on it.
 *
 * The catalogue is the source of which nodes exist — not the sessions. Deriving
 * nodes from session paths meant a group with nothing in it did not render, so
 * the first thing anyone does after making one, filing a session into it, had
 * nowhere to happen.
 *
 * Sessions arrive already sorted by the caller, and this only decides where
 * each one hangs.
 */
export function buildTree(sessions: Session[], groups: SessionGroup[] = []): GroupNode[] {
  const nodes = new Map<string, GroupNode>()
  const byKey = new Map<string, SessionGroup>()
  for (const group of groups) byKey.set(group.key, group)

  // Every group becomes a node, empty or not.
  for (const group of groups) {
    nodes.set(group.key, {
      key: group.key,
      name: group.name,
      position: group.position,
      path: [],
      children: [],
      sessions: [],
      total: 0,
    })
  }
  const roots: GroupNode[] = []
  for (const group of groups) {
    const node = nodes.get(group.key)
    if (!node) continue
    const parent = group.parent ? nodes.get(group.parent) : undefined
    node.path = parent ? [...parent.path, group.key] : [group.key]
    if (parent) parent.children.push(node)
    else roots.push(node)
  }

  // Nested first so a child's path is set before anything reads it.
  const chainOf = (key: string | undefined): GroupNode[] => {
    const chain: GroupNode[] = []
    let at = key ? byKey.get(key) : undefined
    const seen = new Set<string>()
    while (at && !seen.has(at.key)) {
      seen.add(at.key)
      const node = nodes.get(at.key)
      if (node) chain.unshift(node)
      at = at.parent ? byKey.get(at.parent) : undefined
    }
    return chain
  }

  let ungrouped: GroupNode | null = null

  for (const session of sessions) {
    let chain = chainOf(session.group_key)
    if (chain.length === 0) {
      if (!ungrouped) {
        ungrouped = emptyNode(null, [])
        roots.push(ungrouped)
      }
      chain = [ungrouped]
    }

    const host = chain[chain.length - 1] as GroupNode
    host.sessions.push(session)
    for (const node of chain) node.total += 1
  }

  sortNodes(roots)
  return roots
}

/**
 * The label an auto group wears: the last segment of its directory.
 *
 * The whole path is the fallback rather than the first choice because it is
 * the thing that does not fit — at a sidebar's width every row would read
 * `/Users/…/workspace/` and differ only past the truncation.
 */
export function cwdLabel(cwd: string): string {
  return cwd.replace(/\/+$/, '').split('/').pop() || cwd || 'sessions'
}

/**
 * Groups sessions by the directory they run in.
 *
 * Kept apart from `buildTree` rather than folded into it behind a flag: that
 * one reads a catalogue, nests to any depth, and invents a node for the
 * sessions no group claims. This one has no catalogue to read, one level, and
 * no leftovers — every session has a cwd. The two share the node shape and
 * nothing else.
 *
 * A node exists because a session is in that directory, so an empty one cannot
 * happen and there is nothing here to keep an empty one alive for: unlike a
 * made group, an auto group is not somewhere the user can file anything.
 *
 * Sessions arrive already sorted, and both the sessions inside a node and the
 * nodes themselves keep that order — the directory holding the session the
 * caller ranked first comes first.
 */
export function buildCwdTree(sessions: Session[]): GroupNode[] {
  const nodes = new Map<string, GroupNode>()
  for (const session of sessions) {
    const cwd = session.cwd
    let node = nodes.get(cwd)
    if (!node) {
      node = {
        key: cwd,
        name: cwdLabel(cwd),
        position: nodes.size,
        path: [cwd],
        children: [],
        sessions: [],
        total: 0,
      }
      nodes.set(cwd, node)
    }
    node.sessions.push(session)
    node.total += 1
  }
  return [...nodes.values()]
}

function sortNodes(nodes: GroupNode[]): void {
  nodes.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name))
  for (const node of nodes) sortNodes(node.children)
}

/**
 * Every group gets a header, including one holding a single child.
 *
 * A derived grouping would hide a level that splits nothing, because a
 * redundant row is noise nobody asked for. These groups are made by hand: a
 * person who built "Work › opal-app" is telling us both levels matter, and
 * silently dropping one would hide the thing they just created. The rule that
 * survives is the host header, which is skipped when there is only one host —
 * see `Sidebar`.
 */
export function headerFor(node: GroupNode): string {
  return node.name
}

/**
 * A stable colour for a group, drawn from its key.
 *
 * Twelve hues rather than the whole wheel: adjacent hues are not
 * distinguishable at 22px, so a continuous hash spends its range on differences
 * nobody can see, while twelve stops are twelve badges the eye can actually
 * tell apart. Saturation and lightness are fixed so no group gets a badge that
 * shouts.
 */
export function tintOf(key: string): string {
  let hash = 0
  for (let index = 0; index < key.length; index += 1) {
    hash = (hash * 31 + key.charCodeAt(index)) % 4096
  }
  return `hsl(${(hash % 12) * 30} 62% 62%)`
}
