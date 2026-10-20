import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'

import { bridge } from '../bridge.ts'
import { iconKindOf } from '../filetype.ts'
import { changedPaths } from '../keys.ts'
import { grepQuery } from '../queries.ts'
import { Chevron, FileIcon } from './icons.tsx'
import { segmentsIn, snippet } from './match-marks.ts'
import { Marked } from './quick-open.tsx'
import { RootSuggestions } from './root-picker.tsx'
import type { GrepMatch, Worktree } from '../../shared/models.ts'

interface Props {
  hostId: string
  root: string
  /** Bumped when ⌘⇧F is pressed again, to put the caret back in the field. */
  focusSeq: number
  /** Offered when nothing matches: the text is often in another checkout. */
  worktrees?: Worktree[]
  onOpen: (path: string, line: number, column: number) => void
  onPickRoot?: (path: string) => void
}

/** Past this the daemon stops walking and says it truncated. */
const LIMIT = 300

/** How long typing has to pause before the search runs (spec 65, B4). */
const TYPING_PAUSE_MS = 200

/** Shorter than this is a search that matches everything and helps nobody. */
const MIN_CHARS = 2

/** A search, with everything that decides its answer. */
interface Search {
  q: string
  regex: boolean
  caseSensitive: boolean
  word: boolean
  glob: string
}

/**
 * ⌘⇧F: search file contents under the session's directory.
 *
 * It runs as the user types, once they pause. Each new search aborts the one
 * before it in the main process, and the daemon stops ripgrep with it, so a
 * fast typist costs one search rather than one per letter.
 *
 * Stays mounted while the explorer shows something else, so the query, the
 * toggles and the results are where they were left (F5).
 */
