import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react'

/** One entry of a popover: what it says, and what it does. */
export interface MenuAction {
  label: string
  /** Absent on a row that only opens `children`. */
  run?: () => void
  /** Destructive, and coloured to say so before it is clicked. */
  danger?: boolean
  /** Hovered explanation, for an action whose reach is wider than its label —
   *  and for one that does not stop to ask before it acts. */
  title?: string
  /** Shown, but refusing. The title is where the reason goes. */
  disabled?: boolean
  /**
   * A second menu, opened from this row.
   *
   * The row keeps its height whether or not the children have arrived, which is
   * the point: a list fetched when the menu opens would otherwise grow it under
   * the pointer.
   */
  children?: MenuAction[]
}

/**
 * A small popover at a point on screen.
 *
 * Mousedown inside it is swallowed: the browser would otherwise collapse the
 * selection the menu is about, and unmounting on that mousedown would take the
 * button out of the document before its own click could land.
 */
export function SelectionMenu({
  x,
  y,
  anchor = 'point',
  actions,
  onClose,
}: {
  x: number
  y: number
  /** 'point' sits below-right of the pointer; 'above' floats over a selection. */
  anchor?: 'point' | 'above'
  actions: MenuAction[]
  onClose: () => void
}): JSX.Element {
  const box = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const dismiss = (event: Event): void => {
      if (event instanceof KeyboardEvent && event.key !== 'Escape') return
      if (event.type === 'mousedown' && event.target instanceof Node && box.current?.contains(event.target)) return
      onClose()
    }
    window.addEventListener('mousedown', dismiss)
    window.addEventListener('keydown', dismiss)
    return () => {
      window.removeEventListener('mousedown', dismiss)
      window.removeEventListener('keydown', dismiss)
    }
  }, [onClose])

  // Centred on the selection, so a menu near either edge would hang off it.
  const left = anchor === 'above' ? Math.min(Math.max(x, 110), window.innerWidth - 110) : x
  const [shifted, setShifted] = useState<{ left: number; top: number } | null>(null)

  // A menu opened near the bottom of the window would otherwise run off it —
  // and the last item, which is where the destructive one goes, is the part
  // that disappears. Measured rather than estimated, because the height is the
  // number of actions the caller passed. Before paint, so it does not jump.
  useLayoutEffect(() => {
    if (anchor !== 'point') return
    const rect = box.current?.getBoundingClientRect()
    if (!rect) return
    setShifted({
      left: Math.max(8, Math.min(x, window.innerWidth - rect.width - 8)),
      top: Math.max(8, Math.min(y, window.innerHeight - rect.height - 8)),
    })
  }, [anchor, x, y, actions.length])

  return (
    <div
      className={`line-menu${anchor === 'above' ? ' floating' : ''}`}
      ref={box}
      style={shifted ?? { left, top: y }}
      onMouseDown={(event) => event.preventDefault()}
    >
      {actions.map((action) =>
        action.children ? (
          <SubMenu key={action.label} action={action} onClose={onClose} />
        ) : (
          <button
            key={action.label}
            className={action.danger ? 'danger' : undefined}
            title={action.title}
            disabled={action.disabled}
            onClick={() => {
              action.run?.()
              onClose()
            }}
          >
            {action.label}
          </button>
        ),
      )}
    </div>
  )
}

/** How long the pointer may be off both the row and its children before it shuts. */
const SUBMENU_GRACE = 120

/**
 * A row that opens a second menu beside itself.
 *
 * The child is a descendant of the parent's box rather than a portal, so the
 * dismiss handler above — which ignores mousedown inside that box — needs no
 * case for it.
 */
function SubMenu({ action, onClose }: { action: MenuAction; onClose: () => void }): JSX.Element {
  const [open, setOpen] = useState(false)
  const [flip, setFlip] = useState(false)
  const row = useRef<HTMLDivElement | null>(null)
  const child = useRef<HTMLDivElement | null>(null)
  const closing = useRef<number | undefined>(undefined)

  // Delayed, so the pointer can cross the gap between the row and the child.
  const hold = (): void => window.clearTimeout(closing.current)
  const release = (): void => {
    closing.current = window.setTimeout(() => setOpen(false), SUBMENU_GRACE)
  }

  useEffect(() => () => window.clearTimeout(closing.current), [])

  // Opens to the left instead when there is no room on the right. Measured
  // before paint for the same reason the parent menu measures itself: a menu
  // that jumps has already been read in the wrong place.
  useLayoutEffect(() => {
    if (!open) return
    const at = row.current?.getBoundingClientRect()
    const box = child.current?.getBoundingClientRect()
    if (!at || !box) return
    setFlip(at.right + box.width > window.innerWidth - 8)
  }, [open, action.children?.length])

  return (
    <div className="line-sub" ref={row} onMouseEnter={() => { hold(); setOpen(true) }} onMouseLeave={release}>
      <button
        className={action.danger ? 'danger' : undefined}
        title={action.title}
        disabled={action.disabled}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {action.label}
        <span className="line-sub-mark">›</span>
      </button>

      {open && (
        <div
          className={`line-menu line-sub-menu${flip ? ' flip' : ''}`}
          ref={child}
          onMouseEnter={hold}
          onMouseLeave={release}
        >
          {action.children?.map((entry) => (
            <button
              key={entry.label}
              className={entry.danger ? 'danger' : undefined}
              title={entry.title}
              disabled={entry.disabled}
              onClick={() => {
                entry.run?.()
                onClose()
              }}
            >
              {entry.label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** A live text selection, and where a menu about it belongs. */
export interface TextSelection {
  /** The middle of the selection, and its top edge, in viewport coordinates. */
  x: number
  y: number
  text: string
  range: Range
}

/**
 * The text selected inside an element, tracked for as long as it stands.
 *
 * Read on mouseup rather than on selectionchange: a menu that follows the
 * pointer through a drag lands under it as often as beside it.
 */
export function useTextSelection(
  container: RefObject<HTMLElement | null>,
): [TextSelection | null, () => void] {
  const [selected, setSelected] = useState<TextSelection | null>(null)

  useEffect(() => {
    const read = (): void => {
      const host = container.current
      const selection = window.getSelection()
      if (!host || !selection || selection.isCollapsed || selection.rangeCount === 0) {
        setSelected(null)
        return
      }
      const range = selection.getRangeAt(0)
      if (!host.contains(range.commonAncestorContainer)) {
        setSelected(null)
        return
      }
      const rect = range.getBoundingClientRect()
      setSelected({
        x: rect.left + rect.width / 2,
        y: rect.top,
        text: range.toString(),
        range: range.cloneRange(),
      })
    }

    // Scrolling moves the selection out from under a menu pinned to the
    // viewport, so the rectangle is measured again rather than dropped.
    window.addEventListener('mouseup', read)
    window.addEventListener('keyup', read)
    window.addEventListener('scroll', read, true)
    return () => {
      window.removeEventListener('mouseup', read)
      window.removeEventListener('keyup', read)
      window.removeEventListener('scroll', read, true)
    }
  }, [container])

  // Dismissing takes the selection with it. Leaving it standing would bring the
  // menu straight back, since the next mouseup or keyup reads it again.
  const clear = useCallback((): void => {
    window.getSelection()?.removeAllRanges()
    setSelected(null)
  }, [])

  return [selected, clear]
}
