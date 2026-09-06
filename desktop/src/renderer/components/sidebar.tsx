import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { bridge } from '../bridge.ts'
import { providersQuery } from '../queries.ts'
import {
  useHostGroups,
  useHostJobSessions,
  useHostNotifications,
  useHostSessions,
  useHostSortModes,
} from '../host-data.ts'
import { store, useStore } from '../store.ts'
import {
  BUSY_STATUSES,
  canResume,
  hasTerminal,
  isTerminated,
  needsRecovery,
  sessionLabel,
  shortMode,
  shortModel,
  statusLabel,
  timeAgo,
  type HostRecord,
  type Session,
  type HostStats,
} from '../../shared/models.ts'
import { Chevron, Console, Cpu, Memory, Pencil, Plus, Search, Sort } from './icons.tsx'
import {
  buildCwdTree,
  buildTree,
  byRank,
  depthOf,
  rankOf,
  type GroupNode,
} from './grouping.ts'
import { GroupPicker } from './group-picker.tsx'
import { ScheduleHost } from './schedules.tsx'
import { SelectionMenu, type MenuAction } from './selection-menu.tsx'
import { sessionActions } from './session-menu.ts'
import { SECTIONS } from './settings.tsx'

/** What the sidebar may be dragged to. Narrower hides titles; wider is a
 *  session list taking half the window from the session itself. */
const MIN_SIDEBAR = 260
const MAX_SIDEBAR = 560
const SIDEBAR_KEY = 'helios.sidebarWidth'

function readSidebarWidth(): number | null {
  try {
    const saved = Number(localStorage.getItem(SIDEBAR_KEY))
    return saved > 0 ? Math.min(Math.max(saved, MIN_SIDEBAR), MAX_SIDEBAR) : null
  } catch {
    return null
  }
}

function writeSidebarWidth(width: number | null): void {
  try {
    if (width === null) localStorage.removeItem(SIDEBAR_KEY)
    else localStorage.setItem(SIDEBAR_KEY, String(width))
  } catch {
    // A full or unavailable store costs the width, not the sidebar.
  }
}

interface Row {
  session: Session
  pending: number
}

interface Group {
  host: HostRecord
  /** Every session shown, in render order — the flat list, and what a reorder
   *  posts. Used directly when grouping is off. */
  rows: Row[]
  /** The same sessions nested, when grouping is on. Empty when it is off. */
  nodes: GroupNode[]
  /** Approvals waiting on each session, by session id. */
  pending: Map<string, number>
  /** Sessions shown, across every group — what the host head counts. */
  count: number
  /** Terminated sessions withheld from rows, so the host can offer them. */
  hidden: number
  /** The host has not answered yet, which is not the same as having nothing. */
  loading: boolean
}

/** A session being dragged, and the group it may be dropped back into. Held as
 *  the whole path rather than one key: a drop is legal only inside the node the
 *  drag started in, and at three levels deep the innermost key is not enough to
 *  say which node that was. */
interface Drag {
  hostId: string
  path: string
  sessionId: string
}

/** A group header being dragged, to rearrange the tree itself. */
interface GroupDrag {
  hostId: string
  key: string
}

/**
 * Where a dragged header would land.
 *
 * One gesture has to mean two things, so the pointer's height within the header
 * decides: near an edge it goes beside the target, in the middle it goes inside
 * it. Without the indicator that renders this, the difference would be a guess
 * the user makes after the fact.
 */
type DropMode = 'before' | 'after' | 'inside'

interface GroupDrop {
  key: string
  mode: DropMode
}

/** A right-click on a session row, and where the pointer was. */
interface RowMenu {
  kind: 'session'
  hostId: string
  session: Session
  x: number
  y: number
}

/** A right-click on a group header. The name is copied rather than the node
 *  held, so a refresh arriving under an open menu cannot leave it pointing at a
 *  tree that has been rebuilt. */
interface HeadMenu {
  kind: 'group'
  hostId: string
  key: string
  name: string
  x: number
  y: number
}

/**
 * What a drag is carrying, said as a MIME type rather than as a prefix on the
 * payload.
 *
 * A dragover may read `dataTransfer.types` but not the data behind them, and a
 * dragover is where a target decides whether to accept — so the kind of thing
 * being dragged has to be in the type. A group header takes both a group and a
 * session and means something different by each, so it has to know which before
 * the drop.
 *
 * The host is part of the type for the same reason: a header on another daemon
 * has to refuse a session it can never hold, and the type is all it can see.
 * `types` is lowercased by the browser, so the id is lowercased going in.
 */
const GROUP_DRAG = 'application/x-helios-group'
const SESSION_DRAG = 'application/x-helios-session'

function sessionDrag(hostId: string): string {
  return `${SESSION_DRAG}+${hostId.toLowerCase()}`
}

/** Edges claim a quarter each; the middle half nests. */
function dropModeFor(event: React.DragEvent, el: HTMLElement): DropMode {
  const rect = el.getBoundingClientRect()
  const offset = (event.clientY - rect.top) / rect.height
  if (offset < 0.25) return 'before'
  if (offset > 0.75) return 'after'
  return 'inside'
}

