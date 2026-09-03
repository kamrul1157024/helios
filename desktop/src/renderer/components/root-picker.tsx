import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { dirQuery } from '../queries.ts'
import { matchesWorktree, timeAgo, type FileEntry, type Worktree } from '../../shared/models.ts'

interface Props {
  hostId: string
  /** The folder the tree is rooted at now. */
  root: string
  worktrees: Worktree[]
  onPick: (path: string) => void
  onClose: () => void
}

interface Row {
  path: string
  label: string
  note: string
}

/** Long enough that a fast typist makes one request, short enough to feel live. */
const DEBOUNCE_MS = 90

/** Module scope so the cache memoises the filtered list rather than handing
 *  back a fresh array on every keystroke. */
const onlyDirs = (listing: { entries: FileEntry[] }): FileEntry[] =>
  listing.entries.filter((entry) => entry.is_dir)
const NO_DIRS: FileEntry[] = []

/**
 * Chooses what the Files panel is rooted at: one of the repository's worktrees,
 * or any directory on the host. Typing a path browses it — an agent's work is
 * not always inside a worktree, and neither is what the user wants to read.
 */
export function RootPicker({ hostId, root, worktrees, onPick, onClose }: Props): JSX.Element {
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const list = useRef<HTMLDivElement | null>(null)

  // An absolute query is a path being typed: list its parent so the folders
  // under it complete. Anything else filters what is already in view. The
  // daemon expands the tilde, so it counts as absolute here.
  const typedPath = query.startsWith('/') || query.startsWith('~/')
  const browseDir = typedPath ? parentOf(query) : root
  const needle = (typedPath ? basename(query) : query).toLowerCase()

  // The parent only changes when a separator is typed, so this holds back the
  // burst from someone pasting or typing a deep path quickly rather than the
  // keystrokes within one segment.
  const [settled, setSettled] = useState(browseDir)
  useEffect(() => {
    const timer = setTimeout(() => setSettled(browseDir), DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [browseDir])

  // A directory that cannot be listed is an empty one here: the field is being
  // typed into, and half a path naming nothing is the normal case.
  const { data: dirs = NO_DIRS } = useQuery({ ...dirQuery(hostId, settled), select: onlyDirs })

  const rows = useMemo<Row[]>(() => {
    const collected: Row[] = []
    const seen = new Set<string>()
    const add = (row: Row): void => {
      if (row.path === root || seen.has(row.path)) return
      seen.add(row.path)
      collected.push(row)
    }

    if (!typedPath) {
      for (const worktree of worktrees) {
        if (!matchesWorktree(worktree, query)) continue
        add({
          path: worktree.path,
          label: worktree.branch || '(detached)',
          note: worktree.date ? `${tail(worktree.path)} · ${timeAgo(worktree.date)}` : tail(worktree.path),
        })
      }
      const parent = parentOf(root)
      if (parent !== root && basename(parent).toLowerCase().includes(needle)) {
        add({ path: parent, label: '..', note: parent })
      }
    }

    for (const entry of dirs) {
      if (!entry.name.toLowerCase().includes(needle)) continue
      add({ path: entry.path, label: entry.name, note: browseDir })
    }

    // A path that exists but is not listed — a sibling of the browse directory,
    // or one typed in full — is still a directory the user meant.
    if (typedPath && !seen.has(query)) {
      collected.push({ path: query, label: query, note: 'Use this path' })
    }
    return collected
  }, [worktrees, dirs, query, needle, typedPath, browseDir, root])

  useEffect(() => {
    setActive(0)
  }, [query])

  useEffect(() => {
    list.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest' })
  }, [active])

  const keys = (event: React.KeyboardEvent): void => {
    if (event.key === 'ArrowDown' || (event.key === 'n' && event.ctrlKey)) {
      event.preventDefault()
      setActive((i) => Math.min(i + 1, rows.length - 1))
    } else if (event.key === 'ArrowUp' || (event.key === 'p' && event.ctrlKey)) {
      event.preventDefault()
      setActive((i) => Math.max(i - 1, 0))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      const row = rows[active]
      if (row) {
        onPick(row.path)
        onClose()
      }
    } else if (event.key === 'Escape') {
      event.preventDefault()
      onClose()
    }
  }

  return (
    <div className="quick-backdrop" onMouseDown={onClose}>
      <div className="quick" onMouseDown={(event) => event.stopPropagation()}>
        <input
          autoFocus
          className="quick-input"
          placeholder="Search worktrees, or type a folder path…"
          spellCheck={false}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={keys}
        />
        <div className="quick-list" ref={list}>
          {rows.map((row, index) => (
            <button
              key={row.path}
              className={`quick-row ${index === active ? 'active' : ''}`}
              onMouseEnter={() => setActive(index)}
              onClick={() => {
                onPick(row.path)
                onClose()
              }}
            >
              <span className="quick-name">{row.label}</span>
              <span className="quick-dir">{row.note}</span>
            </button>
          ))}
          {rows.length === 0 && <p className="empty-note">No folder matches.</p>}
        </div>
      </div>
    </div>
  )
}

/**
 * Where else the thing being looked for might be. A search that found nothing
 * is usually pointed one directory too deep, or at the wrong checkout of a
 * repository several agents are working in at once.
 */
export function RootSuggestions({
  root,
  worktrees,
  offerParent = true,
  onPick,
}: {
  root: string
  worktrees: Worktree[]
  /** File search sets this false: see the call site for why climbing is wrong. */
  offerParent?: boolean
  onPick: (path: string) => void
}): JSX.Element | null {
  const parent = offerParent ? parentOf(root) : root
  const elsewhere = worktrees.filter((worktree) => worktree.path !== root).slice(0, SUGGESTED_WORKTREES)
  if (parent === root && elsewhere.length === 0) return null

  return (
    <div className="root-hints">
      <span className="root-hint-label">Search in</span>
      {parent !== root && (
        <button className="pill" title={parent} onClick={() => onPick(parent)}>
          ↑ {basename(parent)}
        </button>
      )}
      {elsewhere.map((worktree) => (
        <button key={worktree.path} className="pill" title={worktree.path} onClick={() => onPick(worktree.path)}>
          {worktree.branch || tail(worktree.path)}
        </button>
      ))}
    </div>
  )
}

/** Enough to cover the agents in flight without becoming a second list. */
const SUGGESTED_WORKTREES = 4

function parentOf(path: string): string {
  const cut = path.replace(/\/+$/, '').lastIndexOf('/')
  return cut <= 0 ? '/' : path.slice(0, cut)
}

function basename(path: string): string {
  return path.split('/').filter(Boolean).pop() ?? path
}

/** The last two segments: the parent directory is what distinguishes them. */
function tail(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length <= 2 ? path : `…/${parts.slice(-2).join('/')}`
}
