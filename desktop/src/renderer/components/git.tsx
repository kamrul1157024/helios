import { useEffect, useState } from 'react'

import { api } from '../bridge.ts'
import { store } from '../store.ts'
import { CommitsView } from './commits.tsx'
import { DiffView } from './diff-view.tsx'
import { PathLabel } from './path-label.tsx'
import { WorktreesView } from './worktrees.tsx'
import type { GitChange, GitDiff, GitStatus } from '../../shared/models.ts'

type View = 'changes' | 'commits' | 'worktrees'

/**
 * The git panel: what is uncommitted, what has been committed on this branch,
 * and which worktrees the repository has.
 *
 * Picking a worktree rescopes the whole panel to it, so a session started in
 * one checkout can still read the branch an agent is working in next door.
 */
export function GitPanel({ hostId, cwd, revision }: { hostId: string; cwd: string; revision?: string }): JSX.Element {
  const [view, setView] = useState<View>('changes')
  const [worktree, setWorktree] = useState<string | null>(null)
  const root = worktree ?? cwd

  // A different session is a different repository until proven otherwise.
  useEffect(() => {
    setWorktree(null)
  }, [hostId, cwd])

  return (
    <div className="git">
      <header className="git-head">
        <div className="ws-side-head git-views">
          {(['changes', 'commits', 'worktrees'] as View[]).map((name) => (
            <button
              key={name}
              className={view === name ? 'ws-view on' : 'ws-view'}
              onClick={() => setView(name)}
            >
              {name === 'changes' ? 'Changes' : name === 'commits' ? 'Commits' : 'Worktrees'}
            </button>
          ))}
        </div>
        {worktree && (
          <button className="pill" title="Back to this session's own worktree" onClick={() => setWorktree(null)}>
            ✕ {tail(worktree)}
          </button>
        )}
      </header>

      {view === 'changes' && <ChangesView hostId={hostId} root={root} revision={revision} />}
      {view === 'commits' && <CommitsView hostId={hostId} root={root} />}
      {view === 'worktrees' && (
        <WorktreesView hostId={hostId} cwd={cwd} active={root} onPick={(path) => setWorktree(path)} />
      )}
    </div>
  )
}

/** The working tree: staged, changed and untracked files, and their diffs. */
function ChangesView({
  hostId,
  root,
  revision,
}: {
  hostId: string
  root: string
  revision?: string
}): JSX.Element {
  const [status, setStatus] = useState<GitStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<{ path: string; untracked: boolean } | null>(null)
  const [diff, setDiff] = useState<GitDiff | null>(null)

  useEffect(() => {
    let cancelled = false
    setError(null)
    api(hostId)
      .gitStatus(root)
      .then((result) => {
        if (!cancelled) setStatus(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
    // Re-reads whenever the agent does something: a tool call is the usual way
    // the working tree changes here.
  }, [hostId, root, revision])

  useEffect(() => {
    if (!selected) {
      setDiff(null)
      return
    }
    let cancelled = false
    api(hostId)
      // An untracked file has nothing to diff against, so it is diffed against
      // nothing — otherwise git says the file is not in the index and the pane
      // comes back empty.
      .gitDiff(root, selected.path, { untracked: selected.untracked })
      .then((result) => {
        if (!cancelled) setDiff(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) store.fail(err)
      })
    return () => {
      cancelled = true
    }
  }, [hostId, root, selected?.path, selected?.untracked])

  if (error) return <p className="empty-note">{error}</p>
  if (!status) return <p className="empty-note">Loading…</p>

  // An older daemon sends JSON null for an empty list rather than [].
  const groups: [string, GitChange[], boolean][] = [
    ['Staged', status.staged ?? [], false],
    ['Changed', status.unstaged ?? [], false],
    ['Untracked', status.untracked ?? [], true],
  ]

  return (
    <>
      <div className="git-substatus">
        <span className="branch">{status.branch}</span>
        {status.ahead > 0 && <span className="pill">↑{status.ahead}</span>}
        {status.behind > 0 && <span className="pill">↓{status.behind}</span>}
        {!status.dirty && <span className="pill clean">clean</span>}
      </div>

      <div className="git-body">
        <div className="git-files">
          {groups.map(([name, files, untracked]) =>
            files.length === 0 ? null : (
              <div key={name} className="git-group">
                <span className="git-group-head">{name}</span>
                {files.map((file) => (
                  <button
                    key={`${name}:${file.path}`}
                    className={`git-file ${selected?.path === file.path ? 'selected' : ''}`}
                    onClick={() => setSelected({ path: file.path, untracked })}
                  >
                    <span className={`git-status s${file.status.trim() || 'x'}`}>
                      {file.status.trim() || '?'}
                    </span>
                    <PathLabel path={file.path} className="git-path" />
                  </button>
                ))}
              </div>
            ),
          )}
          {!status.dirty && <p className="empty-note">Working tree clean.</p>}
        </div>

        {diff && (
          <div className="git-diff">
            <header>
              <span>{diff.file}</span>
              <span className="muted">{diff.stat}</span>
            </header>
            <DiffView diff={diff.diff} />
          </div>
        )}
      </div>
    </>
  )
}

/** The last two segments: the parent directory is what distinguishes them. */
function tail(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length <= 2 ? path : `…/${parts.slice(-2).join('/')}`
}
