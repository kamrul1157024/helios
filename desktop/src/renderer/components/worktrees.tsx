import { useEffect, useState } from 'react'

import { api } from '../bridge.ts'
import type { Worktree } from '../../shared/models.ts'

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

  useEffect(() => {
    let cancelled = false
    setError(null)
    api(hostId)
      .gitWorktrees(cwd)
      .then((result) => {
        if (!cancelled) setWorktrees(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [hostId, cwd])

  if (error) return <p className="empty-note">{error}</p>
  if (!worktrees) return <p className="empty-note">Loading…</p>
  if (worktrees.length === 0) return <p className="empty-note">No worktrees.</p>

  return (
    <div className="worktrees">
      {worktrees.map((worktree) => (
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
