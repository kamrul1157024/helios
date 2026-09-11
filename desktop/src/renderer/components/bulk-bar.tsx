import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { providersQuery } from '../queries.ts'
import { store } from '../store.ts'
import { actionsFor } from './session-selection.ts'
import { SelectionMenu, type MenuAction } from './selection-menu.tsx'
import type { Session, SessionGroup } from '../../shared/models.ts'

/**
 * What can be done to everything picked, and how many that is.
 *
 * The bar is where a bulk action lives rather than the row's own menu: a menu
 * opened on one row and applied to nine reads as acting on the row it was
 * opened from. Here the count is the first thing said, so what is about to
 * happen and how much of it are in the same sentence.
 */
export function BulkBar({
  held,
  rows,
  groups,
}: {
  held: string[]
  rows: { hostId: string; session: Session }[]
  groups: Record<string, SessionGroup[]>
}): JSX.Element {
  const [menu, setMenu] = useState<{ kind: 'file' | 'mode'; x: number; y: number } | null>(null)
  const summary = actionsFor(held, rows)
  const hostId = held.length > 0 ? (rows.find((row) => held.includes(`${row.hostId}:${row.session.session_id}`))?.hostId ?? '') : ''
  const { data: providers } = useQuery({ ...providersQuery(hostId), enabled: hostId !== '' })

  if (summary.count === 0) {
    return (
      <div className="bulk-bar">
        <span className="bulk-count">Pick sessions to act on several at once</span>
        <button className="ghost" onClick={() => store.setSelectMode(false)}>
          Done
        </button>
      </div>
    )
  }

  const filing: MenuAction[] = [
    {
      label: 'No group',
      run: () => void store.bulk('Filed', (host, id) => api(host).patchSession(id, { group_key: '' })),
    },
    ...(groups[hostId] ?? []).map((group) => ({
      label: group.name,
      run: () =>
        void store.bulk('Filed', (host, id) => api(host).patchSession(id, { group_key: group.key })),
    })),
  ]

  // The modes the provider offers, taken from the first session held: a
  // selection spanning two providers has no one list, and the daemon refuses
  // what does not apply anyway.
  const modes: MenuAction[] = (providers ?? [])
    .flatMap((provider) => provider.permission_modes ?? [])
    .filter((mode, at, all) => all.indexOf(mode) === at)
    .map((mode) => ({
      label: mode,
      run: () => void store.bulk(`Set ${mode} on`, (host, id) => api(host).setPermissionMode(id, mode)),
    }))

  return (
    <div className="bulk-bar">
      <span className="bulk-count">{summary.count} selected</span>

      <button
        className="ghost"
        onClick={() =>
          void store.bulk(summary.pin === 'pin' ? 'Pinned' : 'Unpinned', (host, id) =>
            api(host).patchSession(id, { pinned: summary.pin === 'pin' }),
          )
        }
      >
        {summary.pin === 'pin' ? 'Pin' : 'Unpin'}
      </button>

      {/* Groups belong to a host, so there is no group that means the same
          thing across two daemons. */}
      <button
        className="ghost"
        disabled={!summary.canFile}
        title={summary.canFile ? 'File all of them under one group' : 'The selection is on more than one host'}
        onClick={(event) => setMenu({ kind: 'file', x: event.clientX, y: event.clientY })}
      >
        Move to group
      </button>

      <button
        className="ghost"
        disabled={modes.length === 0}
        onClick={(event) => setMenu({ kind: 'mode', x: event.clientX, y: event.clientY })}
      >
        Permission mode
      </button>

      <button
        className="ghost danger"
        disabled={!summary.canTerminate}
        title={summary.canTerminate ? 'Stop the agents' : 'Nothing selected is still running'}
        onClick={() => {
          if (!confirm(`Terminate ${summary.count} sessions? Each agent stops, and only Resume brings it back.`)) {
            return
          }
          void store.bulk('Terminated', (host, id) => api(host).terminate(id))
        }}
      >
        Terminate
      </button>

      <button
        className="ghost danger"
        onClick={() => {
          if (!confirm(`Remove ${summary.count} sessions from Helios? Their transcript files stay on disk.`)) {
            return
          }
          for (const key of held) store.closeTab(key)
          void store.bulk('Removed', (host, id) => api(host).deleteSession(id))
        }}
      >
        Delete
      </button>

      <span className="grow" />
      <button className="ghost" onClick={() => store.setSelectMode(false)}>
        Done
      </button>

      {menu && (
        <SelectionMenu
          x={menu.x}
          y={menu.y}
          actions={menu.kind === 'file' ? filing : modes}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  )
}
