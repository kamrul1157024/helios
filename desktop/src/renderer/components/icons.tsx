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
  // A disclosure turns; a caret that merely points does not. Swapping the path
  // for `down` would be a cut rather than a movement, so the open state is the
  // right-facing path rotated, and the rotation is what the stylesheet eases.
  const disclosing = dir === undefined
  const facing = dir ?? 'right'
  const turned = disclosing && open ? ' turned' : ''
  return (
    <svg
      className={`chevron-icon${turned} ${className}`.trim()}
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
    >
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

/** A conversation several sessions are in: two overlapping bubbles. */
export function Chat({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M8.5 15.5H6l-3 2.5v-11a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v4" />
      <path d="M21 19.5l-2.5-2h-6a1.5 1.5 0 0 1-1.5-1.5v-4a1.5 1.5 0 0 1 1.5-1.5h7A1.5 1.5 0 0 1 21 12z" />
    </svg>
  )
}

/** Picking several rows at once: a box with a tick in it. */
export function Ticks({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M4.5 5.5h9M4.5 12h9M4.5 18.5h6" />
      <path d="M15 15.5l2.5 2.5 5-5" />
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

/** A four-pointed spark: the model a session runs on. */
export function Spark({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M12 3.5c0 4.7 3.8 8.5 8.5 8.5-4.7 0-8.5 3.8-8.5 8.5 0-4.7-3.8-8.5-8.5-8.5 4.7 0 8.5-3.8 8.5-8.5z" />
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

/** A shield, for how much the agent may do without asking. */
export function Shield({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M12 3.5l7 2.5v6c0 4-3 7.3-7 8.5-4-1.2-7-4.5-7-8.5V6l7-2.5z" />
    </svg>
  )
}

/** A paperclip: files going with the prompt. */
export function Paperclip({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M18.5 11.5l-6.8 6.8a4 4 0 01-5.7-5.7l7.5-7.5a2.7 2.7 0 013.8 3.8l-7.5 7.5a1.4 1.4 0 01-1.9-1.9l6.6-6.6" />
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

/** Two folders, one behind the other: the tree of groups the user keeps. */
export function FolderStack({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M6.5 7.5V6A1.5 1.5 0 018 4.5h3l1.6 2h6.4A1.5 1.5 0 0120.5 8v1.5" />
      <path d="M3.5 10.5A1.5 1.5 0 015 9h3.5l1.6 2H17a1.5 1.5 0 011.5 1.5V18A1.5 1.5 0 0117 19.5H5A1.5 1.5 0 013.5 18v-7.5z" />
    </svg>
  )
}

/** A folder with the rows inside showing: groups worked out, not made. */
export function FolderLines({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg className={`ui-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M3.5 6.5A1.5 1.5 0 015 5h4l2 2.5h8A1.5 1.5 0 0120.5 9v8.5A1.5 1.5 0 0119 19H5a1.5 1.5 0 01-1.5-1.5v-11z" />
      <path d="M8 12h8M8 15.5h5" />
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

/* ── Provider marks ──────────────────────────────────────────────────────────
   The two agents, as their makers draw them. Filled paths rather than stroked
   ones, so they carry `brand-mark` instead of `ui-icon` — that class sets
   `fill: none`, which would render both as nothing.

   The paths are simple-icons, which is CC0. */

export function AnthropicMark({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg
      className={`brand-mark ${className}`.trim()}
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M17.3041 3.541h-3.6718l6.696 16.918H24Zm-10.6082 0L0 20.459h3.7442l1.3693-3.5527h7.0052l1.3693 3.5528h3.7442L10.5363 3.5409Zm-.3712 10.2232 2.2914-5.9456 2.2914 5.9456Z" />
    </svg>
  )
}

export function OpenAIMark({ className = '' }: { className?: string }): JSX.Element {
  return (
    <svg
      className={`brand-mark ${className}`.trim()}
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z" />
    </svg>
  )
}

/**
 * Which agent is running, from the session's `source`.
 *
 * Anything that is not Codex is Claude: `source` is what the daemon's provider
 * registry stamped on the session, and every provider but one is Anthropic's.
 */
export function ProviderMark({ source, className = '' }: { source: string; className?: string }): JSX.Element {
  const title = source === 'codex' ? 'Codex' : 'Claude Code'
  return (
    <span className="provider-mark" title={title} aria-label={title} role="img">
      {source === 'codex' ? <OpenAIMark className={className} /> : <AnthropicMark className={className} />}
    </span>
  )
}
