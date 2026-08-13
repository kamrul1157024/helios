import { useEffect, useRef, useState } from 'react'

import { api, statusOf } from '../bridge.ts'
import { store, terminalId, useStore, type RightPanel, type Tab } from '../store.ts'
import { ApprovalsPanel } from './approvals.tsx'
import { ChatPanel } from './chat.tsx'
import { PanelBoundary } from './error-boundary.tsx'
import { FilesPanel } from './files.tsx'
import { GitPanel } from './git.tsx'
import { TerminalPanes } from './terminal.tsx'
import {
  BUSY_STATUSES,
  canResume,
  hasTerminal,
  needsRecovery,
  sessionLabel,
  statusLabel,
  type Session,
} from '../../shared/models.ts'

const PANELS: RightPanel[] = ['chat', 'terminal', 'approvals', 'git', 'files']

// The transcript is the agent's side of the session, not a chat room. The
// store key stays 'chat' — it is persisted and referenced from elsewhere.
const PANEL_LABELS: Record<RightPanel, string> = {
  chat: 'agent',
  terminal: 'terminal',
  approvals: 'approvals',
  git: 'git',
  files: 'files',
}

/**
 * Every panel but the terminal, which keeps itself mounted a level up.
 *
 * A panel holds work as much as it displays it — a file open in the editor, a
 * diff scrolled to the hunk being read, a transcript scrolled back through —
 * and unmounting on a tab switch throws all of it away. Once opened they stay
 * mounted and hidden.
 */
const KEEP_MOUNTED: RightPanel[] = ['chat', 'approvals', 'git', 'files']

/** How long an unseen panel keeps its state before it is unmounted. */
const PANEL_TTL = 5 * 60 * 1000
const SWEEP_INTERVAL = 60 * 1000

/**
 * Panels the sweep leaves alone. What they hold is a place the user chose —
 * the file they were reading, the diff they were working through — and losing
 * it to a timer means finding it again by hand. They cost nothing while
 * hidden, so they stay until the session does.
 */
const NO_TTL: RightPanel[] = ['git', 'files']

