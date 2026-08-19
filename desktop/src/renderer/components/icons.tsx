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
