import { useEffect, useRef, useState } from 'react'

import { api } from '../bridge.ts'
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
  const [matches, setMatches] = useState<FileMatch[]>([])
  const [active, setActive] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const list = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    let cancelled = false
    const timer = setTimeout(() => {
      api(hostId)
        .searchFiles(root, query, 50)
        .then((result) => {
          if (cancelled) return
          setMatches(result.matches)
          setActive(0)
          setError(null)
        })
        .catch((err: unknown) => {
          if (!cancelled) setError(err instanceof Error ? err.message : String(err))
        })
    }, DEBOUNCE_MS)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [hostId, root, query])

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
          {error && <p className="empty-note">{error}</p>}
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
              <span className="quick-dir">{dirOf(match.rel)}</span>
            </button>
          ))}
          {!error && matches.length === 0 && (
            <>
              <p className="empty-note">No matching file under {root}.</p>
              {onPickRoot && (
                <RootSuggestions
                  root={root}
                  worktrees={worktrees}
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