export function Sidebar({
  onNewSession,
}: {
  onNewSession: (seed?: { hostId: string; cwd: string; group?: string }) => void
}): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const hostStatus = useStore((s) => s.hostStatus)
  const { sessions, stats, pending: awaiting } = useHostSessions()
  const notifications = useHostNotifications()
  const jobSessions = useHostJobSessions()
  const autoRunsOpen = useStore((s) => s.autoRunsOpen)
  const selection = useStore((s) => s.selection)
  const renamingSession = useStore((s) => s.renamingSession)
  const mode = useStore((s) => s.sidebarMode)
  const settingsSection = useStore((s) => s.settingsSection)
  // Its own search, because the two lists hold different things and a query
  // typed against one is meaningless against the other.
  const scheduleQuery = useStore((s) => s.scheduleQuery)
  const query = useStore((s) => s.query)
  const sortMode = useHostSortModes()
  const groupMode = useStore((s) => s.grouping)
  // Split apart once, here: almost everything below asks "is the list split at
  // all" or "is this a tree the user can edit", and neither reads well as a
  // comparison against a string repeated thirty times.
  const grouping = groupMode !== 'off'
  const derived = groupMode === 'auto'
  const groupOrder = useStore((s) => s.groupOrder)
  const dirOrder = useStore((s) => s.dirOrder)
  const { groups: groupsByHost, unsupported: groupsUnsupported } = useHostGroups()
  // The card being dragged, so the row under the pointer can show where it
  // would land. Held per host: a drag never crosses from one daemon to another.
  const [dragging, setDragging] = useState<Drag | null>(null)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  // Folded groups, keyed by host and the group's whole path: the same group can
  // appear at two depths, and folding it in one place should not fold the
  // other.
  const [folded, setFolded] = useState<Record<string, boolean>>({})
  // A group header on the move. Separate from a session drag: they reorder
  // different things and a drop on one is never a drop on the other.
  const [groupDrag, setGroupDrag] = useState<GroupDrag | null>(null)
  const [groupDrop, setGroupDrop] = useState<GroupDrop | null>(null)
  // Per host, not global: a machine kept for finished work and one being
  // worked in want opposite answers, and the setting is one click away.
  const [showTerminated, setShowTerminated] = useState<Record<string, boolean>>({})
  // What a right-click named, and where the pointer was. Held for the whole
  // list rather than per row, so a second right-click moves the one menu
  // instead of opening another beside it — and one piece of state, so a header
  // menu and a row menu can never be open at once.
  const [menu, setMenu] = useState<RowMenu | HeadMenu | null>(null)
  // Only once a row menu is open, and never for a header one. The list is
  // shared with the new-session dialog and never goes stale, so this is usually
  // a cache hit — but a sidebar nobody has right-clicked should not pay for one
  // request per host on mount.
  const { data: providers } = useQuery({
    ...providersQuery(menu?.kind === 'session' ? menu.hostId : ''),
    enabled: menu?.kind === 'session',
  })
  // Which group is having a child named, keyed by host and group. Empty string
  // is the root of that host, so one piece of state covers both.
  const [creatingIn, setCreatingIn] = useState<string | null>(null)
  // Which group header is being renamed in place, keyed the same way.
  const [renaming, setRenaming] = useState<string | null>(null)
  const [picker, setPicker] = useState(false)
  const aside = useRef<HTMLElement | null>(null)

  // The width is a CSS variable rather than state: the value is read by the
  // stylesheet, nothing in React branches on it, and a drag that re-rendered
  // the whole list on every pointer move would stutter against 150 sessions.
  useEffect(() => {
    const saved = readSidebarWidth()
    if (saved !== null) document.documentElement.style.setProperty('--sidebar', `${saved}px`)
  }, [])

  const resize = (event: React.PointerEvent<HTMLDivElement>): void => {
    event.preventDefault()
    const fromX = event.clientX
    const fromWidth = aside.current?.getBoundingClientRect().width ?? MIN_SIDEBAR
    let latest = fromWidth
    const move = (moved: PointerEvent): void => {
      latest = Math.min(Math.max(fromWidth + (moved.clientX - fromX), MIN_SIDEBAR), MAX_SIDEBAR)
      document.documentElement.style.setProperty('--sidebar', `${Math.round(latest)}px`)
    }
    const done = (): void => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', done)
      writeSidebarWidth(Math.round(latest))
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', done)
  }

  /**
   * A dropped header either nests inside the target or lands beside it.
   *
   * Beside means "among the target's siblings", so a group coming from another
   * parent is re-parented first and then ordered — two writes, because position
   * is only meaningful within one parent and the daemon has no way to express
   * both at once.
   */
  const moveOrReorder = async (
    hostId: string,
    dragged: string,
    target: string,
    mode: DropMode,
  ): Promise<void> => {
    const all = groupsByHost[hostId] ?? []
    const targetGroup = all.find((g) => g.key === target)
    const draggedGroup = all.find((g) => g.key === dragged)
    if (!targetGroup || !draggedGroup) return

    // Refused here as well as in the daemon, so the list does not flicker
    // through an arrangement the write is about to reject.
    const ancestors = new Set<string>()
    for (let at = targetGroup; at; at = all.find((g) => g.key === at.parent) as typeof at) {
      if (ancestors.has(at.key)) break
      ancestors.add(at.key)
      if (!at.parent) break
    }
    if (ancestors.has(dragged)) return

    if (mode === 'inside') {
      await store.moveGroup(hostId, dragged, target)
      return
    }

    const parent = targetGroup.parent ?? ''
    if ((draggedGroup.parent ?? '') !== parent) await store.moveGroup(hostId, dragged, parent)

    const siblings = store
      .groupsOf(hostId)
      .filter((g) => (g.parent ?? '') === parent)
      .map((g) => g.key)
      .filter((k) => k !== dragged)
    const at = siblings.indexOf(target)
    if (at === -1) return
    siblings.splice(mode === 'before' ? at : at + 1, 0, dragged)
    await store.reorderGroups(hostId, parent, siblings)
  }

  const grouped = useMemo<Group[]>(() => {
    const needle = query.trim().toLowerCase()
    return hosts.map((host) => {
      const pendingByCwd = new Map<string, number>()
      for (const notif of notifications[host.id] ?? []) {
        pendingByCwd.set(notif.source_session, (pendingByCwd.get(notif.source_session) ?? 0) + 1)
      }

      const visible = (sessions[host.id] ?? []).filter((session) => {
        if (!needle) return true
        return `${session.title ?? ''} ${session.project} ${session.cwd} ${session.last_user_message ?? ''}`
          .toLowerCase()
          .includes(needle)
      })

      // A terminated session the user searched for is a session they asked to
      // see, so the filter yields to an explicit query.
      const hideTerminated = !showTerminated[host.id] && !needle
      const ordered = visible
        .filter((session) => !hideTerminated || !isTerminated(session))
        .map((session) => ({ session, pending: pendingByCwd.get(session.session_id) ?? 0 }))
        .sort(sortMode[host.id] === 'manual' ? byHand : compareRows)

      // Sorted first, then nested. The rank vector decides where a session
      // hangs; the comparators above decide the order it hangs in. Keeping them
      // apart is what lets a group move without re-ranking anything inside it.
      //
      // Only the manual tree ranks: an auto group's place comes from where its
      // first session already landed, so re-sorting by a rank every session
      // shares would be a no-op at best.
      const tree = groupMode === 'manual'
      const depth = tree ? depthOf(ordered.map((row) => row.session)) : 0
      const sorted = tree
        ? [...ordered].sort((a, b) =>
            byRank(rankOf(a.session, depth), rankOf(b.session, depth)),
          )
        : ordered
      const nodes = tree
        ? buildTree(sorted.map((row) => row.session), groupsByHost[host.id] ?? [])
        : derived
          ? buildCwdTree(sorted.map((row) => row.session), groupOrder, dirOrder)
          : []

      const hidden = hideTerminated ? visible.filter(isTerminated).length : 0
      // Still waiting is not the same as having nothing. Without the
      // distinction a daemon that is slow to answer looks like a daemon with
      // nothing on it, and the sidebar draws the wrong empty state.
      const loading = awaiting[host.id] ?? true
      const pending = new Map(sorted.map((row) => [row.session.session_id, row.pending]))
      return { host, rows: sorted, nodes, pending, count: ordered.length, hidden, loading }
    })
  }, [
    hosts,
    awaiting,
    sessions,
    notifications,
    query,
    showTerminated,
    sortMode,
    groupMode,
    derived,
    groupOrder,
    dirOrder,
    groupsByHost,
  ])

  // One host answering "manual" is enough to show the switch as on: the click
  // writes the other way to every host, which settles any disagreement.
  const manual = hosts.some((host) => sortMode[host.id] === 'manual')

  return (
    <aside className="sidebar" ref={aside}>
      {/* Which mode this is comes from the rail, so the list starts at its own
          search rather than at a switch it is not part of. */}
      {mode === 'settings' && (
        <nav className="settings-nav">
          {SECTIONS.map((entry) => (
            <button
              key={entry.id}
              className={settingsSection === entry.id ? 'active' : ''}
              onClick={() => store.setSettingsSection(entry.id)}
            >
              {entry.label}
            </button>
          ))}
        </nav>
      )}

      {mode === 'schedules' && (
        <>
          <header className="sidebar-head">
            <div className="search-field">
              <Search className="search-icon" />
              <input
                className="search"
                placeholder="Search schedules"
                value={scheduleQuery}
                onChange={(event) => store.setScheduleQuery(event.target.value)}
              />
              {scheduleQuery !== '' && (
                <button
                  className="search-clear"
                  aria-label="Clear the search"
                  onClick={() => store.setScheduleQuery('')}
                >
                  ×
                </button>
              )}
            </div>
            <button
              className="tool primary"
              aria-label="New schedule"
              title="New schedule"
              onClick={() => store.newSchedule(selection?.hostId ?? hosts[0]?.id ?? '')}
            >
              <Plus />
            </button>
          </header>

          {/* A host with nothing scheduled on it is not worth a heading and a
              paragraph each — one machine's empty list is not news when
              another has six. The empty state is shown once, by the last host,
              only when nobody has any. */}
          <div className="sidebar-list">
            {hosts.map((host, index) => (
              <ScheduleHost
                key={host.id}
                hostId={host.id}
                name={host.name}
                status={hostStatus[host.id]?.state ?? 'connecting'}
                showName={hosts.length > 1}
                query={scheduleQuery}
                quiet={index < hosts.length - 1}
              />
            ))}
          </div>
        </>
      )}

      {mode === 'sessions' && (
      <header className="sidebar-head">
        <div className="search-field">
          <Search className="search-icon" />
          <input
            className="search"
            placeholder="Search sessions"
            value={query}
            onChange={(event) => store.setQuery(event.target.value)}
          />
          {/* Only once there is something to clear: an always-present × in a
              field the user has not typed in reads as a control they have
              missed the purpose of. */}
          {query !== '' && (
            <button className="search-clear" aria-label="Clear the search" onClick={() => store.setQuery('')}>
              ×
            </button>
          )}
        </div>
        <div className="picker-anchor">
          <button
            className={grouping || manual ? 'tool sort-toggle on' : 'tool sort-toggle'}
            aria-label="Arrange the list"
            aria-expanded={picker}
            title={'Arrange — grouping, and what each level sorts by.'}
            onClick={() => setPicker((open) => !open)}
          >
            <Sort />
          </button>
          {picker && (
            <GroupPicker
              // Grouping is one setting for the app, but whether a daemon can
              // hold groups at all is per host: the one being looked at, or the
              // first, which is the one the note below would be about.
              hostId={selection?.hostId ?? hosts[0]?.id ?? null}
              hostName={
                hosts.find((h) => h.id === (selection?.hostId ?? hosts[0]?.id))?.name ?? ''
              }
              manual={manual}
              onClose={() => setPicker(false)}
            />
          )}
        </div>
        <button
          className="tool primary"
          aria-label="New session"
          title="New session (⌘N)"
          onClick={() => onNewSession()}
        >
          <Plus />
        </button>
      </header>
      )}

      <div className="sidebar-list" hidden={mode !== 'sessions'}>
        {grouped.map(({ host, rows, nodes, pending, count, hidden, loading }) => {
          const status = hostStatus[host.id]?.state ?? 'connecting'
          const isCollapsed = collapsed[host.id] ?? false
          const revealed = showTerminated[host.id] ?? false
          // The whole host in display order, which is what a reorder posts.
          const order = rows.map((row) => row.session.session_id)
          // One machine needs no header saying which machine: the rule that
          // hides a level splitting nothing survives here, where it started.
          const showHost = hosts.length > 1
          const draggable = sortMode[host.id] === 'manual'
          const needle = query.trim().toLowerCase()
          const automated = (jobSessions[host.id] ?? []).filter((session) => {
            if (!needle) return true
            return `${session.title ?? ''} ${session.project} ${session.cwd} ${session.last_user_message ?? ''}`
              .toLowerCase()
              .includes(needle)
          })
          const autoOpen = autoRunsOpen[host.id] ?? false

          const renderRow = (session: Session, path: string): JSX.Element => (
            <SessionRow
              key={session.session_id}
              hostId={host.id}
              session={session}
              pending={pending.get(session.session_id) ?? 0}
              selected={selection?.hostId === host.id && selection.sessionId === session.session_id}
              draggable={draggable}
              dragging={dragging?.sessionId === session.session_id}
              // Only inside the node it started in: the daemon holds one flat
              // order per host, so a card dropped across a divide would drag
              // its whole group with it.
              accepts={dragging?.hostId === host.id && dragging.path === path}
              onDragStart={() => setDragging({ hostId: host.id, path, sessionId: session.session_id })}
              onDragEnd={() => {
                setDragging(null)
                // A session dropped nowhere leaves a header lit otherwise: the
                // header's own dragend never fires, because it is not the source.
                setGroupDrop(null)
              }}
              editing={
                renamingSession?.hostId === host.id &&
                renamingSession.sessionId === session.session_id
              }
              onContextMenu={(x, y) => setMenu({ kind: 'session', hostId: host.id, session, x, y })}
              onDropBefore={(draggedId) => {
                // The id off the drag itself, not React state: the drop can
                // arrive in the same tick as the drag start, before a setState
                // has committed, and then nothing moves.
                const ids = [...order]
                const from = ids.indexOf(draggedId)
                const to = ids.indexOf(session.session_id)
                setDragging(null)
                if (from === -1 || to === -1 || from === to) return
                ids.splice(to, 0, ids.splice(from, 1)[0] as string)
                void store.reorderSessions(host.id, ids)
              }}
            />
          )

          const renderNode = (node: GroupNode): JSX.Element => {
            const path = node.path.join('/')
            const foldKey = `${host.id}:${path}`
            const isFolded = folded[foldKey] ?? false
            // Ungrouped is synthetic. It has no key to reorder and no name to
            // rename, so it is a header and nothing else. An auto group's key
            // is its directory, which is a name but not one anybody stored.
            const named = node.key !== ''
            // Whether there is a group behind the header to act on. An auto
            // group is a reading of the sessions, not a thing: renaming or
            // deleting one would have to change where the sessions are running,
            // and nesting it would claim a level the directory does not have.
            const real = named && !derived
            // `real` again, because Ungrouped's key is "" — the same string the
            // host's own root uses, and a shared key would put two headers into
            // the field at once.
            const editing = real && renaming === `${host.id}:${node.key}`
            return (
              <div className="group" key={path || 'ungrouped'}>
                <div
                  className={[
                    'group-head',
                    isFolded ? 'folded' : '',
                    groupDrop?.key === node.key ? `drop-${groupDrop.mode}` : '',
                  ]
                    .filter(Boolean)
                    .join(' ')}
                  // Rename and delete are the group's own, so Ungrouped — which
                  // is neither named nor stored — offers no menu at all.
                  onContextMenu={(event) => {
                    if (!real) return
                    event.preventDefault()
                    setMenu({
                      kind: 'group',
                      hostId: host.id,
                      key: node.key,
                      name: node.name,
                      x: event.clientX,
                      y: event.clientY,
                    })
                  }}
                >
                  {editing ? (
                    <InlineNameField
                      initial={node.name}
                      onCancel={() => setRenaming(null)}
                      onCommit={(name) => {
                        setRenaming(null)
                        if (name !== node.name) void store.renameGroup(host.id, node.key, name)
                      }}
                    />
                  ) : (
                    <>
                      <button
                        className="group-title"
                        aria-expanded={!isFolded}
                        draggable={real || derived}
                        onDragStart={(event) => {
                          event.stopPropagation()
                          event.dataTransfer.effectAllowed = 'move'
                          event.dataTransfer.setData('text/plain', node.key)
                          event.dataTransfer.setData(GROUP_DRAG, node.key)
                          setGroupDrag({ hostId: host.id, key: node.key })
                        }}
                        onDragEnd={() => {
                          setGroupDrag(null)
                          setGroupDrop(null)
                        }}
                        // Which gesture this is comes off the transfer's types, not
                        // out of state: the two drags mean different things here,
                        // and a drop can land in the same tick as the drag start,
                        // before either setState has committed.
                        onDragOver={(event) => {
                          const kinds = event.dataTransfer.types
                          // Every header takes a session, Ungrouped included —
                          // dropping there is how a session leaves its group. It
                          // always nests: a session has no position among groups.
                          //
                          // Except an auto header, which is a directory: a
                          // session belongs to the one it is running in, and a
                          // drop cannot move it there.
                          if (!derived && kinds.includes(sessionDrag(host.id))) {
                            event.preventDefault()
                            event.dataTransfer.dropEffect = 'move'
                            setGroupDrop({ key: node.key, mode: 'inside' })
                            return
                          }
                          // A directory header accepts a drag whatever the
                          // current order is: dragging one is itself the
                          // request to arrange them by hand, and requiring the
                          // mode first would mean choosing it before there was
                          // any reason to.
                          const arrangeable = real || derived
                          if (!arrangeable || !kinds.includes(GROUP_DRAG)) return
                          if (groupDrag?.hostId !== host.id || groupDrag.key === node.key) return
                          event.preventDefault()
                          event.dataTransfer.dropEffect = 'move'
                          const over = dropModeFor(event, event.currentTarget)
                          setGroupDrop({
                            key: node.key,
                            mode: derived && over === 'inside' ? 'after' : over,
                          })
                        }}
                        onDragLeave={() => setGroupDrop((d) => (d?.key === node.key ? null : d))}
                        // The payload and the mode both come off the event rather
                        // than out of state, for the reason above.
                        onDrop={(event) => {
                          const moved = derived ? '' : event.dataTransfer.getData(sessionDrag(host.id))
                          if (moved) {
                            event.preventDefault()
                            setDragging(null)
                            setGroupDrop(null)
                            // node.key is "" on Ungrouped, which is what unfiles it.
                            void store.setSessionGroup(host.id, moved, node.key)
                            return
                          }
                          if (!real && !derived) return
                          event.preventDefault()
                          const dragged = event.dataTransfer.getData(GROUP_DRAG)
                          const raw = dropModeFor(event, event.currentTarget)
                          const mode = derived && raw === 'inside' ? 'after' : raw
                          setGroupDrag(null)
                          setGroupDrop(null)
                          if (!dragged || dragged === node.key) return
                          if (derived) {
                            // Directories are one flat level, so there is
                            // nothing to nest into: every drop is a reorder,
                            // and the half of the header you hit only decides
                            // which side of it you land on.
                            const paths = nodes.map((n) => n.key).filter((k) => k !== dragged)
                            const at = paths.indexOf(node.key)
                            if (at === -1) return
                            paths.splice(mode === 'before' ? at : at + 1, 0, dragged)
                            store.reorderDirectories(paths)
                            return
                          }
                          void moveOrReorder(host.id, dragged, node.key, mode)
                        }}
                        onClick={() => setFolded((f) => ({ ...f, [foldKey]: !isFolded }))}
                      >
                        <span className="group-name">{node.name}</span>
                        <span className="group-count">{node.total}</span>
                        <Chevron className="chevron group-chevron" open={!isFolded} />
                      </button>
                      {/* Not `real`: an auto group cannot be renamed or nested,
                          but "another session here" is the one thing a
                          directory does answer, and it answers it exactly —
                          the group's key is the directory. */}
                      {named && (
                        <button
                          className="group-add"
                          aria-label={`New session in ${node.name}`}
                          title={`New session in ${node.name}`}
                          onClick={(event) => {
                            event.stopPropagation()
                            if (derived) {
                              onNewSession({ hostId: host.id, cwd: node.key })
                              return
                            }
                            // The directory is a guess — the group's most recent
                            // session's — but the group is not: the dialog files the
                            // new session here whatever directory it ends up in.
                            const recent = node.sessions[0] ?? node.children[0]?.sessions[0]
                            onNewSession({ hostId: host.id, cwd: recent?.cwd ?? '', group: node.key })
                          }}
                        >
                          <Console />
                        </button>
                      )}
                      {real && !groupsUnsupported[host.id] && (
                        <button
                          className="group-add"
                          aria-label={`New group in ${node.name}`}
                          title={`New group in ${node.name}`}
                          onClick={(event) => {
                            event.stopPropagation()
                            setFolded((f) => ({ ...f, [foldKey]: false }))
                            setCreatingIn(`${host.id}:${node.key}`)
                          }}
                        >
                          <Plus />
                        </button>
                      )}
                    </>
                  )}
                </div>

                {!isFolded && (
                  <div className="group-rows">
                    {/* `real`, because Ungrouped's key is "" — the same string
                        the host's own "+ Group" sets. Without this the one
                        click mounts two fields, the second steals focus from
                        the first, and the first's blur cancels both. */}
                    {real && creatingIn === `${host.id}:${node.key}` && (
                      <InlineNameField
                        onCancel={() => setCreatingIn(null)}
                        onCommit={(name) => {
                          setCreatingIn(null)
                          void store.createGroup(host.id, name, node.key)
                        }}
                      />
                    )}
                    {node.children.map((child) => renderNode(child))}
                    {node.sessions.map((session) => renderRow(session, path))}
                  </div>
                )}
              </div>
            )
          }

          return (
            <section key={host.id} className="host-group">
              <div className="host-head">
                {showHost && (
                  <button
                    className="host-title"
                    aria-expanded={!isCollapsed}
                    onClick={() => setCollapsed((c) => ({ ...c, [host.id]: !isCollapsed }))}
                  >
                    <span className={`host-dot ${status}`} title={hostStatus[host.id]?.error ?? status} />
                    <span className="host-name">{host.name}</span>
                    {/* No count until there is one: a 0 that turns into 12 reads
                        as an answer, and it was not one. */}
                    {!loading && (
                      <span className="host-count">
                        {count} {count === 1 ? 'session' : 'sessions'}
                      </span>
                    )}
                    <Chevron className="chevron" open={!isCollapsed} />
                  </button>
                )}

                {/* One line under the host: what its terminals cost on the
                    left, and the sessions it is not showing on the right. Both
                    are facts about this machine, and neither is worth a row of
                    its own. */}
                {!isCollapsed && (
                  <div className="host-meta">
                    <HostMeter stats={stats[host.id]} />
                    {groupMode === 'manual' && !groupsUnsupported[host.id] && (
                      <button
                        className="link"
                        onClick={() => setCreatingIn(`${host.id}:`)}
                        title="A group at the top level, with no parent"
                      >
                        + Group
                      </button>
                    )}
                    {(hidden > 0 || revealed) && (
                      <button
                        className="link show-terminated"
                        onClick={() =>
                          setShowTerminated((current) => ({ ...current, [host.id]: !current[host.id] }))
                        }
                      >
                        {revealed ? 'Hide terminated' : `Show ${hidden} terminated`}
                      </button>
                    )}
                  </div>
                )}
              </div>

              {!isCollapsed && groupMode === 'manual' && creatingIn === `${host.id}:` && (
                <InlineNameField
                  onCancel={() => setCreatingIn(null)}
                  onCommit={(name) => {
                    setCreatingIn(null)
                    void store.createGroup(host.id, name, '')
                  }}
                />
              )}

              {!isCollapsed &&
                (grouping
                  ? nodes.map((node) => renderNode(node))
                  : rows.map((row) => renderRow(row.session, '')))}

              {/* What a schedule started, under what the user started. Folded
                  until asked for, because a schedule that fires hourly would
                  otherwise bury the sessions the sidebar is for. Opening a run
                  from the schedules tab unfolds it, so the row it selects is on
                  screen rather than behind a header. */}
              {!isCollapsed && automated.length > 0 && (
                <div className="auto-runs">
                  <button
                    className={autoOpen ? 'auto-head open' : 'auto-head'}
                    aria-expanded={autoOpen}
                    onClick={() => store.toggleAutoRuns(host.id)}
                  >
                    <Chevron className="chevron" open={autoOpen} />
                    <span className="auto-name">Automated runs</span>
                    <span className="host-count">{automated.length}</span>
                  </button>
                  {autoOpen &&
                    automated.map((session) => (
                      <SessionRow
                        key={session.session_id}
                        hostId={host.id}
                        session={session}
                        pending={0}
                        selected={
                          selection?.hostId === host.id &&
                          selection.sessionId === session.session_id
                        }
                        // A run has no place in the host's hand-sorted order:
                        // that order is one list, and this is not in it.
                        draggable={false}
                        dragging={false}
                        accepts={false}
                        editing={
                          renamingSession?.hostId === host.id &&
                          renamingSession.sessionId === session.session_id
                        }
                        onDragStart={() => {}}
                        onDragEnd={() => {}}
                        onContextMenu={(x, y) =>
                          setMenu({ kind: 'session', hostId: host.id, session, x, y })
                        }
                        onDropBefore={() => {}}
                      />
                    ))}
                </div>
              )}

              {/* Skeletons rather than a spinner: the list is about to be a
                  list, and showing its shape keeps the sidebar from resizing
                  under the cursor when the rows arrive.

                  Built from the row's own elements rather than a stack of bars,
                  so it is the height of a session row by construction and stays
                  that way when the row changes. */}
              {!isCollapsed &&
                loading &&
                [0, 1, 2].map((index) => (
                  <div key={index} className="session-row skeleton" aria-hidden="true">
                    <div className="row-main">
                      <span className="row-title">
                        <span className="skeleton-line" />
                      </span>
                      <span className="skeleton-line time" />
                    </div>
                    <div className="row-sub">
                      <span className="skeleton-line" />
                    </div>
                  </div>
                ))}

              {!isCollapsed && !loading && count === 0 && hidden === 0 && automated.length === 0 && (
                <p className="empty-note">{query ? 'Nothing matches' : 'No sessions'}</p>
              )}
            </section>
          )
        })}

        {hosts.length === 0 && (
          <div className="empty">
            <p>No daemon connected.</p>
            <p className="muted">
              Start one with <code>helios daemon</code>, or pair a remote machine.
            </p>
            <button onClick={() => store.openSettings('hosts')}>Add host</button>
          </div>
        )}
      </div>

      {/* Two buttons rather than the ⋯ menu they were behind: Settings left for
          the rail, and a menu holding one item is a click in front of it. */}
      <footer className="sidebar-foot">
        {/* Not in the settings mode, where it would sit an inch under the Hosts
            pane it opens. */}
        {mode !== 'settings' && (
          <button className="link" onClick={() => store.openSettings('hosts')}>
            Add host
          </button>
        )}
        {/* Closing the window leaves the app on the tray so approvals keep
            arriving; this is the one control that actually ends it. */}
        <button className="link danger" onClick={() => void bridge.app.quit()}>
          Quit Helios
        </button>
      </footer>

      {menu && (
        <SelectionMenu
          x={menu.x}
          y={menu.y}
          actions={
            menu.kind === 'session'
              ? sessionActions(
                  menu.hostId,
                  menu.session,
                  groupsUnsupported[menu.hostId] ? [] : (groupsByHost[menu.hostId] ?? []),
                  providers,
                )
              : groupActions(menu.hostId, menu.key, () =>
                  setRenaming(`${menu.hostId}:${menu.key}`),
                )
          }
          onClose={() => setMenu(null)}
        />
      )}

      {/* The panel's own edge, not a bar between the panels: the list is what
          the pointer is already near, and a gutter belongs to neither side. */}
      <div
        className="sidebar-grip"
        role="separator"
        aria-label="Resize the session list"
        title="Drag to resize — double-click to reset"
        onPointerDown={resize}
        onDoubleClick={() => {
          document.documentElement.style.removeProperty('--sidebar')
          writeSidebarWidth(null)
        }}
      />
    </aside>
  )
}

