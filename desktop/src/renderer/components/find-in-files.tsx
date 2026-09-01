import { useEffect, useRef, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'

import { grepQuery } from '../queries.ts'
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

/** A search the user pressed Enter on. */
interface Submitted {
  q: string
  regex: boolean
  caseSensitive: boolean
}

/** ⌘⇧F: search file contents under the session's directory. */
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
  const field = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    field.current?.focus()
    field.current?.select()
  }, [focusSeq])

  /**
   * The search as it was submitted, which is not the same as what is in the
   * field. Content search walks the whole tree, so it runs on Enter rather than
   * per keystroke — a half-typed word is rarely what was meant.
   *
   * The root is not part of this because it is part of the key: taking a
   * suggestion moves the root under a search already typed, and the answer the
   * user wanted is that same search run there, which now happens by itself.
   */
  const [submitted, setSubmitted] = useState<Submitted | null>(null)

  const { data, error, isFetching, isPlaceholderData } = useQuery({
    ...grepQuery(hostId, root, submitted?.q ?? '', {
      regex: submitted?.regex ?? false,
      caseSensitive: submitted?.caseSensitive ?? false,
      limit: LIMIT,
    }),
    placeholderData: keepPreviousData,
  })

  const matches = submitted ? (data?.matches ?? null) : null
  const truncated = data?.truncated ?? false
  const groups = groupByFile(matches ?? [])

  /**
   * A fetch worth saying anything about, which is not every fetch.
   *
   * An agent writing a file invalidates every read under `files`, this one
   * included, so a background refetch arrives whenever the session is busy.
   * Announcing those emptied the panel and filled it again a moment later,
   * under a reader who had asked for nothing — which is what `keepPreviousData`
   * above was already holding the results to prevent.
   *
   * What is left is the two cases the reader is waiting on: a first search with
   * nothing on screen yet, and a new one running behind the last one's results.
   */
  const searching = isFetching && (matches === null || isPlaceholderData)

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
            if (event.key !== 'Enter') return
            setSubmitted(query ? { q: query, regex, caseSensitive } : null)
          }}
        />
        <button
          className={`find-toggle ${caseSensitive ? 'on' : ''}`}
          title="Match case"
          onClick={() => setCaseSensitive(!caseSensitive)}
        >
          Aa
        </button>
        <button
          className={`find-toggle ${regex ? 'on' : ''}`}
          title="Use regular expression"
          onClick={() => setRegex(!regex)}
        >
          .*
        </button>
      </div>

      <div className="find-results">
        {searching && <p className="empty-note">Searching…</p>}
        {error && <p className="empty-note">{error.message}</p>}
        {!searching && !error && matches !== null && matches.length === 0 && (
          <>
            <p className="empty-note">No results under {root}.</p>
            {onPickRoot && <RootSuggestions root={root} worktrees={worktrees} onPick={onPickRoot} />}
          </>
        )}
        {groups.map(([rel, hits]) => (
          <div key={rel} className="find-group">
            <span className="find-file" title={rel}>
              {rel}
              <span className="find-count">{hits.length}</span>
            </span>
            {hits.map((hit) => (
              <button
                key={`${hit.line}:${hit.column}`}
                className="find-hit"
                onClick={() => onOpen(hit.path, hit.line, hit.column)}
              >
                <span className="find-line">{hit.line}</span>
                <span className="find-text">{hit.text.trim()}</span>
              </button>
            ))}
          </div>
        ))}
        {truncated && matches !== null && matches.length > 0 && (
          <p className="empty-note">Showing the first {matches.length} results.</p>
        )}
      </div>
    </div>
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
