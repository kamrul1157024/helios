import { useEffect, useState, type ReactNode } from 'react'

/**
 * How long a disclosure takes to open or shut. Matches `.collapsible` in
 * styles.css, and the two have to agree: this is what decides when the closed
 * contents stop being rendered.
 */
export const COLLAPSE_MS = 140

/**
 * A disclosure that moves rather than jumps.
 *
 * The height is animated by the grid trick — `0fr` to `1fr` on a single row —
 * because the alternative is measuring the content, and the content here is a
 * diff, a tree of sessions, or whatever the agent printed. None of those have
 * a height anyone can name in advance.
 *
 * Closed content is unmounted, as it was before this existed: a transcript
 * that rendered every collapsed diff would pay for all of them on every
 * keystroke. The mount is held open for the length of the animation so the
 * shutting rows have something to shrink; without that, closing would be
 * instant and only opening would move.
 */
export function Collapsible({
  open,
  className = '',
  children,
}: {
  open: boolean
  /** Goes on the content element, so the caller keeps its own styling. */
  className?: string
  children: ReactNode
}): JSX.Element | null {
  const [rendered, setRendered] = useState(open)
  // Separate from `rendered` because the first painted frame has to be the
  // shut one: a row that mounts already at `1fr` has nothing to transition
  // from, which is exactly the jump this replaces.
  const [shown, setShown] = useState(open)

  useEffect(() => {
    if (open) {
      setRendered(true)
      const frame = requestAnimationFrame(() => setShown(true))
      return () => cancelAnimationFrame(frame)
    }
    setShown(false)
    const timer = setTimeout(() => setRendered(false), COLLAPSE_MS)
    return () => clearTimeout(timer)
  }, [open])

  if (!rendered) return null
  return (
    <div className={shown ? 'collapsible open' : 'collapsible'}>
      <div className={`collapsible-inner ${className}`.trim()}>{children}</div>
    </div>
  )
}
