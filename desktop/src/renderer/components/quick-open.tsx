import { useEffect, useMemo, useRef, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'

import { iconKindOf } from '../filetype.ts'
import { fileSearchQuery } from '../queries.ts'
import { FileIcon } from './icons.tsx'
import { parseLineQuery, segmentsAt, type Segment } from './match-marks.ts'
import { HOVER_PREFETCH_MS, useHoverPrefetch } from './prefetch.ts'
import { RootSuggestions } from './root-picker.tsx'
import { baseName } from './tab-strip.ts'
import type { FileMatch, Worktree } from '../../shared/models.ts'

interface Props {
  hostId: string
  root: string
  /** Seeds the field, for callers that already know part of the name. */
  initialQuery?: string
  /** Offered when nothing matches: the file is often in another checkout. */
  worktrees?: Worktree[]
  /** The tabs open now, offered first on an empty query. */
  openTabs?: string[]
  /** This session's recently opened files, newest first, offered after them. */
  recent?: string[]
  onOpen: (path: string, at?: { line: number; column: number }) => void
  /** Fetches a file's body ahead of the click. */
  onPrefetch?: (path: string) => void
  onPickRoot?: (path: string) => void
  onClose: () => void
}

/** Long enough that a fast typist makes one request, short enough to feel live. */
const DEBOUNCE_MS = 90

/** A cold index answers with what it has so far; this is how long to wait before asking again. */
const INDEXING_RETRY_MS = 250
const INDEXING_TRIES = 8

const NO_MATCHES: FileMatch[] = []

/** One row of the list: a file, and the characters of it the query matched. */
interface Row {
  path: string
  rel: string
  positions?: number[]
  /** Why an empty query offers it. */
  note?: 'open' | 'recent'
}

/** ⌘P: type part of a file name, press Enter. `name.go:42` opens at line 42. */
export function QuickOpen({
  hostId,
  root,
  initialQuery = '',
  worktrees = [],
  openTabs = [],
  recent = [],
  onOpen,
  onPrefetch,
  onPickRoot,
  onClose,
}: Props): JSX.Element {
  const [query, setQuery] = useState(initialQuery)
  const [active, setActive] = useState(0)
  const list = useRef<HTMLDivElement | null>(null)
  const hover = useHoverPrefetch(onPrefetch)

  const [needle, setNeedle] = useState(query)
  useEffect(() => {
    const timer = setTimeout(() => setNeedle(query), DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [query])
  // The line comes off before the search: no file is called `main.go:42`.
  const asked = useMemo(() => parseLineQuery(needle), [needle])
  const empty = asked.query.trim() === ''

  // keepPreviousData so the list does not blank between keystrokes: a result
  // set that vanishes and comes back is what makes typing here feel laggy, even
  // when the answer arrives just as fast.
  const { data, error, refetch } = useQuery({
    ...fileSearchQuery(hostId, root, asked.query),
    enabled: !empty && root !== '',
    placeholderData: keepPreviousData,
  })

  // A cold index answers with a partial list and says so. Asking again a beat
  // later fills it in, a bounded number of times, so a root that never warms
  // does not poll for ever.
  const tries = useRef(0)
  useEffect(() => {
    tries.current = 0
  }, [needle, root])
  useEffect(() => {
    if (!data?.indexing || tries.current >= INDEXING_TRIES) return
    const timer = setTimeout(() => {
      tries.current += 1
      void refetch()
    }, INDEXING_RETRY_MS)
    return () => clearTimeout(timer)
  }, [data, refetch])

  // An empty query offers what this session already works with: the open
  // tabs, then what was opened lately. Index order put `.nvmrc` first, which
  // is the file nobody ever wants.
  const rows = useMemo<Row[]>(() => {
    if (!empty) return (data?.matches ?? NO_MATCHES).map((match) => ({ ...match }))
    const seen = new Set<string>()
    const out: Row[] = []
    for (const [paths, note] of [
      [openTabs, 'open'],
      [recent, 'recent'],
    ] as const) {
      for (const path of paths) {
        if (seen.has(path)) continue
        seen.add(path)
        out.push({ path, rel: relativeTo(root, path), note })
      }
    }
    return out
  }, [empty, data, openTabs, recent, root])

  // Where the daemon actually looked. A query naming a directory is searched
  // there, so reporting the panel's root would name the wrong folder.
  const searchedIn = data?.root ?? root
  const fromPath = data?.resolved_from === 'path'

  // Back to the top whenever the question changes; arrowing applies to an
  // answer, and this is a different one.
  useEffect(() => {
    setActive(0)
  }, [needle])

  // Keep the highlighted row on screen while arrowing through a long list,
  // and have its body on the way: Enter is the likely next key.
  const selectedPath = rows[active]?.path
  useEffect(() => {
    list.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest' })
    if (!selectedPath || !onPrefetch) return
    const timer = setTimeout(() => onPrefetch(selectedPath), HOVER_PREFETCH_MS)
    return () => clearTimeout(timer)
  }, [active, selectedPath, onPrefetch])

  const open = (row: Row): void => {
    // The line belongs to what is typed now, not to the debounced search.
    const at = parseLineQuery(query)
    onOpen(row.path, at.line ? { line: at.line, column: at.column ?? 1 } : undefined)
    onClose()
  }

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
      if (row) open(row)
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
          placeholder="Go to file…  (name:line to land on a line)"
          spellCheck={false}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={keys}
        />
        <div className="quick-list" ref={list} onMouseLeave={hover.leave}>
          {error && !empty && <p className="empty-note">{error.message}</p>}
          {rows.map((row, index) => (
            <QuickRow
              key={row.path}
              row={row}
              active={index === active}
              fallbackDir={fromPath ? searchedIn : ''}
              onHover={() => {
                setActive(index)
                hover.enter(row.path)
              }}
              onClick={() => open(row)}
            />
          ))}
          {empty && rows.length === 0 && <p className="empty-note">Type part of a file name.</p>}
          {!empty && !error && data && rows.length === 0 && (
            <>
              <p className="empty-note">
                {data.indexing ? `Still indexing ${searchedIn}…` : `No matching file under ${searchedIn}.`}
                {!fromPath && !data.indexing && ' Paste a full path to reach a file outside it.'}
              </p>
              {onPickRoot && (
                <RootSuggestions
                  root={root}
                  worktrees={worktrees}
                  // Climbing widens the search and cannot hold what the folder
                  // below it did not. Another checkout is the useful direction.
                  offerParent={false}
                  onPick={(path) => {
                    onPickRoot(path)
                    setActive(0)
                  }}
                />
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}

function QuickRow({
  row,
  active,
  fallbackDir,
  onHover,
  onClick,
}: {
  row: Row
  active: boolean
  fallbackDir: string
  onHover: () => void
  onClick: () => void
}): JSX.Element {
  const name = baseName(row.rel)
  const dir = dirOf(row.rel)
  return (
    <button className={`quick-row ${active ? 'active' : ''}`} title={row.path} onMouseEnter={onHover} onClick={onClick}>
      <FileIcon kind={iconKindOf(row.path)} className="quick-icon" />
      <span className="quick-name">
        <Marked segments={segmentsAt(name, row.positions, row.rel.length - name.length)} />
      </span>
      <span className="quick-dir">
        {dir ? <Marked segments={segmentsAt(dir, row.positions, 0)} /> : fallbackDir}
      </span>
      {row.note && <span className="quick-note">{row.note === 'open' ? 'open' : 'recent'}</span>}
    </button>
  )
}

/** Text with its matched characters in bold. */
export function Marked({ segments }: { segments: Segment[] }): JSX.Element {
  return (
    <>
      {segments.map((segment, index) =>
        segment.hit ? (
          <b key={index} className="match">
            {segment.text}
          </b>
        ) : (
          <span key={index}>{segment.text}</span>
        ),
      )}
    </>
  )
}

function dirOf(rel: string): string {
  const cut = rel.lastIndexOf('/')
  return cut < 0 ? '' : rel.slice(0, cut)
}

function relativeTo(root: string, path: string): string {
  return path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path
}
