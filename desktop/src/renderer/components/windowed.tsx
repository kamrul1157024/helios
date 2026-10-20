import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react'

import { centredOffset, nearestOffset, visibleSlice, type WindowSlice } from './windowing.ts'

/**
 * Which rows of a fixed-height list the scroller can see, plus the overscan.
 *
 * Followed through the scroller's own scroll and resize, batched to one update
 * a frame: a wheel fires far more scroll events than the screen has frames.
 */
export function useScrollWindow(
  scroller: RefObject<HTMLElement | null>,
  count: number,
  rowHeight: number,
  overscan: number,
): { slice: WindowSlice; scrollToIndex: (index: number, how: 'center' | 'nearest') => void } {
  const [view, setView] = useState({ top: 0, height: 0 })

  useLayoutEffect(() => {
    const element = scroller.current
    if (!element) return
    let frame = 0
    const read = (): void => {
      frame = 0
      setView((current) =>
        current.top === element.scrollTop && current.height === element.clientHeight
          ? current
          : { top: element.scrollTop, height: element.clientHeight },
      )
    }
    const schedule = (): void => {
      if (frame === 0) frame = requestAnimationFrame(read)
    }
    read()
    element.addEventListener('scroll', schedule, { passive: true })
    const resize = new ResizeObserver(schedule)
    resize.observe(element)
    return () => {
      element.removeEventListener('scroll', schedule)
      resize.disconnect()
      if (frame !== 0) cancelAnimationFrame(frame)
    }
  }, [scroller])

  const scrollToIndex = useCallback(
    (index: number, how: 'center' | 'nearest') => {
      const element = scroller.current
      if (!element || index < 0) return
      const height = element.clientHeight
      element.scrollTop =
        how === 'center'
          ? centredOffset(index, rowHeight, height, count)
          : nearestOffset(index, rowHeight, height, element.scrollTop)
    },
    [scroller, rowHeight, count],
  )

  return { slice: visibleSlice(view.top, view.height, rowHeight, count, overscan), scrollToIndex }
}

/** Rows a chunk holds. Big enough that a chunk boundary is rarely on screen. */
const CHUNK = 100

/** Below this many rows everything is drawn: windowing has nothing to save. */
const WINDOW_FROM = CHUNK * 3

/**
 * A long list of rows whose heights are not all the same, drawn a chunk at a
 * time (spec 65, 2.6).
 *
 * A wrapped diff line is two or three rows tall, so the fixed-height window the
 * tree uses would put the scrollbar in the wrong place. Instead the rows go in
 * chunks, a chunk is drawn while it is near the view, and one that is not keeps
 * its place with a spacer of the height it last measured — or, before it has
 * ever been drawn, an estimate from the chunks that have.
 *
 * `pinned` is a row that must be drawn whatever the scroll position: the line a
 * diff was asked to show, which has to exist before it can be scrolled to.
 */
export function ChunkedRows({
  count,
  render,
  pinned,
}: {
  count: number
  render: (index: number) => ReactNode
  pinned?: number
}): JSX.Element {
  const box = useRef<HTMLDivElement | null>(null)
  const [root, setRoot] = useState<Element | null>(null)
  const perRow = useRef(20)

  useLayoutEffect(() => setRoot(scrollParent(box.current)), [])

  if (count < WINDOW_FROM) {
    return <div ref={box}>{Array.from({ length: count }, (_, index) => render(index))}</div>
  }
  const chunks = Math.ceil(count / CHUNK)
  return (
    <div ref={box}>
      {Array.from({ length: chunks }, (_, chunk) => {
        const from = chunk * CHUNK
        const to = Math.min(count, from + CHUNK)
        return (
          <Chunk
            key={chunk}
            root={root}
            rows={to - from}
            perRow={perRow}
            forced={pinned !== undefined && pinned >= from && pinned < to}
            // The first screen is drawn before any observer has reported, so
            // the top of the list never flashes empty.
            initiallyShown={chunk < 2}
          >
            {() => Array.from({ length: to - from }, (_, at) => render(from + at))}
          </Chunk>
        )
      })}
    </div>
  )
}

function Chunk({
  root,
  rows,
  perRow,
  forced,
  initiallyShown,
  children,
}: {
  root: Element | null
  rows: number
  perRow: React.MutableRefObject<number>
  forced: boolean
  initiallyShown: boolean
  children: () => ReactNode
}): JSX.Element {
  const box = useRef<HTMLDivElement | null>(null)
  const [near, setNear] = useState(initiallyShown)
  const measured = useRef<number | null>(null)

  useEffect(() => {
    const element = box.current
    if (!element) return
    const watch = new IntersectionObserver(([entry]) => setNear(Boolean(entry?.isIntersecting)), {
      root,
      rootMargin: '600px 0px',
    })
    watch.observe(element)
    return () => watch.disconnect()
  }, [root])

  const shown = near || forced
  useLayoutEffect(() => {
    if (!shown || !box.current) return
    const height = box.current.getBoundingClientRect().height
    if (height <= 0) return
    measured.current = height
    perRow.current = height / rows
  })

  return (
    <div ref={box} style={shown ? undefined : { height: measured.current ?? perRow.current * rows }}>
      {shown ? children() : null}
    </div>
  )
}

/** The nearest ancestor that scrolls vertically, or null for the viewport. */
function scrollParent(element: Element | null): Element | null {
  for (let at = element?.parentElement ?? null; at; at = at.parentElement) {
    const overflow = getComputedStyle(at).overflowY
    if (overflow === 'auto' || overflow === 'scroll') return at
  }
  return null
}
