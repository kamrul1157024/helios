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

/** A four-pointed spark: the model a session runs on. */
export function Spark({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M12 3.5c0 4.7 3.8 8.5 8.5 8.5-4.7 0-8.5 3.8-8.5 8.5 0-4.7-3.8-8.5-8.5-8.5 4.7 0 8.5-3.8 8.5-8.5z" />
    </svg>
  )
}

/** A shield, for how much the agent may do without asking. */
export function Shield({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M12 3.5l7 2.5v6c0 4-3 7.3-7 8.5-4-1.2-7-4.5-7-8.5V6l7-2.5z" />
    </svg>
  )
}

/** A folder, for a directory the composer offers rather than one it remembers. */
export function Folder({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M3.5 6.5A1.5 1.5 0 015 5h4l2 2.5h8A1.5 1.5 0 0120.5 9v8.5A1.5 1.5 0 0119 19H5a1.5 1.5 0 01-1.5-1.5v-11z" />
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