/**
 * What can be done to a group, as the right-click on its header offers it.
 *
 * Only ever called for a real group: Ungrouped is a heading the tree draws over
 * the sessions nothing has claimed, and there is no record behind it to rename
 * or remove.
 */
function groupActions(hostId: string, key: string, onRename: () => void): MenuAction[] {
  return [
    { label: 'Rename', run: onRename },
    {
      label: 'Delete',
      danger: true,
      // Nothing inside is lost — the daemon lifts the sessions and any child
      // groups up a level — so this does not stop to ask.
      title: 'Delete the group — its sessions and subgroups move up a level',
      run: () => void store.deleteGroup(hostId, key),
    },
  ]
}

/**
 * Names a group or a session, in place.
 *
 * Inline rather than a dialog: the point of a `+` on a header is that the
 * parent is already chosen by where you clicked, and a modal would ask again
 * with a field the header could just have shown. The same holds for a row —
 * and a dialog is not on offer anyway, since Electron does not implement
 * window.prompt.
 */
function InlineNameField({
  onCommit,
  onCancel,
  initial = '',
  className = 'new-group',
  placeholder = 'Group name',
}: {
  onCommit: (name: string) => void
  onCancel: () => void
  /** The name already on the header, when this is a rename rather than a new
   *  one. Seeded into state rather than left as a defaultValue, so the field
   *  stays controlled — see below for what uncontrolled cost. */
  initial?: string
  className?: string
  placeholder?: string
}): JSX.Element {
  const [draft, setDraft] = useState(initial)
  const field = useRef<HTMLInputElement | null>(null)
  // Enter commits and the parent unmounts this input, which fires blur — so
  // without a latch the same name is submitted twice, or a commit races the
  // cancel that follows it.
  const done = useRef(false)
  // autoFocus does not fire reliably for an input mounted into a tree that is
  // already on screen — the button that opened it keeps focus, so the keystrokes
  // go nowhere and the field looks like a button that did nothing.
  useEffect(() => {
    field.current?.focus()
  }, [])
  // Controlled, so React owns the value. Uncontrolled looked simpler and cost a
  // day: anything setting the field programmatically — a test, an autofill —
  // writes straight to the DOM node, and a handler reading React's idea of it
  // sees an empty string.
  const commit = (typed: string): void => {
    if (done.current) return
    done.current = true
    const name = typed.trim()
    if (name) onCommit(name)
    else onCancel()
  }
  return (
    <input
      ref={field}
      className={className}
      placeholder={placeholder}
      value={draft}
      onChange={(event) => setDraft(event.target.value)}
      onKeyDown={(event) => {
        if (event.key === 'Enter') commit(event.currentTarget.value)
        if (event.key === 'Escape') {
          done.current = true
          onCancel()
        }
      }}
      onBlur={(event) => commit(event.target.value)}
    />
  )
}

