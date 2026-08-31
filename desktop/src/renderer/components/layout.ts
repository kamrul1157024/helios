// How a session's panels are arranged: a flat row of groups, each with its own
// tab strip, and one of its items in front.
//
// Kept out of the components and reaching nothing, so it can be tested. Every
// function here takes a layout and returns one; a nested pane tree would be a
// change of representation behind the same calls.

export type ItemId = string

/** One tab strip and the items behind it. `size` is an fr weight, not pixels:
 *  a width measured in today's window leaves gutters in a wider one. */
export interface Group {
  id: string
  items: ItemId[]
  active: ItemId
  size: number
}

export interface Layout {
  axis: 'row' | 'column'
  groups: Group[]
  /** Which group a reveal lands in, and which one owns the keyboard. */
  focused: string
}

/** Where a drop on a group's body lands. */
export type Edge = 'before' | 'after'

export function panelItem(panel: string): ItemId {
  return `panel:${panel}`
}

export function termItem(tabId: string): ItemId {
  return `term:${tabId}`
}

/** The panel an item names, or null if it is a terminal. */
export function panelOf(item: ItemId): string | null {
  return item.startsWith('panel:') ? item.slice(6) : null
}

/** The tab an item names, or null if it is a panel. */
export function tabOf(item: ItemId): string | null {
  return item.startsWith('term:') ? item.slice(5) : null
}

/**
 * The panels every session has, in the order the strip lists them.
 *
 * `panel:terminal` is the session's own terminal — the live pane when one is
 * attached and the attach button when there is not. Shells are `term:` items
 * of their own, because a session can have any number of them.
 */
export const PANEL_ITEMS: ItemId[] = ['chat', 'terminal', 'approvals', 'git', 'files'].map(panelItem)

/**
 * One group, holding every panel, reading the transcript.
 *
 * A tab in the strip is not a mounted panel: the strip lists what the session
 * *has*, and the view mounts an item the first time it is looked at. Seeding
 * the layout with all five therefore costs nothing, and it is what lets a panel
 * be dragged somewhere before it has ever been opened.
 */
export function defaultLayout(): Layout {
  return {
    axis: 'row',
    groups: [{ id: 'g1', items: [...PANEL_ITEMS], active: panelItem('chat'), size: 1 }],
    focused: 'g1',
  }
}

let counter = 0

function newGroupId(layout: Layout): string {
  let id = ''
  do {
    counter += 1
    id = `g${counter}`
  } while (layout.groups.some((group) => group.id === id))
  return id
}

export function groupOf(layout: Layout, item: ItemId): Group | null {
  return layout.groups.find((group) => group.items.includes(item)) ?? null
}

/** Every item on screen — one per group, which is what "visible" means here. */
export function visibleItems(layout: Layout): ItemId[] {
  return layout.groups.map((group) => group.active)
}

export function isVisible(layout: Layout, item: ItemId): boolean {
  return layout.groups.some((group) => group.active === item)
}

/** Every item the layout holds, whether or not it is in front. */
export function placedItems(layout: Layout): ItemId[] {
  return layout.groups.flatMap((group) => group.items)
}

function focusedGroup(layout: Layout): Group | undefined {
  return layout.groups.find((group) => group.id === layout.focused) ?? layout.groups[0]
}

/**
 * Brings an item to the front.
 *
 * Already placed means activate it where it is and focus that group — a reveal
 * must not drag a panel out of the group the user put it in. Otherwise it joins
 * the focused group.
 */
export function reveal(layout: Layout, item: ItemId): Layout {
  const holder = groupOf(layout, item)
  if (holder) {
    if (holder.active === item && layout.focused === holder.id) return layout
    return {
      ...layout,
      focused: holder.id,
      groups: layout.groups.map((group) =>
        group.id === holder.id ? { ...group, active: item } : group,
      ),
    }
  }

  const target = focusedGroup(layout)
  if (!target) return layout
  return {
    ...layout,
    focused: target.id,
    groups: layout.groups.map((group) =>
      group.id === target.id ? { ...group, items: [...group.items, item], active: item } : group,
    ),
  }
}

