import { useEffect, useState } from 'react'

import { api } from '../bridge.ts'
import { store } from '../store.ts'
import { CommitChanges, ScopePicker, type Scope } from './commits.tsx'
import { DiffView } from './diff-view.tsx'
import { PathLabel } from './path-label.tsx'
import { WorktreesView } from './worktrees.tsx'
import type { GitChange, GitDiff, GitStatus } from '../../shared/models.ts'

/**
 * The git panel: what is uncommitted, what has been committed on this branch,
 * and which worktrees the repository has.
 *
 * The working tree and history used to be separate tabs, which gave the commit
 * view three columns — log, files, patch — and left none of them readable. They
 * are one view now, files on the left and the patch on the right, with a
 * dropdown choosing which set of changes fills it. The shape of the panel no
 * longer depends on what you are looking at.
 *
 * Picking a worktree rescopes the whole panel to it, so a session started in
 * one checkout can still read the branch an agent is working in next door.
 */
export function GitPanel({
  hostId,
  cwd,
  revision,
  active = true,
}: {
  hostId: string
  cwd: string
  revision?: string
  /** False while another tab is showing: a hidden panel must not poll. */
  active?: boolean
}): JSX.Element {
  const [worktree, setWorktree] = useState<string | null>(null)
  const [scope, setScope] = useState<Scope>({ kind: 'working' })
  const [worktrees, setWorktrees] = useState(false)
  const [status, setStatus] = useState<GitStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const root = worktree ?? cwd

  // A different session is a different repository until proven otherwise.
  useEffect(() => {
    setWorktree(null)
  }, [hostId, cwd])

  // A commit picked from one repository means nothing in the next.
  useEffect(() => {
    setScope({ kind: 'working' })
    setWorktrees(false)
    setStatus(null)
  }, [hostId, root])

  // Status is fetched here rather than in the changes view because the header
  // shows the branch in every scope — reading a commit is no reason to lose it.
  useEffect(() => {
    // revision moves with every hook the agent fires; behind another tab that
    // is a status call per tool call, for a panel nobody is looking at.
    if (!active) return
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
  }, [hostId, root, revision, active])

  return (
    <div className="git">
      <header className="git-head">
        <ScopePicker
          hostId={hostId}
          root={root}
          scope={scope}
          status={status}
          onPick={(next) => {
            setScope(next)
            setWorktrees(false)
          }}
        />

        {status && (
          <>
            <span className="branch">{status.branch}</span>
            {status.ahead > 0 && <span className="pill">↑{status.ahead}</span>}
            {status.behind > 0 && <span className="pill">↓{status.behind}</span>}
          </>
        )}

        <button
          className={worktrees ? 'ws-view on' : 'ws-view'}
          title="Other checkouts of this repository"
          onClick={() => setWorktrees((v) => !v)}
        >
          Worktrees
        </button>

        {worktree && (
          <button className="pill" title="Back to this session's own worktree" onClick={() => setWorktree(null)}>
            ✕ {tail(worktree)}
          </button>
        )}
      </header>

      {error ? (
        <p className="empty-note">{error}</p>
      ) : worktrees ? (
        <WorktreesView
          hostId={hostId}
          cwd={cwd}
          active={root}
          onPick={(path) => {
            setWorktree(path)
            setWorktrees(false)
          }}
        />
      ) : scope.kind === 'working' ? (
        <ChangesView hostId={hostId} root={root} status={status} />
      ) : (
        <CommitChanges hostId={hostId} root={root} scope={scope} />
      )}
    </div>
  )
}

/** The working tree: staged, changed and untracked files, and their diffs. */
function ChangesView({
  hostId,
  root,
  status,
}: {
  hostId: string
  root: string
  status: GitStatus | null
}): JSX.Element {
  const [selected, setSelected] = useState<{ path: string; untracked: boolean } | null>(null)
  const [diff, setDiff] = useState<GitDiff | null>(null)

  // A path from the previous repository would only produce a failed diff.
  useEffect(() => {
    setSelected(null)
  }, [hostId, root])

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

  if (!status) return <p className="empty-note">Loading…</p>

  // An older daemon sends JSON null for an empty list rather than [].
  const groups: [string, GitChange[], boolean][] = [
    ['Staged', status.staged ?? [], false],
    ['Changed', status.unstaged ?? [], false],
    ['Untracked', status.untracked ?? [], true],
  ]

  return (
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

      <div className="git-diff">
        {diff ? (
          <>
            <header>
              <span>{diff.file}</span>
              <span className="muted">{diff.stat}</span>
            </header>
            <DiffView diff={diff.diff} />
          </>
        ) : (
          <p className="empty-note">{status.dirty ? 'Pick a file.' : 'Nothing to show.'}</p>
        )}
      </div>
    </div>
  )
}

/** The last two segments: the parent directory is what distinguishes them. */
function tail(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length <= 2 ? path : `…/${parts.slice(-2).join('/')}`
}
