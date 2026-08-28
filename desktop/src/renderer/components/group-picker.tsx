import { useEffect, useRef, useState } from 'react'

import { store, useStore } from '../store.ts'
import type { SortMode } from '../store.ts'
import { tintOf } from './grouping.ts'
import type { SessionGroup } from '../../shared/models.ts'

/** One shared empty array. A fresh `[]` from the selector is a new reference
 *  every render, which useSyncExternalStore reads as a changed snapshot — and
 *  that is an infinite render loop, not a re-render. */
const NO_GROUPS: SessionGroup[] = []

/**
 * How the list is arranged, and the only place groups can be managed.
 *
 * A popover rather than another toolbar toggle because there are several
 * questions now, and a row of icons that all mean "order" would be a guess
 * every time. Managing groups lives here too: the row menu can only reach a
 * group that already has a session in it, which leaves no way to make one
 * first, rename it, or place an empty one.
 */
export function GroupPicker({
  hostId,
  hostName,
  showHostName,
  manual,
  onClose,
}: {
  /** Whose groups these are. Null when no host is paired yet. */
  hostId: string | null
  hostName: string
  /** Named only when there is more than one, so the single-host case stays quiet. */
  showHostName: boolean
  manual: boolean
  onClose: () => void
}): JSX.Element {
  const grouping = useStore((s) => s.grouping)
  const directoryDepth = useStore((s) => s.directoryDepth)
  const groups = useStore((s) => (hostId ? (s.groups[hostId] ?? NO_GROUPS) : NO_GROUPS))
  const panel = useRef<HTMLDivElement | null>(null)
  const [busy, setBusy] = useState(false)
  const [adding, setAdding] = useState(false)
  const [draft, setDraft] = useState('')
  const [renaming, setRenaming] = useState<string | null>(null)
  const [dragKey, setDragKey] = useState<string | null>(null)

  // Dismissed the same way the row menu is: a click elsewhere, or Escape. Not
  // while a name is being typed — Escape there should abandon the field, and
  // closing the whole panel would lose the word half-written in it.
  useEffect(() => {
    const editing = adding || renaming !== null
    const onDown = (event: MouseEvent): void => {
      if (!panel.current?.contains(event.target as Node)) onClose()
    }
    const onKey = (event: KeyboardEvent): void => {
      if (event.key !== 'Escape') return
      if (editing) {
        setAdding(false)
        setRenaming(null)
        return
      }
      onClose()
    }
    const timer = setTimeout(() => document.addEventListener('mousedown', onDown), 0)
    document.addEventListener('keydown', onKey)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose, adding, renaming])

  // The value comes off the field rather than out of state, as the rename does:
  // Enter can arrive in the same tick as the last keystroke, before React has
  // committed it, and then the group is created with the name minus its last
  // letter — or with no name at all.
  const commitNew = async (typed: string): Promise<void> => {
    const name = typed.trim()
    setAdding(false)
    setDraft('')
    if (!name || !hostId) return
    await store.createGroup(hostId, name)
  }

  const commitRename = async (key: string, name: string): Promise<void> => {
    setRenaming(null)
    const next = name.trim()
    if (!next || !hostId) return
    await store.renameGroup(hostId, key, next)
  }

  // Directory is a grouping key like the rest, so it lives in the same list
  // rather than behind a checkbox of its own. Its place in the list is the
  // depth it nests at: first and it gathers, last and it splits.
  const levels: (SessionGroup | 'dir')[] = [...groups]
  if (directoryDepth !== null) levels.splice(Math.min(directoryDepth, levels.length), 0, 'dir')

  return (
    <div className="group-picker" ref={panel} role="dialog" aria-label="Arrange the list">
      <label className="picker-check">
        <input
          type="checkbox"
          checked={grouping}
          disabled={busy}
          onChange={async (event) => {
            setBusy(true)
            await store.setGrouping(event.target.checked)
            setBusy(false)
          }}
        />
        <span>Group sessions</span>
      </label>

      {grouping && (
        <>
          <div className="picker-sep" />
          <div className="picker-head">{showHostName ? `Groups on ${hostName}` : 'Groups'}</div>
          {groups.length === 0 && !adding && (
            <div className="picker-note">None yet. Make one, then file sessions into it.</div>
          )}

          {levels.map((entry, index) =>
            entry === 'dir' ? (
              <div className="picker-group derived" key="dir">
                <span className="picker-handle" aria-hidden="true">⠿</span>
                <span className="picker-group-name">Directory</span>
                <span className="picker-derived" title="Read from each session's working directory, not assigned">
                  auto
                </span>
                <button
                  className="picker-icon"
                  aria-label="Move Directory up"
                  disabled={index === 0}
                  onClick={() => store.setDirectoryDepth(index - 1)}
                >
                  ⌃
                </button>
                <button
                  className="picker-icon"
                  aria-label="Move Directory down"
                  disabled={index === levels.length - 1}
                  onClick={() => store.setDirectoryDepth(index + 1)}
                >
                  ⌄
                </button>
                <button
                  className="picker-icon danger"
                  aria-label="Remove the Directory level"
                  onClick={() => store.setDirectoryDepth(null)}
                >
                  ×
                </button>
              </div>
            ) : ((group) => (
            <div
              key={group.key}
              className={dragKey === group.key ? 'picker-group dragging' : 'picker-group'}
              draggable={renaming === null}
              onDragStart={(event) => {
                event.dataTransfer.effectAllowed = 'move'
                event.dataTransfer.setData('text/plain', group.key)
                setDragKey(group.key)
              }}
              onDragEnd={() => setDragKey(null)}
              onDragOver={(event) => {
                if (dragKey === group.key) return
                event.preventDefault()
                event.dataTransfer.dropEffect = 'move'
              }}
              // The key comes off the transfer rather than out of state: a drop
              // can land in the same tick as the drag start, before setDragKey
              // has committed, and then nothing moves.
              onDrop={(event) => {
                event.preventDefault()
                const dragged = event.dataTransfer.getData('text/plain')
                setDragKey(null)
                if (!hostId || !dragged || dragged === group.key) return
                const keys = groups.map((g) => g.key)
                const from = keys.indexOf(dragged)
                const to = keys.indexOf(group.key)
                if (from === -1 || to === -1) return
                keys.splice(to, 0, keys.splice(from, 1)[0] as string)
                void store.reorderGroups(hostId, keys)
              }}
            >
              <span className="picker-handle" aria-hidden="true">
                ⠿
              </span>
              <span className="group-badge" style={{ '--tint': tintOf(group.key) } as React.CSSProperties}>
                {group.name.slice(0, 1).toUpperCase()}
              </span>
              {renaming === group.key ? (
                <input
                  className="picker-input"
                  autoFocus
                  defaultValue={group.name}
                  onBlur={(event) => void commitRename(group.key, event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') void commitRename(group.key, event.currentTarget.value)
                  }}
                />
              ) : (
                <span className="picker-group-name">{group.name}</span>
              )}
              <button
                className="picker-icon"
                aria-label={`Rename ${group.name}`}
                title="Rename"
                onClick={() => setRenaming(group.key)}
              >
                ✎
              </button>
              <button
                className="picker-icon danger"
                aria-label={`Delete ${group.name}`}
                title="Delete — the sessions stay, they just leave the group"
                onClick={() => {
                  if (!hostId) return
                  // Only the grouping is lost, which is why this does not ask.
                  void store.deleteGroup(hostId, group.key)
                }}
              >
                ×
              </button>
            </div>
          ))(entry),
          )}

          {directoryDepth === null && (
            <button
              className="picker-item"
              onClick={() => store.setDirectoryDepth(groups.length)}
            >
              + Directory
            </button>
          )}

          {adding ? (
            <input
              className="picker-input new"
              autoFocus
              placeholder="Group name"
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onBlur={(event) => void commitNew(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') void commitNew(event.currentTarget.value)
              }}
            />
          ) : (
            <button className="picker-item" disabled={hostId === null} onClick={() => setAdding(true)}>
              + New group
            </button>
          )}
        </>
      )}

      <div className="picker-sep" />
      <div className="picker-head">Order sessions by</div>
      <button
        className={manual ? 'picker-item' : 'picker-item on'}
        onClick={() => void store.setSortModeEverywhere('activity' as SortMode)}
        disabled={hostId === null}
      >
        <span className="picker-radio">{manual ? '○' : '●'}</span> Activity
      </button>
      <button
        className={manual ? 'picker-item on' : 'picker-item'}
        onClick={() => void store.setSortModeEverywhere('manual' as SortMode)}
        disabled={hostId === null}
      >
        <span className="picker-radio">{manual ? '●' : '○'}</span> Manual — drag to move
      </button>
    </div>
  )
}