/**
 * One session, two lines: what it is called, and what it is.
 *
 * The first line is identity — status, title, the one action, how long ago —
 * and the title has the whole width of it. The second is configuration, in
 * small print: which agent, which model, under which permissions, holding how
 * much. Compact folds the second line away, which is the difference between
 * the two densities.
 */
function SessionRow({
  hostId,
  session,
  pending,
  selected,
  draggable,
  dragging,
  accepts,
  editing,
  onDragStart,
  onDragEnd,
  onContextMenu,
  onDropBefore,
}: {
  hostId: string
  session: Session
  pending: number
  selected: boolean
  /** Only in manual mode: dragging a card in an auto-sorted list means nothing. */
  draggable: boolean
  dragging: boolean
  /** A drag is in flight and this row is somewhere it may be dropped. */
  accepts: boolean
  /** The title is a field rather than a label, because this is the row being
   *  renamed. */
  editing: boolean
  onDragStart: () => void
  onDragEnd: () => void
  onContextMenu: (x: number, y: number) => void
  onDropBefore: (draggedId: string) => void
}): JSX.Element {
  const live = hasTerminal(session)
  const busy = BUSY_STATUSES.has(session.status)
  const terminated = canResume(session)
  const cold = needsRecovery(session)
  const label = sessionLabel(session)
  const classes = [
    'session-row',
    session.status,
    selected ? 'selected' : '',
    dragging ? 'dragging' : '',
    draggable ? 'movable' : '',
  ]
  return (
    <article
      className={classes.filter(Boolean).join(' ')}
      // A draggable ancestor takes the pointer off the field inside it: the
      // browser starts a drag instead of placing the caret, so a title cannot
      // be clicked into while the row can be moved.
      draggable={draggable && !editing}
      onDragStart={(event) => {
        // Firefox and Chromium both want data on the transfer or the drag never
        // starts; the id is also what makes the drop unambiguous.
        event.dataTransfer.effectAllowed = 'move'
        event.dataTransfer.setData('text/plain', session.session_id)
        // Again under a type of its own, so a group header can tell this from a
        // header being dragged and file the session instead of moving a group.
        event.dataTransfer.setData(sessionDrag(hostId), session.session_id)
        onDragStart()
      }}
      onDragEnd={onDragEnd}
      onDragOver={(event) => {
        if (!draggable || !accepts) return
        // Without this the drop never fires: the default is to refuse.
        event.preventDefault()
        event.dataTransfer.dropEffect = 'move'
      }}
      onDrop={(event) => {
        if (!draggable || !accepts) return
        event.preventDefault()
        onDropBefore(event.dataTransfer.getData('text/plain'))
      }}
      onClick={() => store.select(hostId, session.session_id)}
      // Pointed at, not opened. A right-click is a question about a session,
      // and answering it by loading that session into the panel throws away
      // whatever the user was reading — the one thing they did not ask for.
      onContextMenu={(event) => {
        event.preventDefault()
        onContextMenu(event.clientX, event.clientY)
      }}
      // A terminated session has to be resumed before it has a terminal worth
      // opening, so the shortcut resumes instead of waking one it will refuse.
      // Double-clicking a word in the field is how you edit one word of a
      // title, and it must not also open the terminal underneath.
      onDoubleClick={() => {
        if (editing) return
        void (terminated
          ? store.resumeSession(hostId, session.session_id)
          : store.openTerminal(hostId, session, !live))
      }}
    >
      <div className="row-main">
        {editing ? (
          <InlineNameField
            className="row-rename"
            placeholder={label}
            initial={session.title ?? ''}
            onCancel={() => store.endSessionRename()}
            onCommit={(title) => {
              store.endSessionRename()
              if (title !== (session.title ?? '')) {
                void store.patchSessionField(hostId, session.session_id, { title })
              }
            }}
          />
        ) : (
          <span className="row-title" title={label}>
            {label}
          </span>
        )}
        {cold && (
          <span className="cold-mark" title="Cold — no live terminal">
            ⚯
          </span>
        )}
        {session.pinned && (
          <span className="pin" title="Pinned">
            ★
          </span>
        )}
        {pending > 0 && (
          <span className="badge" title={pending === 1 ? '1 waiting on you' : `${pending} waiting on you`}>
            {pending}
          </span>
        )}
        {/* Two shapes for two situations. A live or cold session offers an icon
            that opens out of nothing under the pointer, so an unhovered row
            spends its whole width on the title. A terminated one gets the word,
            because resuming is the only move it has left and it should not have
            to be hovered to say so. */}
        {/* Hidden while the field is up: it opens what is already open. */}
        {!editing && (
          <button
            className="row-act"
            aria-label="Rename session"
            title="Rename"
            onClick={(event) => {
              event.stopPropagation()
              store.renameSession(hostId, session.session_id)
            }}
          >
            <Pencil />
          </button>
        )}
        {terminated ? (
          <button
            className="row-btn resume"
            title="Resume — bring the agent back"
            onClick={(event) => {
              event.stopPropagation()
              void store.resumeSession(hostId, session.session_id)
            }}
          >
            Resume
          </button>
        ) : (
          <button
            className="row-act"
            aria-label={live ? 'Open terminal' : 'Wake and open terminal'}
            title={live ? 'Open terminal' : 'Cold — wake and open terminal'}
            onClick={(event) => {
              event.stopPropagation()
              void store.openTerminal(hostId, session, !live)
            }}
          >
            <Console />
          </button>
        )}
        {/* Last of the row's actions, and the right-click for a pointer that
            does not have one: the only way to reach Terminate, Pin, the groups
            and the permission modes without knowing the menu is there. Opens
            under the button rather than at the pointer, since the button has a
            fixed place. */}
        {!editing && (
          <button
            className="row-act row-more"
            aria-label="Session actions"
            title="Session actions"
            onClick={(event) => {
              event.stopPropagation()
              const box = event.currentTarget.getBoundingClientRect()
              onContextMenu(box.left, box.bottom + 4)
            }}
          >
            ⋯
          </button>
        )}
        <span className="row-time">{timeAgo(session.last_event_at ?? session.created_at)}</span>
      </div>

      {/* What the session is, under what it is called. Kept off the title's
          line so the title gets the whole width — a long one is the common
          case, and it was losing half its room to a trail of small print. */}
      {/* The status was a coloured dot with a tooltip, which is the least
          legible thing a row can say about the thing that matters most. In
          words, and first, it reads without being hovered — and the provider it
          replaces was "claude" on every row until a second one ships. */}
      <div className="row-sub">
        <span className={`row-status ${session.status}${busy ? ' pulse' : ''}`}>
          {statusLabel(session.status)}
        </span>
        {session.model !== undefined && (
          <span className="row-model" title={session.model}>
            {shortModel(session.model, session.source)}
          </span>
        )}
        {session.permission_mode !== undefined && (
          <span className="row-mode" title={session.permission_mode}>
            {shortMode(session.permission_mode)}
          </span>
        )}
        <span className="grow" />
        {session.memory_bytes !== undefined && (
          <span className="row-ram" title="Memory this terminal holds">
            {formatBytes(session.memory_bytes)}
          </span>
        )}
      </div>
    </article>
  )
}

