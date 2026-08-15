import { useEffect, useMemo, useRef, useState } from 'react'

import { bridge } from '../bridge.ts'
import { store, useStore } from '../store.ts'
import {
  BUSY_STATUSES,
  canResume,
  hasTerminal,
  isTerminated,
  needsRecovery,
  sessionLabel,
  shortCwd,
  statusLabel,
  timeAgo,
  type HostRecord,
  type Session,
} from '../../shared/models.ts'

interface Row {
  host: HostRecord
  session: Session
  pending: number
}

interface Group {
  host: HostRecord
  rows: Row[]
  /** Terminated sessions withheld from rows, so the host can offer them. */
  hidden: number
  /** The host has not answered yet, which is not the same as having nothing. */
  loading: boolean
}

export function Sidebar({
  onNewSession,
  onAddHost,
  onSettings,
}: {
  onNewSession: () => void
  onAddHost: () => void
  onSettings: () => void
}): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const hostStatus = useStore((s) => s.hostStatus)
  const sessions = useStore((s) => s.sessions)
  const notifications = useStore((s) => s.notifications)
  const selection = useStore((s) => s.selection)
  const query = useStore((s) => s.query)
  const showArchived = useStore((s) => s.showArchived)
  const sortMode = useStore((s) => s.sortMode)
  // The card being dragged, so the row under the pointer can show where it
  // would land. Held per host: a drag never crosses from one daemon to another.
  const [dragging, setDragging] = useState<{ hostId: string; sessionId: string } | null>(null)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  // Per host, not global: a machine kept for finished work and one being
  // worked in want opposite answers, and the setting is one click away.
  const [showTerminated, setShowTerminated] = useState<Record<string, boolean>>({})

  const grouped = useMemo<Group[]>(() => {
    const needle = query.trim().toLowerCase()
    return hosts.map((host) => {
      const pendingByCwd = new Map<string, number>()
      for (const notif of notifications[host.id] ?? []) {
        pendingByCwd.set(notif.source_session, (pendingByCwd.get(notif.source_session) ?? 0) + 1)
      }

      const visible = (sessions[host.id] ?? [])
        .filter((session) => showArchived || !session.archived)
        .filter((session) => {
          if (!needle) return true
          return `${session.title ?? ''} ${session.project} ${session.cwd} ${session.last_user_message ?? ''}`
            .toLowerCase()
            .includes(needle)
        })

      // A terminated session the user searched for is a session they asked to
      // see, so the filter yields to an explicit query.
      const hideTerminated = !showTerminated[host.id] && !needle
      const rows: Row[] = visible
        .filter((session) => !hideTerminated || !isTerminated(session))
        .map((session) => ({ host, session, pending: pendingByCwd.get(session.session_id) ?? 0 }))
        .sort(sortMode[host.id] === 'manual' ? byHand : compareRows)

      const hidden = hideTerminated ? visible.filter(isTerminated).length : 0
      // An unfetched host has no entry at all, an empty one has []. Without the
      // distinction a daemon that is slow to answer looks like a daemon with
      // nothing on it.
      const loading = sessions[host.id] === undefined
      return { host, rows, hidden, loading }
    })
  }, [hosts, sessions, notifications, query, showArchived, showTerminated, sortMode])

  // One host answering "manual" is enough to show the switch as on: the click
  // writes the other way to every host, which settles any disagreement.
  const manual = hosts.some((host) => sortMode[host.id] === 'manual')

  return (
    <aside className="sidebar">
      <header className="sidebar-head">
        <div className="search-field">
          <span className="search-icon" aria-hidden="true">
            ⌕
          </span>
          <input
            className="search"
            placeholder="Search sessions"
            value={query}
            onChange={(event) => store.setQuery(event.target.value)}
          />
        </div>
        <button
          className={manual ? 'fab sort-toggle on' : 'fab sort-toggle'}
          aria-label={manual ? 'Sorting by hand' : 'Sorting by activity'}
          title={
            manual
              ? 'Sort: Manual — drag a session to move it.\nClick to sort by activity instead.'
              : 'Sort: Activity — approvals first, then live, then most recent.\nClick to arrange them by hand instead.'
          }
          onClick={() => void store.setSortModeEverywhere(manual ? 'activity' : 'manual')}
        >
          ⇅
        </button>
        <button className="fab" title="New session (⌘N)" onClick={onNewSession}>
          +
        </button>
      </header>

      <div className="sidebar-list">
        {grouped.map(({ host, rows, hidden, loading }) => {
          const status = hostStatus[host.id]?.state ?? 'connecting'
          const isCollapsed = collapsed[host.id] ?? false
          return (
            <section key={host.id} className="host-group">
              <button
                className="host-head"
                onClick={() => setCollapsed((c) => ({ ...c, [host.id]: !isCollapsed }))}
              >
                <span className={`dot ${status}`} title={hostStatus[host.id]?.error ?? status} />
                <span className="host-name">{host.name}</span>
                {/* No count until there is one: a 0 that turns into 12 reads as
                    an answer, and it was not one. */}
                {!loading && <span className="host-count">{rows.length}</span>}
                <span className="chevron">{isCollapsed ? '▸' : '▾'}</span>
              </button>

              {/* Above the list, not below it: revealed sessions are appended,
                  so a toggle under them walks further down the page with every
                  use and the way back is a scroll. */}
              {!isCollapsed && (hidden > 0 || showTerminated[host.id]) && (
                <button
                  className="link show-terminated"
                  onClick={() =>
                    setShowTerminated((current) => ({ ...current, [host.id]: !current[host.id] }))
                  }
                >
                  {showTerminated[host.id] ? 'Hide terminated' : `Show ${hidden} terminated`}
                </button>
              )}

              {!isCollapsed &&
                rows.map(({ session, pending }) => (
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
                    onDragStart={() => setDragging({ hostId: host.id, sessionId: session.session_id })}
                    onDragEnd={() => setDragging(null)}
                    onDropBefore={(draggedId) => {
                      // The id off the drag itself, not React state: the drop
                      // can arrive in the same tick as the drag start, before
                      // a setState has committed, and then nothing moves.
                      const ids = rows.map((row) => row.session.session_id)
                      const from = ids.indexOf(draggedId)
                      const to = ids.indexOf(session.session_id)
                      setDragging(null)
                      if (from === -1 || to === -1 || from === to) return
                      ids.splice(to, 0, ids.splice(from, 1)[0] as string)
                      void store.reorderSessions(host.id, ids)
                    }}
                  />
                ))}

              {/* Skeletons rather than a spinner: the list is about to be a
                  list, and showing its shape keeps the sidebar from resizing
                  under the cursor when the rows arrive.

                  Built from the card's own elements rather than a stack of
                  bars, so it is the height of a session card by construction
                  and stays that way when the card changes. */}
              {!isCollapsed &&
                loading &&
                [0, 1, 2].map((index) => (
                  <div key={index} className="session-card skeleton" aria-hidden="true">
                    <div className="card-inner">
                      <div className="card-top">
                        <span className="skeleton-line chip" />
                        <span className="grow" />
                        <span className="skeleton-line time" />
                      </div>
                      <div className="card-title">
                        <span className="skeleton-line" />
                      </div>
                      <div className="card-cwd">
                        <span className="skeleton-line cwd" />
                      </div>
                      <div className="card-bottom">
                        <span className="skeleton-line meta" />
                        <span className="skeleton-line btn" />
                      </div>
                    </div>
                  </div>
                ))}

              {!isCollapsed && !loading && rows.length === 0 && hidden === 0 && (
                <p className="empty-note">No sessions</p>
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
        <label className="check">
          <input
            type="checkbox"
            checked={showArchived}
            onChange={(event) => store.setShowArchived(event.target.checked)}
          />
          Show archived
        </label>
        <button className="link" onClick={onAddHost}>
          Add host
        </button>
        <AppMenu onSettings={onSettings} />
      </footer>
    </aside>
  )
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

function SessionRow({
  hostId,
  session,
  pending,
  selected,
  draggable,
  dragging,
  onDragStart,
  onDragEnd,
  onDropBefore,
}: {
  hostId: string
  session: Session
  pending: number
  selected: boolean
  /** Only in manual mode: dragging a card in an auto-sorted list means nothing. */
  draggable: boolean
  dragging: boolean
  onDragStart: () => void
  onDragEnd: () => void
  onDropBefore: (draggedId: string) => void
}): JSX.Element {
  const live = hasTerminal(session)
  const busy = BUSY_STATUSES.has(session.status)
  const terminated = canResume(session)
  const cold = needsRecovery(session)
  return (
    <article
      className={`session-card ${session.status} ${selected ? 'selected' : ''}${dragging ? ' dragging' : ''}${draggable ? ' movable' : ''}`}
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
        if (!draggable) return
        // Without this the drop never fires: the default is to refuse.
        event.preventDefault()
        event.dataTransfer.dropEffect = 'move'
      }}
      onDrop={(event) => {
        if (!draggable) return
        event.preventDefault()
        onDropBefore(event.dataTransfer.getData('text/plain'))
      }}
      onClick={() => store.select(hostId, session.session_id)}
      // A terminated session has to be resumed before it has a terminal worth
      // opening, so the shortcut resumes instead of waking one it will refuse.
      onDoubleClick={() =>
        void (terminated
          ? store.resumeSession(hostId, session.session_id)
          : store.openTerminal(hostId, session, !live))
      }
    >
      <div className="card-inner">
        <div className="card-top">
          <span className={`chip ${session.status}`}>
            <span className={busy ? 'dot pulse' : 'dot'} />
            {statusLabel(session.status)}
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
          {pending > 0 && <span className="badge">{pending}</span>}
          <span className="grow" />
          <span className="time">{timeAgo(session.last_event_at ?? session.created_at)}</span>
        </div>

        <div className="card-title">{sessionLabel(session)}</div>
        <div className="card-cwd" title={session.cwd}>
          {shortCwd(session.cwd)}
        </div>

        <div className="card-bottom">
          <span className="card-meta">
            {session.model ?? session.source}
            {session.permission_mode ? ` · ${shortMode(session.permission_mode)}` : ''}
          </span>
          <button
            className={terminated ? 'row-btn resume' : 'row-btn'}
            title={
              terminated
                ? 'Resume — bring the agent back'
                : live
                  ? 'Open terminal'
                  : 'Cold — wake and open terminal'
            }
            onClick={(event) => {
              event.stopPropagation()
              if (terminated) void store.resumeSession(hostId, session.session_id)
              else void store.openTerminal(hostId, session, !live)
            }}
          >
            {terminated ? 'Resume' : live ? 'Terminal' : 'Wake'}
          </button>
        </div>
      </div>
    </article>
  )
}

/** Pending approvals first, then live sessions, then most recent activity. */
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

function shortMode(mode: string): string {
  switch (mode) {
    case 'acceptEdits':
      return 'accept edits'
    case 'bypassPermissions':
      return 'bypass'
    case 'plan':
      return 'plan'
    default:
      return mode
  }
}