/** Moves an item into a group, or reorders it within the one it is in. */
export function moveItem(layout: Layout, item: ItemId, toGroup: string, index: number): Layout {
  if (!layout.groups.some((group) => group.id === toGroup)) return layout
  const from = groupOf(layout, item)
  if (!from) return layout

  const stripped = layout.groups.map((group) =>
    group.items.includes(item) ? { ...group, items: group.items.filter((id) => id !== item) } : group,
  )
  const placed = stripped.map((group) => {
    if (group.id !== toGroup) return group
    const items = [...group.items]
    items.splice(Math.max(0, Math.min(index, items.length)), 0, item)
    return { ...group, items, active: item }
  })

  return collapse({ ...layout, focused: toGroup, groups: placed }, from.id)
}

/** Splits an item out into a group of its own, beside the one it was dropped on. */
export function splitInto(layout: Layout, item: ItemId, atGroup: string, edge: Edge): Layout {
  const at = layout.groups.findIndex((group) => group.id === atGroup)
  const host = layout.groups[at]
  if (!host) return layout
  const from = groupOf(layout, item)
  // A lone item splitting off its own group would dissolve and rebuild the
  // same group, which reads as the drag having done nothing.
  if (from && from.id === atGroup && from.items.length === 1) return layout

  const id = newGroupId(layout)
  const half = host.size / 2
  const stripped = layout.groups.map((group) =>
    group.items.includes(item)
      ? { ...group, items: group.items.filter((one) => one !== item), size: group.id === atGroup ? half : group.size }
      : group.id === atGroup
        ? { ...group, size: half }
        : group,
  )

  const groups = [...stripped]
  groups.splice(edge === 'before' ? at : at + 1, 0, { id, items: [item], active: item, size: half })

  return collapse({ ...layout, focused: id, groups }, from?.id ?? '')
}

/**
 * Drops an item wherever it sits.
 *
 * The group it leaves keeps a neighbour in front rather than nothing: closing
 * the middle of three shells is not a request to leave the shells, and the one
 * before it is usually the one it was opened beside.
 */
export function removeItem(layout: Layout, item: ItemId): Layout {
  const holder = groupOf(layout, item)
  if (!holder) return layout

  const at = holder.items.indexOf(item)
  const rest = holder.items.filter((one) => one !== item)
  const next = holder.active === item ? (rest[at - 1] ?? rest[0] ?? '') : holder.active

  const groups = layout.groups.map((group) =>
    group.id === holder.id ? { ...group, items: rest, active: next } : group,
  )
  return collapse({ ...layout, groups }, holder.id)
}

/**
 * Drops a group that has been emptied and hands its weight to a neighbour.
 *
 * The last group is never dropped — there would be nothing left to draw — so it
 * falls back to the transcript, which is where a session starts.
 */
function collapse(layout: Layout, emptied: string): Layout {
  const at = layout.groups.findIndex((group) => group.id === emptied)
  const gone = layout.groups[at]
  if (!gone || gone.items.length > 0) return normalise(layout)

  const only = layout.groups[0]
  if (layout.groups.length === 1 && only) {
    // Nothing left to draw. The transcript is where a session starts, so it is
    // what an emptied last group falls back to.
    const chat = panelItem('chat')
    return normalise({ ...layout, groups: [{ ...only, items: [chat], active: chat }] })
  }

  const heir = layout.groups[at - 1] ?? layout.groups[at + 1]
  if (!heir) return normalise(layout)
  const groups = layout.groups
    .filter((group) => group.id !== emptied)
    .map((group) => (group.id === heir.id ? { ...group, size: group.size + gone.size } : group))

  return normalise({ ...layout, groups })
}

/** Keeps `focused` and every `active` pointing at something that exists. */
function normalise(layout: Layout): Layout {
  const groups = layout.groups.map((group) =>
    group.items.includes(group.active) ? group : { ...group, active: group.items[0] ?? group.active },
  )
  const focused = groups.some((group) => group.id === layout.focused)
    ? layout.focused
    : (groups[0]?.id ?? layout.focused)
  return { ...layout, groups, focused }
}

