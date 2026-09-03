/**
 * Drawn icons, not typed ones.
 *
 * The disclosure arrows used to be `▸` and `▾` — U+25B8 and U+25BE, whose
 * Unicode names begin with "small". They occupy a fraction of their em box, so
 * every site that used one had to oversize it to read as an arrow at all, and
 * still got a speck. A path scales with its box instead.
 */

/** Which way the chevron points. `open` is the disclosure shorthand for down. */
export type ChevronDir = 'right' | 'down' | 'left' | 'up'

const PATHS: Record<ChevronDir, string> = {
  right: 'M9 5l7 7-7 7',
  down: 'M5 9l7 7 7-7',
  left: 'M15 5l-7 7 7 7',
  up: 'M5 15l7-7 7 7',
}

/**
 * Sized in `em`, so it follows whatever the surrounding row is set to rather
 * than pinning a pixel size the reader cannot change.
 */
export function Chevron({
  open,
  dir,
  className = '',
}: {
  /** Disclosure state: down when open, right when closed. */
  open?: boolean
  /** Explicit direction, for carets that do not disclose anything. */
  dir?: ChevronDir
  className?: string
}): JSX.Element {
  const facing = dir ?? (open ? 'down' : 'right')
  return (
    <svg className={`chevron-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d={PATHS[facing]} />
    </svg>
  )
}

/** A memory module, for the row that prices a host's terminals. */
export function Memory({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`meter-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <rect x="3" y="7" width="18" height="10" rx="1.5" />
      <path d="M7 17v3M12 17v3M17 17v3M7 11v2M12 11v2M17 11v2" />
    </svg>
  )
}

/** A processor, for the load the host is under. */
export function Cpu({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`meter-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <rect x="7" y="7" width="10" height="10" rx="1.5" />
      <path d="M10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4" />
    </svg>
  )
}

/**
 * The sidebar's toolbar and list glyphs.
 *
 * These were typed characters — ⌕ ⇅ ▤ ▦ + — which is why they never lined up:
 * each one is a different fraction of its em box in a different font, so four
 * buttons of the same size held four glyphs of four sizes. A path fills the box
 * it is given, and `ui-icon` gives them all the same one.
 */

/** The magnifier on the search field. */
export function Search({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="11" cy="11" r="6.5" />
      <path d="M16 16l4.5 4.5" />
    </svg>
  )
}

/** Two arrows facing apart: the sort-order toggle. */
export function Sort({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M8 20V4M8 4L4.5 7.5M8 4l3.5 3.5" />
      <path d="M16 4v16M16 20l3.5-3.5M16 20l-3.5-3.5" />
    </svg>
  )
}

/** Three even lines — one line a session. */
export function SingleLine({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M4 6.5h16M4 12h16M4 17.5h16" />
    </svg>
  )
}

/** Two sessions, each a title with a shorter line under it. */
export function MultiLine({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M4 5.5h16M4 9.5h9" />
      <path d="M4 15h16M4 19h9" />
    </svg>
  )
}

/** New session. */
export function Plus({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}

/**
 * A list of sessions, for the rail. Bulleted, so it is not the sort icon.
 *
 * The bullets are circles rather than dotted line caps: a cap thick enough to
 * read as a bullet is nearly twice the weight of every other stroke in the set,
 * and next to the clock beside it that looked like a different icon family.
 */
export function ListRows({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="4.8" cy="6.5" r="1.1" fill="currentColor" stroke="none" />
      <circle cx="4.8" cy="12" r="1.1" fill="currentColor" stroke="none" />
      <circle cx="4.8" cy="17.5" r="1.1" fill="currentColor" stroke="none" />
      <path d="M9.5 6.5h10M9.5 12h10M9.5 17.5h10" />
    </svg>
  )
}

/** A clock, for what runs on one. */
export function Clock({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </svg>
  )
}

/** A gear, for the settings mode. */
export function Gear({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="3.2" />
      <path d="M12 3.2l1.6 2.5 2.9-.6.5 2.9 2.6 1.5-1.6 2.5 1.6 2.5-2.6 1.5-.5 2.9-2.9-.6L12 20.8l-1.6-2.5-2.9.6-.5-2.9L4.4 14.5 6 12 4.4 9.5 7 8l.5-2.9 2.9.6z" />
    </svg>
  )
}

/** A pencil, for the title a row lets you type over. */
export function Pencil({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M4 20h4l10-10-4-4L4 16v4zM14.5 5.5l4 4" />
    </svg>
  )
}

/** A terminal, for the one action a row offers under the pointer. */
export function Console({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
      <path d="M7.5 10l2.5 2.5-2.5 2.5M13 15h3.5" />
    </svg>
  )
}
