import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { worktreesQuery } from '../queries.ts'
import { byLastTouched, matchesWorktree, timeAgo } from '../../shared/models.ts'

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
  const [query, setQuery] = useState('')
  // The same read the files panel makes for its root picker, so opening both
  // against one session costs one request rather than two.
  const { data: worktrees, error, isLoading } = useQuery({ ...worktreesQuery(hostId, cwd), select: byLastTouched })

  const matches = useMemo(
    () => (worktrees ?? []).filter((worktree) => matchesWorktree(worktree, query)),
    [worktrees, query],
  )

  if (error) return <p className="empty-note">{error.message}</p>
  if (isLoading) return <p className="empty-note">Loading…</p>
  if (!worktrees || worktrees.length === 0) return <p className="empty-note">No worktrees.</p>

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
