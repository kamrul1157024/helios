import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { PathLabel } from './path-label.tsx'
import { SelectionMenu } from './selection-menu.tsx'
import { modeActions } from './session-menu.ts'
import { gitStatusQuery, providersQuery } from '../queries.ts'
import { currentZone, useStore } from '../store.ts'
import type { SegmentId } from '../../shared/status-line.ts'
import { BUSY_STATUSES, statusLabel, type Session } from '../../shared/models.ts'

/**
 * One line at the foot of the pane saying where the session is and what it is
 * doing.
 *
 * It replaced a 67px header carrying the same facts, so it is deliberately as
 * short as it can be read at: a fixed height rather than padding, and a tint
 * rather than a rule, because a hairline costs a pixel and reads heavier than
 * the thing it separates.
 *
 * One bar for the pane rather than one per split group. Every fact here belongs
 * to the session — its directory, its model, whether it is busy — and none of
 * them changes with which panel happens to be in front.
 */
export function StatusLine({ hostId, session }: { hostId: string; session: Session }): JSX.Element | null {
  const order = useStore((s) => s.statusLine)
  const hosts = useStore((s) => s.hosts)
  const vimEnabled = useStore((s) => s.vimEnabled)
  const vimMode = useStore((s) => s.vimMode)
  const vimPending = useStore((s) => s.vimPending)
  const zone = useStore(currentZone)
  const [modeMenu, setModeMenu] = useState<{ x: number; y: number } | null>(null)

  const wanted = new Set(order)

  // Asked for whenever the bar shows the branch, which is the point: the branch
  // was previously only knowable by opening the Git panel. It costs nothing to
  // keep current — the key sits under `keys.git(hostId)`, which a file_changed
  // event already invalidates, so a checkout lands here without polling.
  const { data: git } = useQuery({
    ...gitStatusQuery(hostId, session.cwd),
    enabled: wanted.has('branch') && session.cwd !== '',
  })

  // Only once the mode menu has been asked for. The list is shared with the new
  // session dialog and never goes stale, so this is usually a cache hit, but a
  // bar that nobody has clicked should not pay for it.
  const { data: providers } = useQuery({ ...providersQuery(hostId), enabled: modeMenu !== null })

  // Vim mode keeps the bar even when every session segment has been switched
  // off: with the keymap on, what mode the keyboard is in is the one fact on
  // this line you cannot work without.
  if (order.length === 0 && !vimEnabled) return null

  const segment = (id: SegmentId): JSX.Element | null => {
    switch (id) {
      case 'cwd':
        return <PathLabel key={id} path={session.cwd} className="status-cwd" />

      // Bare, as the Git panel and the worktree list already show it. A glyph
      // in front reads as noise at 11px, and the branch is legible without one.
      case 'branch':
        if (!git?.branch) return null
        return (
          <span key={id} title={git.dirty ? `${git.branch} — uncommitted changes` : git.branch}>
            <span className="branch">
              {git.branch}
              {git.dirty && '*'}
            </span>
            {git.ahead > 0 && `↑${git.ahead}`}
            {git.behind > 0 && `↓${git.behind}`}
          </span>
        )

      case 'model':
        if (!session.model) return null
        return (
          <span key={id} title="Model">
            {session.model}
          </span>
        )

      case 'mode':
        if (!session.permission_mode) return null
        return (
          <button
            key={id}
            className="status-mode"
            title="Permission mode — click to change"
            onClick={(event) => setModeMenu({ x: event.clientX, y: event.clientY })}
          >
            {session.permission_mode}
          </button>
        )

      // The header's chip, stripped of its pill by the stylesheet. Reused
      // rather than restated so the status colours have one definition.
      case 'status':
        return (
          <span key={id} className={`chip ${session.status}`}>
            <span className={BUSY_STATUSES.has(session.status) ? 'dot pulse' : 'dot'} />
            {statusLabel(session.status)}
          </span>
        )

      case 'memory':
        if (session.memory_bytes === undefined) return null
        return (
          <span key={id} className="card-ram" title="Memory this session's terminal holds">
            {formatBytes(session.memory_bytes)}
          </span>
        )

      case 'host':
        return (
          <span key={id} title="Host">
            {hosts.find((host) => host.id === hostId)?.name ?? hostId}
          </span>
        )

      case 'id':
        return (
          <span key={id} title={session.session_id}>
            {session.session_id.slice(0, 8)}
          </span>
        )
    }
  }

  return (
    <footer className={vimEnabled ? `status-line vim ${vimMode}` : 'status-line'}>
      {/* First, and coloured, because the mode changes what every key does —
          the session facts beside it are true either way. */}
      {vimEnabled && <span className="vim-mode">{vimMode.toUpperCase()}</span>}
      {vimEnabled && <span className="vim-zone">{zone}</span>}

      {order.map(segment)}

      {/* Last and hard right, so a half-typed sequence appears in the same
          place whatever the segments in front of it are doing. */}
      {vimEnabled && vimPending && <span className="vim-pending">{vimPending}</span>}

      {modeMenu && (
        <SelectionMenu
          x={modeMenu.x}
          y={modeMenu.y}
          actions={modeActions(hostId, session, providers)}
          onClose={() => setModeMenu(null)}
        />
      )}
    </footer>
  )
}

export function formatBytes(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  return `${Math.round(bytes / 1024 ** 2)} MB`
}