export function Detail(): JSX.Element {
  const selection = useStore((s) => s.selection)
  const sessions = useStore((s) => s.sessions)
  const notifications = useStore((s) => s.notifications)
  const panel = useStore((s) => s.panel)
  const tabs = useStore((s) => s.tabs)

  const hostId = selection?.hostId ?? null
  const session =
    (selection && sessions[selection.hostId]?.find((s) => s.session_id === selection.sessionId)) ?? null
  const pendingList = Boolean(selection) && sessions[selection?.hostId ?? ''] === undefined

  const pending = session
    ? (notifications[hostId ?? ''] ?? []).filter((n) => n.source_session === session.session_id).length
    : 0
  const term = hostId && session ? tabs.find((t) => t.id === terminalId(hostId, session.session_id)) : undefined
  const activeTab = useStore((s) => s.activeTab)
  const shells = tabs.filter(
    (t) => t.kind === 'shell' && t.hostId === hostId && t.sessionId === session?.session_id,
  )
  const onAgentTab = !activeTab || activeTab === term?.id

  // Shells outlive the app, so a restart would leave them running and
  // invisible. Listing them is also how a second window learns about one.
  useEffect(() => {
    if (!hostId || !session) return
    void store.syncShells(hostId, session.session_id)
  }, [hostId, session?.session_id])

  // When each panel was last on screen, for the idle sweep below.
  const [kept, setKept] = useState<Partial<Record<RightPanel, number>>>({})
  const sessionKey = hostId && session ? `${hostId}:${session.session_id}` : ''
  // A terminated session's file and diff describe a working tree nobody is
  // changing any more, so they close with it. Switching sessions drops them
  // too: they belong to the tree they were opened from.
  const terminated = session ? canResume(session) : false

  useEffect(() => {
    setKept({})
  }, [sessionKey, terminated])

  useEffect(() => {
    if (!KEEP_MOUNTED.includes(panel) || terminated) return
    setKept((current) => ({ ...current, [panel]: Date.now() }))
  }, [panel, terminated])

  // Held state is worth memory for as long as the user is moving between
  // panels, and not much longer. A panel untouched for the timeout is
  // unmounted, and comes back fetched fresh.
  useEffect(() => {
    const sweep = setInterval(() => {
      setKept((current) => {
        const cutoff = Date.now() - PANEL_TTL
        const next = Object.fromEntries(
          Object.entries(current).filter(
            ([name, seen]) =>
              name === panel || NO_TTL.includes(name as RightPanel) || seen > cutoff,
          ),
        ) as Partial<Record<RightPanel, number>>
        return Object.keys(next).length === Object.keys(current).length ? current : next
      })
    }, SWEEP_INTERVAL)
    return () => clearInterval(sweep)
  }, [panel])

  return (
    <div className="detail">
      {hostId && session && (
        <>
          <SessionHeader hostId={hostId} session={session} />

          {/* One strip: the panels, the session's own terminal, then the
              shells opened beside it. Tabs within a tab would be a hierarchy
              nobody asked for — a shell is a sibling of the transcript, not a
              mode of the terminal. */}
          <nav className="panel-tabs">
            {PANELS.map((name) => (
              <button
                key={name}
                className={panel === name && (name !== 'terminal' || onAgentTab) ? 'active' : ''}
                onClick={() => (name === 'terminal' ? store.showTerminal(hostId, session) : store.setPanel(name))}
              >
                {/* The old tabstrip carried the connection state, and it is
                    still the one thing about a terminal worth seeing from
                    another panel. */}
                {name === 'terminal' && term && <span className={`dot ${term.status.state}`} />}
                {PANEL_LABELS[name]}
                {name === 'approvals' && pending > 0 && <span className="badge">{pending}</span>}
                {name === 'terminal' && term && (
                  <span
                    className="tab-close"
                    role="button"
                    aria-label="Reconnect"
                    title="Reconnect — the agent keeps running"
                    onClick={(event) => {
                      event.stopPropagation()
                      void store.reconnectTab(term.id)
                    }}
                  >
                    ⟳
                  </span>
                )}
              </button>
            ))}

            {shells.map((shell) => (
              <ShellTab key={shell.id} tab={shell} active={panel === 'terminal' && activeTab === shell.id} />
            ))}

            <button
              className="tab-add"
              title="New shell in this session's directory"
              aria-label="New shell"
              onClick={() => void store.openShell(hostId, session.session_id)}
            >
              +
            </button>
          </nav>
        </>
      )}

      <div className="panel-body">
        {!session &&
          (pendingList ? (
            // An unfetched host has no entry at all, an empty one has []. Without
            // that distinction a selected session reads as deleted for as long as
            // its host takes to answer.
            <div className="panel-loading">
              <span className="spinner" />
              <span>Loading session…</span>
            </div>
          ) : (
            <div className="panel-empty">
              <p>{selection ? 'That session is no longer listed.' : 'Select a session.'}</p>
            </div>
          ))}

        {hostId &&
          session &&
          KEEP_MOUNTED.filter((name) => name === panel || kept[name]).map((name) => (
            <div key={name} className="panel-keep" hidden={name !== panel}>
              <PanelBoundary resetKey={`${hostId}:${session.session_id}:${name}`}>
                {/* Approvals ride alongside the transcript instead of behind
                    their own tab: an agent that stops for permission stops the
                    panel the user is already looking at, and a tab round-trip
                    per approval is the whole interaction. */}
                {name === 'chat' && (
                  <div className="agent-split">
                    <ChatPanel hostId={hostId} session={session} active={name === panel} />
                    {pending > 0 && (
                      <aside className="approvals-dock">
                        <h3 className="dock-title">
                          Approvals <span className="badge">{pending}</span>
                        </h3>
                        <ApprovalsPanel hostId={hostId} sessionId={session.session_id} />
                      </aside>
                    )}
                  </div>
                )}
                {name === 'approvals' && (
                  <ApprovalsPanel hostId={hostId} sessionId={session.session_id} />
                )}
                {name === 'git' && (
                  <GitPanel
                    hostId={hostId}
                    cwd={session.cwd}
                    revision={session.last_event_at}
                    sessionId={session.session_id}
                    active={name === panel}
                  />
                )}
                {name === 'files' && (
                  <FilesPanel
                    hostId={hostId}
                    sessionId={session.session_id}
                    cwd={session.cwd}
                    visible={name === panel}
                  />
                )}
              </PanelBoundary>
            </div>
          ))}

        {/* Outside the switch above, and outside the boundary: the panes stay
            mounted whatever is selected, or every terminal would lose its
            scrollback the moment the user looked at another panel. */}
        <TerminalPanes hostId={hostId} session={session} visible={panel === 'terminal' && Boolean(session)} />
      </div>
    </div>
  )
}

