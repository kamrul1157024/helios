import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'

/** One entry of a popover: what it says, and what it does. */
export interface MenuAction {
  label: string
  run: () => void
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

  return (
    <div
      className={`line-menu${anchor === 'above' ? ' floating' : ''}`}
      ref={box}
      style={{ left, top: y }}
      onMouseDown={(event) => event.preventDefault()}
    >
      {actions.map((action) => (
        <button
          key={action.label}
          onClick={() => {
            action.run()
            onClose()
          }}
        >
          {action.label}
        </button>
      ))}
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