/**
 * The order the user put them in, and nothing else.
 *
 * No tie-breaking on activity: the whole point is that a card does not move
 * when a session does something. Sessions the daemon has not numbered yet sort
 * by creation, newest first, which is where a new one belongs.
 */
function byHand(a: Row, b: Row): number {
  const left = a.session.sort_order ?? 0
  const right = b.session.sort_order ?? 0
  if (left !== right) return left - right
  return b.session.created_at.localeCompare(a.session.created_at)
}

/** Pending approvals first, then live sessions, then most recent activity. */
function compareRows(a: Row, b: Row): number {
  if (a.pending !== b.pending) return b.pending - a.pending
  if (a.session.pinned !== b.session.pinned) return a.session.pinned ? -1 : 1
  const aLive = hasTerminal(a.session)
  const bLive = hasTerminal(b.session)
  if (aLive !== bLive) return aLive ? -1 : 1
  return (b.session.last_event_at ?? b.session.created_at).localeCompare(
    a.session.last_event_at ?? a.session.created_at,
  )
}

/**
 * What this host is carrying: its warm terminals, then the machine behind
 * them.
 *
 * The daemon lets idle sessions go cold once the pool passes the budget, so
 * the budget is what the reading turns amber against — but it is a number the
 * user chose, and the machine's own free memory is the one they can check
 * against anything else. So the line reads warm against the machine, and the
 * budget stays in the tooltip with the advice that depends on it.
 *
 * It belongs to the host and not the window — memory is a machine's to run out
 * of, and a session on the laptop says nothing about the one on the VM.
 */