/**
 * A shell's tab. Closing it kills the process — unlike the agent's terminal,
 * which is the session's and only ever detaches — so the cross is the real
 * thing here, and the name is the user's to set.
 */
function ShellTab({ tab, active }: { tab: Tab; active: boolean }): JSX.Element {
  const [renaming, setRenaming] = useState(false)
  const [draft, setDraft] = useState(tab.title)

  if (renaming) {
    return (
      <input
        className="tab-rename"
        autoFocus
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => {
          store.renameTab(tab.id, draft)
          setRenaming(false)
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter') event.currentTarget.blur()
          if (event.key === 'Escape') {
            setDraft(tab.title)
            setRenaming(false)
          }
        }}
      />
    )
  }

  return (
    <button
      className={active ? 'active' : ''}
      onClick={() => store.selectTab(tab.id)}
      onDoubleClick={() => {
        setDraft(tab.title)
        setRenaming(true)
      }}
      title={`${tab.title} — double-click to rename`}
    >
      <span className={`dot ${tab.status.state}`} />
      {tab.title}
      <span
        className="tab-close"
        role="button"
        aria-label="Close shell"
        title="Close this shell"
        onClick={(event) => {
          event.stopPropagation()
          void store.killShell(tab.id)
        }}
      >
        ×
      </span>
    </button>
  )
}

