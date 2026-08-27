import { useEffect, useMemo, useRef, useState } from 'react'

import { api, bridge } from '../bridge.ts'
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
import { Chevron, Console, Cpu, Memory, MultiLine, Plus, Search, SingleLine, Sort } from './icons.tsx'
import { placeOf, tintOf } from './projects.ts'
import { SelectionMenu, type MenuAction } from './selection-menu.tsx'

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

/** One directory, and every session the host is running in it. */
interface Project {
  /** The project's name, folded — what its sessions are gathered on. */
  key: string
  name: string
  /** Where a new session in this project starts. */
  cwd: string
  rows: Row[]
  /** What this project's warm terminals hold, summed over its rows. */
  memory: number
}

interface Group {
  host: HostRecord
  projects: Project[]
  /** Sessions shown, across every project — what the host head counts. */
  count: number
  /** Terminated sessions withheld from rows, so the host can offer them. */
  hidden: number
  /** The host has not answered yet, which is not the same as having nothing. */
  loading: boolean
}

/** A session being dragged, and the project it may be dropped back into. */
interface Drag {
  hostId: string
  projectKey: string
  sessionId: string
}

export function Sidebar({
  onNewSession,
  onAddHost,
  onSettings,
}: {
  onNewSession: (seed?: { hostId: string; cwd: string }) => void
  onAddHost: () => void
  onSettings: () => void
}): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const hostStatus = useStore((s) => s.hostStatus)
  const sessions = useStore((s) => s.sessions)
  const stats = useStore((s) => s.stats)
  const notifications = useStore((s) => s.notifications)
  const selection = useStore((s) => s.selection)
  const query = useStore((s) => s.query)
  const sortMode = useStore((s) => s.sortMode)
  const density = useStore((s) => s.density)
  // The card being dragged, so the row under the pointer can show where it
  // would land. Held per host: a drag never crosses from one daemon to another.
  const [dragging, setDragging] = useState<Drag | null>(null)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  // Folded projects, keyed by host and path together: two machines can hold
  // checkouts at the same path, and folding one should not fold the other.
  const [folded, setFolded] = useState<Record<string, boolean>>({})
  // Per host, not global: a machine kept for finished work and one being
  // worked in want opposite answers, and the setting is one click away.
  const [showTerminated, setShowTerminated] = useState<Record<string, boolean>>({})
  // The session a right-click named, and where the pointer was. Held for the
  // whole list rather than per row, so a second right-click moves the one menu
  // instead of opening another beside it.
  const [menu, setMenu] = useState<{ hostId: string; session: Session; x: number; y: number } | null>(
    null,
  )
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

      // Projects take the position of their best session rather than an order
      // of their own: grouping arranges the list, it does not re-rank it, so a
      // session that wants attention still carries its project to the top.
      const byProject = new Map<string, Project>()
      for (const row of ordered) {
        const place = placeOf(row.session)
        let project = byProject.get(place.key)
        if (!project) {
          project = { key: place.key, name: place.name, cwd: place.cwd, rows: [], memory: 0 }
          byProject.set(place.key, project)
        }
        project.rows.push(row)
        project.memory += row.session.memory_bytes ?? 0
      }

      const hidden = hideTerminated ? visible.filter(isTerminated).length : 0
      // An unfetched host has no entry at all, an empty one has []. Without the
      // distinction a daemon that is slow to answer looks like a daemon with
      // nothing on it.
      const loading = sessions[host.id] === undefined
      return { host, projects: [...byProject.values()], count: ordered.length, hidden, loading }
    })
  }, [hosts, sessions, notifications, query, showTerminated, sortMode])

  // One host answering "manual" is enough to show the switch as on: the click
  // writes the other way to every host, which settles any disagreement.
  const manual = hosts.some((host) => sortMode[host.id] === 'manual')

  return (
    <aside className="sidebar" ref={aside}>
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
        <button
          className={manual ? 'tool sort-toggle on' : 'tool sort-toggle'}
          aria-label={manual ? 'Sorting by hand' : 'Sorting by activity'}
          aria-pressed={manual}
          title={
            manual
              ? 'Sort: Manual — drag a session to move it.\nClick to sort by activity instead.'
              : 'Sort: Activity — approvals first, then live, then most recent.\nClick to arrange them by hand instead.'
          }
          onClick={() => void store.setSortModeEverywhere(manual ? 'activity' : 'manual')}
        >
          <Sort />
        </button>
        {/* Beside the sort toggle because it is the same kind of control: not
            a preference set once, but a way of looking at the list, changed
            while looking at it. Shows the state it is in, as the sort does. */}
        <button
          className="tool density-toggle"
          aria-label={density === 'compact' ? 'Compact list' : 'Comfortable list'}
          title={
            density === 'compact'
              ? 'Compact — one line a session, title and time.\nClick for the second line back.'
              : 'Comfortable — a second line with the agent, model and memory.\nClick to fit more on screen.'
          }
          onClick={() => void store.setDensity(density === 'compact' ? 'comfortable' : 'compact')}
        >
          {density === 'compact' ? <SingleLine /> : <MultiLine />}
        </button>
        <button
          className="tool primary"
          aria-label="New session"
          title="New session (⌘N)"
          onClick={() => onNewSession()}
        >
          <Plus />
        </button>
      </header>

      <div className="sidebar-list">
        {grouped.map(({ host, projects, count, hidden, loading }) => {
          const status = hostStatus[host.id]?.state ?? 'connecting'
          const isCollapsed = collapsed[host.id] ?? false
          const revealed = showTerminated[host.id] ?? false
          // The whole host in display order, which is what a reorder posts.
          const order = projects.flatMap((project) => project.rows.map((row) => row.session.session_id))
          return (
            <section key={host.id} className="host-group">
              <div className="host-head">
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

                {/* One line under the host: what its terminals cost on the
                    left, and the sessions it is not showing on the right. Both
                    are facts about this machine, and neither is worth a row of
                    its own. */}
                {!isCollapsed && (
                  <div className="host-meta">
                    <HostMeter stats={stats[host.id]} />
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

              {!isCollapsed &&
                projects.map((project) => {
                  const foldKey = `${host.id}:${project.key}`
                  const isFolded = folded[foldKey] ?? false
                  return (
                    <div className="project" key={project.key}>
                      <div className={isFolded ? 'project-head folded' : 'project-head'}>
                        <button
                          className="project-title"
                          title={project.cwd}
                          aria-expanded={!isFolded}
                          onClick={() => setFolded((f) => ({ ...f, [foldKey]: !isFolded }))}
                        >
                          <span className="project-badge" style={{ '--tint': tintOf(project.key) } as React.CSSProperties}>
                            {project.name.slice(0, 1).toUpperCase()}
                          </span>
                          <span className="project-name">{project.name}</span>
                          <span className="project-count">{project.rows.length}</span>
                          {project.memory > 0 && (
                            <>
                              <span className="project-sep">·</span>
                              <span className="project-ram">{formatBytes(project.memory)}</span>
                            </>
                          )}
                          <Chevron className="chevron project-chevron" open={!isFolded} />
                        </button>
                        <button
                          className="project-add"
                          aria-label={`New session in ${project.name}`}
                          title={`New session in ${project.name}`}
                          onClick={() => onNewSession({ hostId: host.id, cwd: project.cwd })}
                        >
                          <Plus />
                        </button>
                      </div>

                      {!isFolded && (
                        <div className="project-rows">
                          {project.rows.map(({ session, pending }) => (
                            <SessionRow
                              key={session.session_id}
                              hostId={host.id}
                              session={session}
                              pending={pending}
                              selected={
                                selection?.hostId === host.id && selection.sessionId === session.session_id
                              }
                              draggable={sortMode[host.id] === 'manual'}
                              dragging={dragging?.sessionId === session.session_id}
                              // Only inside its own project: the daemon holds one
                              // flat order per host, so a card dropped across the
                              // divide would drag its whole project with it.
                              accepts={dragging?.hostId === host.id && dragging.projectKey === project.key}
                              onDragStart={() =>
                                setDragging({
                                  hostId: host.id,
                                  projectKey: project.key,
                                  sessionId: session.session_id,
                                })
                              }
                              onDragEnd={() => setDragging(null)}
                              onContextMenu={(x, y) => setMenu({ hostId: host.id, session, x, y })}
                              onDropBefore={(draggedId) => {
                                // The id off the drag itself, not React state: the
                                // drop can arrive in the same tick as the drag
                                // start, before a setState has committed, and then
                                // nothing moves.
                                const ids = [...order]
                                const from = ids.indexOf(draggedId)
                                const to = ids.indexOf(session.session_id)
                                setDragging(null)
                                if (from === -1 || to === -1 || from === to) return
                                ids.splice(to, 0, ids.splice(from, 1)[0] as string)
                                void store.reorderSessions(host.id, ids)
                              }}
                            />
                          ))}
                        </div>
                      )}
                    </div>
                  )
                })}

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
                      <span className="row-dot" />
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

              {!isCollapsed && !loading && count === 0 && hidden === 0 && (
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
            <button onClick={onAddHost}>Add host</button>
          </div>
        )}
      </div>

      <footer className="sidebar-foot">
        <button className="link" onClick={onAddHost}>
          Add host
        </button>
        <AppMenu onSettings={onSettings} />
      </footer>

      {menu && (
        <SelectionMenu
          x={menu.x}
          y={menu.y}
          actions={sessionActions(menu.hostId, menu.session)}
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
 * What can be done to a session, as the right-click on its row offers it.
 *
 * These were buttons in the detail header, where they applied to whichever
 * session happened to be open: acting on any other one meant selecting it
 * first and reading the header to check it had changed. On the row they name
 * the session under the pointer, which is the one being pointed at.
 */
function sessionActions(hostId: string, session: Session): MenuAction[] {
  const run = async (fn: () => Promise<unknown>): Promise<void> => {
    try {
      await fn()
      await store.refreshSessions(hostId)
    } catch (err) {
      store.fail(err)
    }
  }

  const actions: MenuAction[] = [
    {
      label: 'Regenerate title',
      // The daemon waits for the model before answering, so this can sit for
      // several seconds. Saying what came back is the difference between a
      // slow menu item and a broken one.
      run: () =>
        void run(async () => {
          store.notify('Naming the session…')
          const result = await api(hostId).generateTitle(session.session_id)
          if (result.title) store.notify(result.title)
          else store.notify('The model did not return a usable title', 'error')
        }),
    },
    {
      label: session.pinned ? 'Unpin' : 'Pin',
      run: () =>
        void run(() => api(hostId).patchSession(session.session_id, { pinned: !session.pinned })),
    },
  ]

  // Nothing to end when it has already ended, and the row itself carries the
  // Resume that a terminated session is waiting for.
  if (!canResume(session)) {
    actions.push({
      label: 'Terminate',
      danger: true,
      run: () => {
        if (confirm('Terminate this session? The agent stops, and only Resume brings it back.')) {
          void run(() => api(hostId).terminate(session.session_id))
        }
      },
    })
  }

  actions.push({
    label: 'Delete',
    danger: true,
    run: () => {
      // Deleting drops the daemon's record; the agent's own transcript on disk
      // is untouched, which is worth saying before the click.
      if (confirm('Remove this session from Helios? The transcript file stays on disk.')) {
        store.closeTab(`${hostId}:${session.session_id}`)
        void run(() => api(hostId).deleteSession(session.session_id))
      }
    },
  })

  return actions
}

/**
 * Settings and Quit, which otherwise live only on the tray and the app menu —
 * neither of which is where the eye goes, and the app menu is not somewhere a
 * user looks on the platforms where the window is the whole of the app.
 */
function AppMenu({ onSettings }: { onSettings: () => void }): JSX.Element {
  const menu = useRef<HTMLDetailsElement | null>(null)

  // <details> only closes on its own summary, so a menu left open stays open
  // over whatever the user clicks next.
  useEffect(() => {
    const close = (event: Event): void => {
      const element = menu.current
      if (!element?.open) return
      if (event.type === 'keydown' && (event as KeyboardEvent).key !== 'Escape') return
      if (event.type === 'mousedown' && event.target instanceof Node && element.contains(event.target)) return
      element.open = false
    }
    window.addEventListener('mousedown', close)
    window.addEventListener('keydown', close)
    window.addEventListener('blur', close)
    return () => {
      window.removeEventListener('mousedown', close)
      window.removeEventListener('keydown', close)
      window.removeEventListener('blur', close)
    }
  }, [])

  return (
    <details className="menu drop-up" ref={menu}>
      <summary title="More">⋯</summary>
      <div
        className="menu-body"
        onClick={() => {
          if (menu.current) menu.current.open = false
        }}
      >
        <button onClick={onSettings}>Settings…</button>
        {/* Closing the window leaves the app on the tray so approvals keep
            arriving; this is the one control that actually ends it. */}
        <button className="danger" onClick={() => void bridge.app.quit()}>
          Quit Helios
        </button>
      </div>
    </details>
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
      draggable={draggable}
      onDragStart={(event) => {
        // Firefox and Chromium both want data on the transfer or the drag never
        // starts; the id is also what makes the drop unambiguous.
        event.dataTransfer.effectAllowed = 'move'
        event.dataTransfer.setData('text/plain', session.session_id)
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
      // Selected as well as pointed at: the menu acts on this session, and the
      // panel beside it should be showing the one that is about to change.
      onContextMenu={(event) => {
        event.preventDefault()
        store.select(hostId, session.session_id)
        onContextMenu(event.clientX, event.clientY)
      }}
      // A terminated session has to be resumed before it has a terminal worth
      // opening, so the shortcut resumes instead of waking one it will refuse.
      onDoubleClick={() =>
        void (terminated
          ? store.resumeSession(hostId, session.session_id)
          : store.openTerminal(hostId, session, !live))
      }
    >
      <div className="row-main">
        <span
          className={`row-dot ${session.status}${busy ? ' pulse' : ''}`}
          title={statusLabel(session.status)}
        />
        <span className="row-title" title={label}>
          {label}
        </span>
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
            that is already in the row before the pointer arrives and only fades
            in — a button that appears into the layout re-measures the title
            under the cursor, and a list that reflows as the pointer crosses it
            is what makes hovering feel jarring. A terminated one gets the word,
            because resuming is the only move it has left and it should not have
            to be hovered to say so. */}
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
        <span className="row-time">{timeAgo(session.last_event_at ?? session.created_at)}</span>
      </div>

      {/* What the session is, under what it is called. Kept off the title's
          line so the title gets the whole width — a long one is the common
          case, and it was losing half its room to a trail of small print. */}
      <div className="row-sub">
        <span className="row-provider">{session.source}</span>
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
