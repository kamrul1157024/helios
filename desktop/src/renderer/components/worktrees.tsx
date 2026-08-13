import { useEffect, useMemo, useState } from 'react'

import { api } from '../bridge.ts'
import { byLastTouched, matchesWorktree, timeAgo, type Worktree } from '../../shared/models.ts'

interface Props {
  hostId: string
  /** The repository to list from — any path inside it will do. */
  cwd: string
  /** The worktree the panel is currently scoped to. */
  active: string
  onPick: (path: string) => void
}

/**
 * Every worktree of this repository, with enough state to tell parallel agents
 * apart. Read-only: Helios shows worktrees, it does not make them.
 */
export function WorktreesView({ hostId, cwd, active, onPick }: Props): JSX.Element {
  const [worktrees, setWorktrees] = useState<Worktree[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')

  useEffect(() => {
    let cancelled = false
    setError(null)
    api(hostId)
      .gitWorktrees(cwd)
      .then((result) => {
        if (!cancelled) setWorktrees(byLastTouched(result))
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [hostId, cwd])

  const matches = useMemo(
    () => (worktrees ?? []).filter((worktree) => matchesWorktree(worktree, query)),
    [worktrees, query],
  )

  if (error) return <p className="empty-note">{error}</p>
  if (!worktrees) return <p className="empty-note">Loading…</p>
  if (worktrees.length === 0) return <p className="empty-note">No worktrees.</p>

  return (
    <div className="worktrees">
      <input
        className="worktree-search"
        type="search"
        placeholder="Search branch, path or subject"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      {matches.length === 0 && <p className="empty-note">No worktree matches “{query}”.</p>}
      {matches.map((worktree) => (
        <button
          key={worktree.path}
          className={`worktree ${worktree.path === active ? 'selected' : ''}`}
          onClick={() => onPick(worktree.path)}
          title={worktree.path}
        >
          <span className="worktree-top">
            <span className="branch">{worktree.branch || '(detached)'}</span>
            {worktree.is_main && <span className="pill">main</span>}
            {worktree.locked && <span className="pill">locked</span>}
            {worktree.ahead > 0 && (
              <span className="pill" title={`Ahead of ${worktree.base}`}>
                ↑{worktree.ahead}
              </span>
            )}
            {worktree.behind > 0 && (
              <span className="pill" title={`Behind ${worktree.base}`}>
                ↓{worktree.behind}
              </span>
            )}
            {worktree.dirty > 0 ? (
              <span className="pill" title={`${worktree.dirty} changed files`}>
                ●{worktree.dirty}
              </span>
            ) : (
              <span className="pill clean">clean</span>
            )}
          </span>
          {worktree.subject && <span className="worktree-subject">{worktree.subject}</span>}
          <span className="worktree-path">
            <span className="commit-sha">{worktree.head}</span>
            {tail(worktree.path)}
            {worktree.date && <span>{timeAgo(worktree.date)}</span>}
          </span>
        </button>
      ))}
    </div>
  )
}

/** The last two segments: the parent directory is what distinguishes them. */
function tail(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length <= 2 ? path : `…/${parts.slice(-2).join('/')}`
}
