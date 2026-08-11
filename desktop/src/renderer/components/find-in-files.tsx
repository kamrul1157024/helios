import { useEffect, useRef, useState } from 'react'

import { api } from '../bridge.ts'
import type { GrepMatch } from '../../shared/models.ts'

interface Props {
  hostId: string
  root: string
  /** Bumped when ⌘⇧F is pressed again, to put the caret back in the field. */
  focusSeq: number
  onOpen: (path: string, line: number, column: number) => void
}

/** ⌘⇧F: search file contents under the session's directory. */
export function FindInFiles({ hostId, root, focusSeq, onOpen }: Props): JSX.Element {
  const [query, setQuery] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [regex, setRegex] = useState(false)
  const [matches, setMatches] = useState<GrepMatch[] | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [searching, setSearching] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const field = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    field.current?.focus()
    field.current?.select()
  }, [focusSeq])

  // Content search is run on demand rather than per keystroke: it walks the
  // whole tree, and a half-typed word is rarely what was meant.
  const run = async (): Promise<void> => {
    if (!query) {
      setMatches(null)
      return
    }
    setSearching(true)
    setError(null)
    try {
      const result = await api(hostId).grepFiles(root, query, { regex, caseSensitive, limit: 300 })
      setMatches(result.matches)
      setTruncated(result.truncated)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setMatches([])
    } finally {
      setSearching(false)
    }
  }

  const groups = groupByFile(matches ?? [])

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
            if (event.key === 'Enter') void run()
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
        {error && <p className="empty-note">{error}</p>}
        {!searching && !error && matches !== null && matches.length === 0 && (
          <p className="empty-note">No results.</p>
        )}
        {!searching &&
          groups.map(([rel, hits]) => (
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