function SessionHeader({ hostId, session }: { hostId: string; session: Session }): JSX.Element {
  const [renaming, setRenaming] = useState(false)
  const [title, setTitle] = useState(session.title ?? '')
  const overflow = useRef<HTMLDetailsElement | null>(null)
  const live = hasTerminal(session)
  const busy = BUSY_STATUSES.has(session.status)
  const terminated = canResume(session)
  const cold = needsRecovery(session)

  // <details> only closes on its own summary, so a menu left open stays open
  // over whatever the user clicks next.
  useEffect(() => {
    const close = (event: Event): void => {
      const element = overflow.current
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

  const run = async (fn: () => Promise<unknown>): Promise<void> => {
    try {
      await fn()
      await store.refreshSessions(hostId)
    } catch (err) {
      store.fail(err)
    }
  }

  return (
    <header className="detail-head">
      <div className="detail-title">
        {renaming ? (
          <input
            autoFocus
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            onBlur={() => {
              setRenaming(false)
              if (title !== (session.title ?? '')) {
                void run(() => api(hostId).patchSession(session.session_id, { title }))
              }
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter') event.currentTarget.blur()
              if (event.key === 'Escape') {
                setTitle(session.title ?? '')
                setRenaming(false)
              }
            }}
          />
        ) : (
          <h1 onDoubleClick={() => setRenaming(true)}>{sessionLabel(session)}</h1>
        )}
        <span className="detail-cwd" title={session.cwd}>
          {session.cwd}
        </span>
      </div>

      <div className="detail-actions">
        <span className={`chip ${session.status}`}>
          <span className={busy ? 'dot pulse' : 'dot'} />
          {statusLabel(session.status)}
        </span>

        {cold && (
          <button
            className="ghost cold"
            title="Cold — no live terminal. Resume brings the agent back."
            onClick={() => void store.resumeSession(hostId, session.session_id)}
          >
            ⚯ Cold
          </button>
        )}

        <PermissionMode hostId={hostId} session={session} />

        {/* Resume, not Wake: waking a terminated session starts its host but
            leaves the daemon refusing every prompt. */}
        {terminated ? (
          <button
            className="filled"
            onClick={() => void store.resumeSession(hostId, session.session_id)}
          >
            Resume
          </button>
        ) : (
          <button className="filled" onClick={() => void store.openTerminal(hostId, session, !live)}>
            {live ? 'Terminal' : 'Wake'}
          </button>
        )}

        {busy && <button className="ghost" onClick={() => void run(() => api(hostId).stop(session.session_id))}>Stop</button>}

        <details className="menu" ref={overflow}>
          <summary>⋯</summary>
          <div
            className="menu-body"
            onClick={() => {
              if (overflow.current) overflow.current.open = false
            }}
          >
            <button onClick={() => void run(() => api(hostId).generateTitle(session.session_id))}>
              Regenerate title
            </button>
            <button
              onClick={() =>
                void run(() => api(hostId).patchSession(session.session_id, { pinned: !session.pinned }))
              }
            >
              {session.pinned ? 'Unpin' : 'Pin'}
            </button>
            <button
              onClick={() =>
                void run(() =>
                  api(hostId).patchSession(session.session_id, { archived: !session.archived }),
                )
              }
            >
              {session.archived ? 'Unarchive' : 'Archive'}
            </button>
            <button onClick={() => void run(() => api(hostId).terminate(session.session_id))}>
              Terminate
            </button>
            <button
              className="danger"
              onClick={() => {
                // Deleting drops the daemon's record; the agent's own transcript
                // on disk is untouched, which is worth saying before the click.
                if (confirm('Remove this session from Helios? The transcript file stays on disk.')) {
                  store.closeTab(`${hostId}:${session.session_id}`)
                  void run(() => api(hostId).deleteSession(session.session_id))
                }
              }}
            >
              Delete
            </button>
          </div>
        </details>
      </div>
    </header>
  )
}

/**
 * Switching mode restarts the agent, because the CLI takes it as a launch flag.
 * The daemon refuses while a session is mid-turn; the control is disabled to
 * match, but the server check is the one that counts.
 */
function PermissionMode({ hostId, session }: { hostId: string; session: Session }): JSX.Element | null {
  const [modes, setModes] = useState<string[] | null>(null)
  const [pending, setPending] = useState(false)

  if (session.source !== 'claude') return null

  const load = async (): Promise<void> => {
    if (modes) return
    try {
      const providers = await api(hostId).providers()
      setModes(providers.find((p) => p.id === session.source)?.permission_modes ?? [])
    } catch {
      setModes([])
    }
  }

  const switchable = session.status === 'idle'

  const change = async (mode: string): Promise<void> => {
    if (!mode || mode === session.permission_mode) return
    setPending(true)
    try {
      await api(hostId).setPermissionMode(session.session_id, mode)
      store.notify(`Permission mode set to ${mode}`)
      await store.refreshSessions(hostId)
    } catch (err) {
      if (statusOf(err) === 409) store.notify('Session is busy — try again when it is idle', 'error')
      else store.fail(err)
    } finally {
      setPending(false)
    }
  }

  return (
    <select
      className="mode-select"
      value={session.permission_mode ?? ''}
      disabled={!switchable || pending}
      title={switchable ? 'Restarts the agent' : 'Only while the session is idle'}
      onFocus={() => void load()}
      onChange={(event) => void change(event.target.value)}
    >
      {/* The modes list loads on focus, so the current value needs an option of
          its own until it arrives. */}
      {!session.permission_mode && <option value="">default</option>}
      {(modes ?? [session.permission_mode].filter((m): m is string => Boolean(m))).map((mode) => (
        <option key={mode} value={mode}>
          {mode}
        </option>
      ))}
    </select>
  )
}
