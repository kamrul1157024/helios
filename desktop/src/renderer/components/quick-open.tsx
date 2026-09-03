import { useEffect, useRef, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'

import { fileSearchQuery } from '../queries.ts'
import { RootSuggestions } from './root-picker.tsx'
import type { FileMatch, Worktree } from '../../shared/models.ts'

interface Props {
  hostId: string
  root: string
  /** Seeds the field, for callers that already know part of the name. */
  initialQuery?: string
  /** Offered when nothing matches: the file is often in another checkout. */
  worktrees?: Worktree[]
  onOpen: (path: string) => void
  onPickRoot?: (path: string) => void
  onClose: () => void
}

/** Long enough that a fast typist makes one request, short enough to feel live. */
const DEBOUNCE_MS = 90

const NO_MATCHES: FileMatch[] = []

/** ⌘P: type part of a file name, press Enter. */
export function QuickOpen({
  hostId,
  root,
  initialQuery = '',
  worktrees = [],
  onOpen,
  onPickRoot,
  onClose,
}: Props): JSX.Element {
  const [query, setQuery] = useState(initialQuery)
  const [active, setActive] = useState(0)
  const list = useRef<HTMLDivElement | null>(null)

  const [needle, setNeedle] = useState(query)
  useEffect(() => {
    const timer = setTimeout(() => setNeedle(query), DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [query])

  // keepPreviousData so the list does not blank between keystrokes: a result
  // set that vanishes and comes back is what makes typing here feel laggy, even
  // when the answer arrives just as fast.
  const { data, error } = useQuery({
    ...fileSearchQuery(hostId, root, needle),
    placeholderData: keepPreviousData,
  })
  const matches = data?.matches ?? NO_MATCHES
  // Where the daemon actually looked. A query naming a directory is searched
  // there, so reporting the panel's root would name the wrong folder.
  const searchedIn = data?.root ?? root
  const fromPath = data?.resolved_from === 'path'

  // Back to the top whenever the question changes; arrowing applies to an
  // answer, and this is a different one.
  useEffect(() => {
    setActive(0)
  }, [needle])

  // Keep the highlighted row on screen while arrowing through a long list.
  useEffect(() => {
    list.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest' })
  }, [active])

  const keys = (event: React.KeyboardEvent): void => {
    if (event.key === 'ArrowDown' || (event.key === 'n' && event.ctrlKey)) {
      event.preventDefault()
      setActive((i) => Math.min(i + 1, matches.length - 1))
    } else if (event.key === 'ArrowUp' || (event.key === 'p' && event.ctrlKey)) {
      event.preventDefault()
      setActive((i) => Math.max(i - 1, 0))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      const match = matches[active]
      if (match) {
        onOpen(match.path)
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
          placeholder="Go to file…"
          spellCheck={false}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={keys}
        />
        <div className="quick-list" ref={list}>
          {error && <p className="empty-note">{error.message}</p>}
          {matches.map((match, index) => (
            <button
              key={match.path}
              className={`quick-row ${index === active ? 'active' : ''}`}
              onMouseEnter={() => setActive(index)}
              onClick={() => {
                onOpen(match.path)
                onClose()
              }}
            >
              <span className="quick-name">{basename(match.rel)}</span>
              <span className="quick-dir">{dirOf(match.rel) || (fromPath ? searchedIn : '')}</span>
            </button>
          ))}
          {!error && matches.length === 0 && (
            <>
              <p className="empty-note">
                No matching file under {searchedIn}.
                {!fromPath && ' Paste a full path to reach a file outside it.'}
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

function basename(rel: string): string {
  return rel.split('/').pop() ?? rel
}

function dirOf(rel: string): string {
  const cut = rel.lastIndexOf('/')
  return cut < 0 ? '' : rel.slice(0, cut)
}
