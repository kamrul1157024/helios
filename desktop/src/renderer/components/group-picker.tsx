import { useEffect, useRef, useState } from 'react'

import { store, useStore } from '../store.ts'
import type { SortMode } from '../store.ts'

/**
 * How the list is arranged: whether it is grouped at all, and what orders it.
 *
 * A popover rather than another toolbar toggle because there are several
 * questions now, and a row of icons that all mean "order" would be a guess
 * every time. The groups themselves are not managed here: they are made,
 * renamed, moved and deleted on the tree, where the one being pointed at is
 * the one that changes.
 */
export function GroupPicker({
  hostId,
  hostName,
  manual,
  onClose,
}: {
  /** Whose groups these are. Null when no host is paired yet. */
  hostId: string | null
  hostName: string
  manual: boolean
  onClose: () => void
}): JSX.Element {
  const grouping = useStore((s) => s.grouping)
  const unsupported = useStore((s) => (hostId ? Boolean(s.groupsUnsupported[hostId]) : false))
  const panel = useRef<HTMLDivElement | null>(null)
  const [busy, setBusy] = useState(false)

  // Dismissed the same way the row menu is: a click elsewhere, or Escape.
  useEffect(() => {
    const onDown = (event: MouseEvent): void => {
      if (!panel.current?.contains(event.target as Node)) onClose()
    }
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') onClose()
    }
    const timer = setTimeout(() => document.addEventListener('mousedown', onDown), 0)
    document.addEventListener('keydown', onKey)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose])

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

      {grouping && unsupported && (
        <>
          <div className="picker-sep" />
          <div className="picker-note">
            {hostName || 'This machine'} is running a daemon without grouping. Update it to make
            groups here.
          </div>
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
