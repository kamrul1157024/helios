import { useEffect, useRef, useState } from 'react'

import { store, useStore } from '../store.ts'
import type { GroupMode, GroupOrder, SortMode } from '../store.ts'

/** The choices, in the order they widen: nothing, then a tree the user keeps,
 *  then one the app works out. */
const GROUP_MODES: { mode: GroupMode; label: string; hint: string }[] = [
  { mode: 'off', label: 'Off', hint: 'One flat list, in whatever order is set below.' },
  {
    mode: 'manual',
    label: 'Manual grouping',
    hint:
      'Groups you make and keep. Nest them as deep as you like, drag a session onto a group to file it, ' +
      'and right-click a group to rename or delete it. Deleting one moves its sessions and subgroups up a level.',
  },
  {
    mode: 'auto',
    label: 'Directory',
    hint:
      "One group per working directory, worked out from the sessions themselves. Nothing to set up and " +
      'nothing to maintain, but it is a single level and you cannot move a session between directories.',
  },
]

/**
 * What orders the directory groups. Offered only in that mode: a made group
 * sits where the user dragged its header to, and a choice here would fight the
 * drag rather than add to it.
 */
const GROUP_ORDERS: { order: GroupOrder; label: string }[] = [
  { order: 'activity', label: 'Activity' },
  { order: 'name', label: 'Name A→Z' },
  { order: 'manual', label: 'Manual — drag to move' },
]

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
  const groupOrder = useStore((s) => s.groupOrder)
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
      {/* Radios rather than the switch this was, and the same radios as the
          order below it: there are three answers now, and two controls that
          both arrange the list should not disagree about how a choice looks. */}
      <div className="picker-head">Group sessions by</div>
      {GROUP_MODES.map(({ mode, label, hint }) => (
        <button
          key={mode}
          className={grouping === mode ? 'picker-item on' : 'picker-item'}
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            await store.setGrouping(mode)
            setBusy(false)
          }}
        >
          <span className="picker-radio">{grouping === mode ? '●' : '○'}</span>
          <span className="picker-label">{label}</span>
          {/* The mode names cannot carry their own explanation — "Manual
              grouping" says nothing about nesting or filing, and a label long
              enough to say it would not be a label. */}
          <span className="picker-info" title={hint} aria-label={hint}>
            ⓘ
          </span>
        </button>
      ))}

      {grouping === 'manual' && unsupported && (
        <>
          <div className="picker-sep" />
          <div className="picker-note">
            {hostName || 'This machine'} is running a daemon without grouping. Update it to make
            groups here.
          </div>
        </>
      )}

      {/* Auto only, and hidden rather than disabled: the manual tree's order is
          the drag, the way the other manual-only affordances are absent here
          rather than greyed out. */}
      {grouping === 'auto' && (
        <>
          <div className="picker-sep" />
          <div className="picker-head">Order groups by</div>
          {GROUP_ORDERS.map(({ order, label }) => (
            <button
              key={order}
              className={groupOrder === order ? 'picker-item on' : 'picker-item'}
              onClick={() => store.orderGroupsBy(order)}
            >
              <span className="picker-radio">{groupOrder === order ? '●' : '○'}</span> {label}
            </button>
          ))}
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