function HostMeter({ stats }: { stats?: HostStats }): JSX.Element | null {
  if (!stats || stats.warm === 0) return null
  const metered = stats.budget > 0
  const heavy = metered && stats.warm_rss > stats.budget
  const machine = stats.memory_total > 0
  return (
    <div
      className={heavy ? 'host-meter heavy' : 'host-meter'}
      title={
        heavy
          ? `${stats.warm} warm sessions holding ${formatBytes(stats.warm_rss)} of ${formatBytes(stats.budget)} allowed — the ones you have not opened lately will go cold`
          : metered
            ? `${stats.warm} warm sessions holding ${formatBytes(stats.warm_rss)} of ${formatBytes(stats.budget)} allowed`
            : `${stats.warm} warm sessions holding ${formatBytes(stats.warm_rss)} — no limit set, nothing is evicted`
      }
    >
      <Memory />
      <span>{formatBytes(stats.warm_rss)}</span>
      {/* The machine's own figure is context for the pool's, not a thing to
          act on, so it is the first thing a narrow sidebar drops. */}
      {machine && (
        <span className="host-machine">of {formatPair(stats.memory_used, stats.memory_total)}</span>
      )}
      <Cpu />
      <span>{Math.round(stats.load * 100)}%</span>
    </div>
  )
}

/** Two figures sharing one unit: "21.3/34.4 GB" rather than "21.3 GB/34.4 GB". */
function formatPair(used: number, total: number): string {
  if (total >= 1024 ** 3) return `${(used / 1024 ** 3).toFixed(1)}/${(total / 1024 ** 3).toFixed(1)} GB`
  return `${Math.round(used / 1024 ** 2)}/${Math.round(total / 1024 ** 2)} MB`
}

function formatBytes(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  return `${Math.round(bytes / 1024 ** 2)} MB`
}
