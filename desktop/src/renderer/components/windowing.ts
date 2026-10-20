// The arithmetic behind a windowed list: which rows to draw for a scroll
// position, and where to scroll to put a row in view.
//
// Fixed row heights, which is what makes this a few lines of division rather
// than a measuring layer. The tree's rows are all one height by construction.

/** The slice of rows to draw, and the space to leave above and below it. */
export interface WindowSlice {
  start: number
  /** Exclusive. */
  end: number
  padTop: number
  padBottom: number
}

export function visibleSlice(
  scrollTop: number,
  viewport: number,
  rowHeight: number,
  count: number,
  overscan: number,
): WindowSlice {
  if (count === 0 || rowHeight <= 0) return { start: 0, end: 0, padTop: 0, padBottom: 0 }
  const first = Math.floor(Math.max(scrollTop, 0) / rowHeight)
  const visible = Math.ceil(Math.max(viewport, 0) / rowHeight) + 1
  const start = Math.max(0, Math.min(first - overscan, count - 1))
  const end = Math.min(count, first + visible + overscan)
  return { start, end: Math.max(end, start), padTop: start * rowHeight, padBottom: (count - Math.max(end, start)) * rowHeight }
}

/**
 * The scroll offset that puts a row in the middle of the view, clamped to what
 * the list can actually scroll to. This is what "reveal" means.
 */
export function centredOffset(index: number, rowHeight: number, viewport: number, count: number): number {
  const total = count * rowHeight
  const wanted = index * rowHeight - (viewport - rowHeight) / 2
  return Math.round(Math.max(0, Math.min(wanted, total - viewport)))
}

/**
 * The scroll offset that brings a row into view with as little movement as
 * possible, or the current one if it is already there. Arrow keys use this.
 */
export function nearestOffset(index: number, rowHeight: number, viewport: number, scrollTop: number): number {
  const top = index * rowHeight
  if (top < scrollTop) return top
  if (top + rowHeight > scrollTop + viewport) return top + rowHeight - viewport
  return scrollTop
}