export function FindInFiles({
  hostId,
  root,
  focusSeq,
  worktrees = [],
  onOpen,
  onPickRoot,
}: Props): JSX.Element {
  const [query, setQuery] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [regex, setRegex] = useState(false)
  const [word, setWord] = useState(false)
  const [glob, setGlob] = useState('')
  const [showGlob, setShowGlob] = useState(false)
  const field = useRef<HTMLInputElement | null>(null)
  const results = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    field.current?.focus()
    field.current?.select()
  }, [focusSeq])

  /**
   * The search that is running, which is not always what is in the field: it
   * trails the typing by a pause.
   *
   * The root is not part of this because it is part of the key: taking a
   * suggestion moves the root under a search already typed, and the answer the
   * user wanted is that same search run there, which now happens by itself.
   */
  const [search, setSearch] = useState<Search | null>(null)
  const current: Search = { q: query, regex, caseSensitive, word, glob }
  const latest = useRef(current)
  latest.current = current

  const run = useCallback((next: Search, force = false): void => {
    if (!next.q || (!force && next.q.length < MIN_CHARS)) {
      setSearch(null)
      return
    }
    setSearch(next)
  }, [])

  // As you type, after a pause.
  useEffect(() => {
    const timer = window.setTimeout(() => run(latest.current), TYPING_PAUSE_MS)
    return () => window.clearTimeout(timer)
  }, [query, glob, run])

  // A toggle reruns the search at once (F4). It used to light up and change
  // nothing until the next Enter, which read as the toggle being broken.
  const toggle = (patch: Partial<Search>): void => {
    if (patch.caseSensitive !== undefined) setCaseSensitive(patch.caseSensitive)
    if (patch.regex !== undefined) setRegex(patch.regex)
    if (patch.word !== undefined) setWord(patch.word)
    if (search) run({ ...current, ...patch }, true)
  }

  const { data, error, isFetching, isPlaceholderData, refetch } = useQuery({
    ...grepQuery(hostId, root, search?.q ?? '', {
      regex: search?.regex ?? false,
      caseSensitive: search?.caseSensitive ?? false,
      word: search?.word || undefined,
      glob: search?.glob.trim() || undefined,
      limit: LIMIT,
    }),
    placeholderData: keepPreviousData,
  })

  const matches = search ? (data?.matches ?? null) : null
  const truncated = data?.truncated ?? false
  const groups = useMemo(() => groupByFile(matches ?? []), [matches])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  /**
   * Whether a file has changed under results already on screen.
   *
   * An agent write no longer reruns the search (spec 65, 2.1): a full ripgrep
   * per write was the most expensive thing a busy session did, and results
   * that shuffle under the reader are worse than results that say they may be
   * old. Cleared by any new answer.
   */
  const [stale, setStale] = useState(false)
  useEffect(() => setStale(false), [data])
  useEffect(
    () =>
      bridge.hosts.onEvent(({ hostId: from, event }) => {
        if (from !== hostId || event.type !== 'file_changed') return
        if (changedPaths(event.data.paths).some(({ path }) => path === root || path.startsWith(`${root}/`))) {
          setStale(true)
        }
      }),
    [hostId, root],
  )

  /**
   * A fetch worth saying anything about, which is not every fetch: a first
   * search with nothing on screen yet, and a new one running behind the last
   * one's results. `keepPreviousData` above holds the old results meanwhile.
   */
  const searching = isFetching && (matches === null || isPlaceholderData)

  // The hits in reading order, skipping folded groups: what the arrow keys
  // walk through.
  const visibleHits = useMemo(
    () => groups.flatMap(([rel, hits]) => (collapsed.has(rel) ? [] : hits)),
    [groups, collapsed],
  )
  const [cursor, setCursor] = useState(-1)
  useEffect(() => setCursor(-1), [matches])
  useEffect(() => {
    results.current?.querySelector('.find-hit.active')?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  const step = (by: number): void => {
    if (visibleHits.length === 0) return
    setCursor((at) => Math.max(0, Math.min(at + by, visibleHits.length - 1)))
  }

  const navigate = (event: React.KeyboardEvent): boolean => {
    if (event.key === 'ArrowDown') step(1)
    else if (event.key === 'ArrowUp') step(-1)
    else if (event.key === 'Enter' && cursor >= 0) {
      const hit = visibleHits[cursor]
      if (hit) onOpen(hit.path, hit.line, hit.column)
    } else return false
    event.preventDefault()
    return true
  }

  const fileCount = groups.length
  const count = matches?.length ?? 0

  return (
    <div className="find">
      <div className="find-head">
        <input
          ref={field}
          className="find-input"
          placeholder="Search"
          spellCheck={false}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (navigate(event)) return
            // Enter runs it now, whatever its length: the pause and the minimum
            // are for typing, not for someone who asked.
            if (event.key === 'Enter') run(current, true)
          }}
        />
        <button
          className={`find-toggle ${caseSensitive ? 'on' : ''}`}
          title="Match case"
          aria-label="Match case"
          aria-pressed={caseSensitive}
          onClick={() => toggle({ caseSensitive: !caseSensitive })}
        >
          Aa
        </button>
        <button
          className={`find-toggle ${word ? 'on' : ''}`}
          title="Match whole word"
          aria-label="Match whole word"
          aria-pressed={word}
          onClick={() => toggle({ word: !word })}
        >
          <span className="find-word">ab</span>
        </button>
        <button
          className={`find-toggle ${regex ? 'on' : ''}`}
          title="Use regular expression"
          aria-label="Use regular expression"
          aria-pressed={regex}
          onClick={() => toggle({ regex: !regex })}
        >
          .*
        </button>
        <button
          className={`find-toggle ${showGlob || glob ? 'on' : ''}`}
          title="Files to include or exclude"
          aria-label="Files to include or exclude"
          aria-expanded={showGlob}
          onClick={() => setShowGlob(!showGlob)}
        >
          …
        </button>
      </div>
      {showGlob && (
        <div className="find-head find-glob">
          <input
            className="find-input"
            placeholder="Files: *.go, !*_test.go"
            spellCheck={false}
            value={glob}
            onChange={(event) => setGlob(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') run(current, true)
            }}
          />
        </div>
      )}

      {matches !== null && count > 0 && (
        <div className="find-summary">
          <span>
            {count} {count === 1 ? 'result' : 'results'} in {fileCount} {fileCount === 1 ? 'file' : 'files'}
            {truncated && ' (first results only)'}
          </span>
          {searching && <span className="spinner tiny" />}
        </div>
      )}
      {stale && matches !== null && (
        <div className="find-stale" role="status">
          <span>Results may be out of date</span>
          <span aria-hidden="true">·</span>
          <button
            className="find-again"
            onClick={() => {
              setStale(false)
              void refetch()
            }}
          >
            Search again
          </button>
        </div>
      )}

      <div className="find-results" ref={results} tabIndex={-1} onKeyDown={navigate}>
        {searching && matches === null && (
          <div className="find-searching">
            <span className="spinner tiny" />
          </div>
        )}
        {error && <p className="empty-note">{error.message}</p>}
        {!searching && !error && matches !== null && matches.length === 0 && (
          <>
            <p className="empty-note">No results under {root}.</p>
            {onPickRoot && <RootSuggestions root={root} worktrees={worktrees} onPick={onPickRoot} />}
          </>
        )}
        {groups.map(([rel, hits]) => {
          const folded = collapsed.has(rel)
          return (
            <div key={rel} className="find-group">
              <button
                className="find-file"
                title={rel}
                aria-expanded={!folded}
                onClick={() =>
                  setCollapsed((current) => {
                    const next = new Set(current)
                    if (!next.delete(rel)) next.add(rel)
                    return next
                  })
                }
              >
                <Chevron open={!folded} />
                <FileIcon kind={iconKindOf(rel)} className="find-icon" />
                <span className="find-path">{rel}</span>
                <span className="find-count">{hits.length}</span>
              </button>
              {!folded &&
                hits.map((hit) => {
                  const index = visibleHits.indexOf(hit)
                  return (
                    <Hit
                      key={`${hit.line}:${hit.column}`}
                      hit={hit}
                      active={index === cursor}
                      onClick={() => {
                        setCursor(index)
                        onOpen(hit.path, hit.line, hit.column)
                      }}
                    />
                  )
                })}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function Hit({ hit, active, onClick }: { hit: GrepMatch; active: boolean; onClick: () => void }): JSX.Element {
  // The snippet starts just before the first match, so a hit at column 200 of
  // a long line is not cut off at the right edge before it is reached.
  const cut = snippet(hit.text, hit.ranges)
  return (
    <button className={`find-hit ${active ? 'active' : ''}`} onClick={onClick}>
      <span className="find-line">{hit.line}</span>
      <span className="find-text">
        {cut.clipped && '…'}
        <Marked segments={segmentsIn(cut.text, cut.ranges)} />
      </span>
    </button>
  )
}

/** Hits arrive file by file already; this only keeps them that way. */
function groupByFile(matches: GrepMatch[]): [string, GrepMatch[]][] {
  const groups: [string, GrepMatch[]][] = []
  for (const match of matches) {
    const last = groups[groups.length - 1]
    if (last && last[0] === match.rel) last[1].push(match)
    else groups.push([match.rel, [match]])
  }
  return groups
}