/** The smallest share of the row a group may be dragged down to. */
const MIN_SIZE = 0.15

/**
 * Moves weight across one sash. `delta` is the fraction of the whole row the
 * pointer travelled, so a drag reads the same at any window width.
 */
export function resize(layout: Layout, sash: number, delta: number): Layout {
  const left = layout.groups[sash]
  const right = layout.groups[sash + 1]
  if (!left || !right) return layout

  const total = left.size + right.size
  // Rounded because these are serialised: a drag that accumulated
  // 0.30000000000000004 writes that to disk and reads it back for ever.
  const size = round(Math.min(Math.max(left.size + delta * totalSize(layout), MIN_SIZE), total - MIN_SIZE))
  if (size === left.size) return layout

  return {
    ...layout,
    groups: layout.groups.map((group) =>
      group.id === left.id
        ? { ...group, size }
        : group.id === right.id
          ? { ...group, size: round(total - size) }
          : group,
    ),
  }
}

function round(size: number): number {
  return Math.round(size * 1e4) / 1e4
}

export function totalSize(layout: Layout): number {
  return layout.groups.reduce((sum, group) => sum + group.size, 0)
}

/** Even weights again, for a double-click on a sash. */
export function evenSizes(layout: Layout): Layout {
  return { ...layout, groups: layout.groups.map((group) => ({ ...group, size: 1 })) }
}

/**
 * Places terminals the layout has not seen yet.
 *
 * Append-only, deliberately. A saved layout is read the moment a session is
 * selected, but its shells are re-attached over an await — pruning items with
 * no live tab would delete the arrangement in that window, before it could be
 * shown. Terminals are removed when they close, by `removeItem`.
 */
export function reconcile(layout: Layout, liveTabs: string[]): Layout {
  const placed = new Set(placedItems(layout))
  const missing = liveTabs.map(termItem).filter((item) => !placed.has(item))
  if (missing.length === 0) return layout

  const target = focusedGroup(layout)
  if (!target) return layout
  return {
    ...layout,
    groups: layout.groups.map((group) =>
      group.id === target.id ? { ...group, items: [...group.items, ...missing] } : group,
    ),
  }
}

/**
 * Which held panels may be unmounted, given when each was last in front.
 *
 * An item visible in any group is in use, even when the focus is elsewhere —
 * that is the whole point of a split, and sweeping it would take a panel out
 * from under someone reading it.
 */
export function sweep(layout: Layout, lastSeen: Record<ItemId, number>, now: number, ttl: number): ItemId[] {
  return Object.entries(lastSeen)
    .filter(([item, seen]) => !isVisible(layout, item) && now - seen > ttl)
    .map(([item]) => item)
}

/** A stored layout, or the default if it is anything else. */
export function parseLayout(raw: unknown): Layout {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return defaultLayout()
  const value = raw as Partial<Layout>
  if (!Array.isArray(value.groups) || value.groups.length === 0) return defaultLayout()

  const groups: Group[] = []
  for (const entry of value.groups) {
    if (!entry || typeof entry !== 'object') return defaultLayout()
    const { id, items, active, size } = entry as Partial<Group>
    if (typeof id !== 'string' || !id) return defaultLayout()
    if (!Array.isArray(items) || items.some((item) => typeof item !== 'string')) return defaultLayout()
    const head = items[0]
    if (head === undefined) continue
    groups.push({
      id,
      items,
      active: typeof active === 'string' && items.includes(active) ? active : head,
      size: typeof size === 'number' && Number.isFinite(size) && size > 0 ? size : 1,
    })
  }
  const first = groups[0]
  if (!first) return defaultLayout()

  // Ids seen here must not be handed out again, or a later split would collide
  // with a group restored from disk.
  for (const group of groups) {
    const n = Number(group.id.slice(1))
    if (group.id.startsWith('g') && Number.isInteger(n) && n > counter) counter = n
  }

  const axis = value.axis === 'column' ? 'column' : 'row'
  const focused = groups.some((group) => group.id === value.focused) ? (value.focused as string) : first.id
  return { axis, groups, focused }
}
