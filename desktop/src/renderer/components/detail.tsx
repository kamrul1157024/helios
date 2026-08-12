import { useState } from 'react'

import { api, statusOf } from '../bridge.ts'
import { store, terminalId, useStore, type RightPanel } from '../store.ts'
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

export function Detail(): JSX.Element {
  const selection = useStore((s) => s.selection)
  const sessions = useStore((s) => s.sessions)
  const notifications = useStore((s) => s.notifications)
  const panel = useStore((s) => s.panel)
  const tabs = useStore((s) => s.tabs)

  const hostId = selection?.hostId ?? null
  const session =
    (selection && sessions[selection.hostId]?.find((s) => s.session_id === selection.sessionId)) ?? null

  const pending = session
    ? (notifications[hostId ?? ''] ?? []).filter((n) => n.source_session === session.session_id).length
    : 0
  const term = hostId && session ? tabs.find((t) => t.id === terminalId(hostId, session.session_id)) : undefined

  return (
    <div className="detail">
      {hostId && session && (
        <>
          <SessionHeader hostId={hostId} session={session} />

          <nav className="panel-tabs">
            {PANELS.map((name) => (
              <button
                key={name}
                className={panel === name ? 'active' : ''}
                onClick={() => (name === 'terminal' ? store.showTerminal(hostId, session) : store.setPanel(name))}
              >
                {/* The old tabstrip carried the connection state, and it is
                    still the one thing about a terminal worth seeing from
                    another panel. */}
                {name === 'terminal' && term && <span className={`dot ${term.status.state}`} />}
                {name}
                {name === 'approvals' && pending > 0 && <span className="badge">{pending}</span>}
                {name === 'terminal' && term && (
                  <span
                    className="tab-close"
                    role="button"
                    aria-label="Close terminal"
                    onClick={(event) => {
                      event.stopPropagation()
                      store.closeTab(term.id)
                    }}
                  >
                    ×
                  </span>
                )}
              </button>
            ))}
          </nav>
        </>
      )}

      <div className="panel-body">
        {!session && (
          <div className="panel-empty">
            <p>{selection ? 'That session is no longer listed.' : 'Select a session.'}</p>
          </div>
        )}

        {hostId && session && panel !== 'terminal' && (
          <PanelBoundary resetKey={`${hostId}:${session.session_id}:${panel}`}>
            {panel === 'chat' && <ChatPanel hostId={hostId} session={session} />}
            {panel === 'approvals' && <ApprovalsPanel hostId={hostId} sessionId={session.session_id} />}
            {panel === 'git' && (
              <GitPanel hostId={hostId} cwd={session.cwd} revision={session.last_event_at} />
            )}
            {panel === 'files' && <FilesPanel hostId={hostId} cwd={session.cwd} />}
          </PanelBoundary>
        )}

        {/* Outside the switch above, and outside the boundary: the panes stay
            mounted whatever is selected, or every terminal would lose its
            scrollback the moment the user looked at another panel. */}
        <TerminalPanes hostId={hostId} session={session} visible={panel === 'terminal' && Boolean(session)} />
      </div>
    </div>
  )
}

function SessionHeader({ hostId, session }: { hostId: string; session: Session }): JSX.Element {
  const [renaming, setRenaming] = useState(false)
  const [title, setTitle] = useState(session.title ?? '')
  const live = hasTerminal(session)
  const busy = BUSY_STATUSES.has(session.status)
  const terminated = canResume(session)
  const cold = needsRecovery(session)

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

        <details className="menu">
          <summary>⋯</summary>
          <div className="menu-body">
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
