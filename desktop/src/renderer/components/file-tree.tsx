import { useEffect, useMemo, useRef } from 'react'
import { useQueries } from '@tanstack/react-query'

import { dirQuery } from '../queries.ts'
import type { FileEntry } from '../../shared/models.ts'
import { Chevron } from './icons.tsx'

interface Props {
  hostId: string
  root: string
  /** The open file, highlighted in the tree. */
  selected: string | null
  /** A file to scroll to and expand the ancestors of, e.g. from quick open. */
  reveal: string | null
  /**
   * Directories to show open. Owned by the panel because it is remembered per
   * session, and a root is not a session: two sessions in one repository share
   * one.
   */
  expanded: Set<string>
  onExpandedChange: (next: Set<string>) => void
  onOpen: (path: string) => void
}

/**
 * A lazily loaded directory tree.
 *
 * Directories are listed the first time they are expanded and then kept — an
 * agent's repository is big enough that reading it all up front would stall the
 * panel, and small enough that holding what was opened costs nothing.
 */
export function FileTree({
  hostId,
  root,
  selected,
  reveal,
  expanded,
  onExpandedChange,
  onOpen,
}: Props): JSX.Element {
  // Read by the reveal effect, which adds to what is already open without
  // wanting to re-run every time that changes.
  const expandedRef = useRef(expanded)
  expandedRef.current = expanded

  /**
   * Every directory that has to be listed: the root, and whatever is open
   * under it. Wherever the instruction came from — a restore for this session,
   * a reveal, or a click — it lands here, and one query per directory is what
   * keeps a slow one from being asked for twice.
   */
  const dirs = useMemo(() => {
    const wanted = new Set<string>([root])
    for (const dir of expanded) if (dir.startsWith(root)) wanted.add(dir)
    return [...wanted]
  }, [root, expanded])

  const listings = useQueries({ queries: dirs.map((dir) => dirQuery(hostId, dir)) })

  const children: Record<string, FileEntry[]> = {}
  const busy = new Set<string>()
  dirs.forEach((dir, index) => {
    const listing = listings[index]
    if (!listing) return
    if (listing.data) children[dir] = listing.data.entries
    if (listing.isFetching) busy.add(dir)
  })
  const error = listings.find((listing) => listing.error)?.error ?? null

  const toggle = (dir: string): void => {
    const next = new Set(expanded)
    if (next.has(dir)) next.delete(dir)
    else next.add(dir)
    onExpandedChange(next)
  }

  // Opening a file from quick open or the transcript should show where it
  // lives, which means listing every directory between it and the root.
  useEffect(() => {
    if (!reveal || !reveal.startsWith(root)) return
    const parts = reveal.slice(root.length).split('/').filter(Boolean)
    parts.pop()
    const next = new Set(expandedRef.current)
    let dir = root
    for (const part of parts) {
      dir = `${dir}/${part}`
      next.add(dir)
    }
    onExpandedChange(next)
  }, [reveal, root, onExpandedChange])

  const rows: JSX.Element[] = []
  const push = (dir: string, depth: number): void => {
    for (const entry of children[dir] ?? []) {
      const open = expanded.has(entry.path)
      rows.push(
        <button
          key={entry.path}
          className={`tree-row ${selected === entry.path ? 'selected' : ''}`}
          style={{ paddingLeft: 8 + depth * 12 }}
          title={entry.name}
          onClick={() => (entry.is_dir ? toggle(entry.path) : onOpen(entry.path))}
        >
          <span className="tree-twist">{entry.is_dir && <Chevron open={open} />}</span>
          <span className={`tree-name ${entry.is_dir ? 'dir' : ''}`}>{entry.name}</span>
          {busy.has(entry.path) && <span className="tree-busy">…</span>}
        </button>,
      )
      if (entry.is_dir && open) push(entry.path, depth + 1)
    }
  }
  push(root, 0)

  return (
    <div className="tree">
      {error && <p className="empty-note">{error.message}</p>}
      {rows}
      {rows.length === 0 && !error && !busy.has(root) && <p className="empty-note">Empty directory.</p>}
    </div>
  )
}
