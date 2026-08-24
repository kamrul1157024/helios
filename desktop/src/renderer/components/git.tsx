import { useEffect, useState } from 'react'

import { api } from '../bridge.ts'
import { store, useStore } from '../store.ts'
import { CommitChanges, ScopePicker, type Scope } from './commits.tsx'
import { DiffView } from './diff-view.tsx'
import { PathLabel } from './path-label.tsx'
import { ReviewView } from './review.tsx'
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
/**
 * An absolute path as a path inside root, or null when it is somewhere else.
 * A file from another checkout has no diff in this one.
 */
function relativeTo(root: string, absolute: string): string | null {
  const base = root.replace(/\/+$/, '')
  if (absolute === base) return null
  return absolute.startsWith(`${base}/`) ? absolute.slice(base.length + 1) : null
}

export function GitPanel({
  hostId,
  cwd,
  revision,
  sessionId,
  active = true,
}: {
  hostId: string
  cwd: string
  revision?: string
  /** Whose agent a review comment is addressed to. */
  sessionId?: string
  /** False while another tab is showing: a hidden panel must not poll. */
  active?: boolean
}): JSX.Element {
  const [worktree, setWorktree] = useState<string | null>(null)
  // Null until resolved: the default depends on the branch, which takes a
  // request, and rendering the working tree in the meantime would make the
  // panel flip scopes under the reader.
  const [scope, setScope] = useState<Scope | null>(null)
  const [worktrees, setWorktrees] = useState(false)
  const [status, setStatus] = useState<GitStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const root = worktree ?? cwd
  const scopeKey = `helios.git.scope.${hostId}.${sessionId ?? root}`

  // A different session is a different repository until proven otherwise.
  useEffect(() => {
    setWorktree(null)
  }, [hostId, cwd])

  /**
   * What the panel opens on. The question the user has when they open git
   * during a session is "what has the agent done to this branch", so that is
   * the default — the uncommitted-only view answers a narrower question and
   * was the wrong one to answer first.
   *
   * A choice made afterwards is remembered per session: two sessions on two
   * branches are two separate reviews, and one's scope means nothing in the
   * other.
   */
  useEffect(() => {
    setWorktrees(false)
    setStatus(null)

    const stored = readScope(scopeKey)
    if (stored) {
      setScope(stored)
      return
    }

    setScope(null)
    let cancelled = false
    api(hostId)
      .gitLog(root, { limit: SCOPE_PROBE })
      .then((log) => {
        if (cancelled) return
        const reviewable = log.scope === 'branch' && log.base && log.commits.length > 0
        setScope(
          reviewable
            ? {
                kind: 'review',
                base: log.base,
                span: log.commits.length,
                label: `Review vs ${log.base}`,
              }
            : { kind: 'working' },
        )
      })
      .catch(() => {
        // No history, no base branch, not a repository — the working tree is
        // the answer that needs nothing to be true.
        if (!cancelled) setScope({ kind: 'working' })
      })
    return () => {
      cancelled = true
    }
  }, [hostId, root, scopeKey])

  const pickScope = (next: Scope): void => {
    setScope(next)
    writeScope(scopeKey, next)
  }

  // An agent naming a file means its uncommitted change, so the panel has to
  // be showing the working tree for the selection below to land anywhere.
  const diffTarget = useStore((s) => s.diffTarget)
  useEffect(() => {
    if (!diffTarget || diffTarget.hostId !== hostId) return
    setScope({ kind: 'working' })
  }, [diffTarget?.seq])

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

  if (!scope) {
    return (
      <div className="panel-loading">
        <span className="spinner" />
        <span>Reading the branch…</span>
      </div>
    )
  }

  return (
    <div className="git">
      <header className="git-head">
        <ScopePicker
          hostId={hostId}
          root={root}
          scope={scope}
          status={status}
          onPick={(next) => {
            pickScope(next)
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
      ) : scope.kind === 'review' ? (
        <ReviewView hostId={hostId} root={root} scope={scope} sessionId={sessionId} />
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
  const target = useStore((s) => s.diffTarget)

  // A path from the previous repository would only produce a failed diff.
  useEffect(() => {
    setSelected(null)
  }, [hostId, root])

  // An agent asked for one file's diff. It names the file the way its own
  // tools do, absolutely, while git wants it relative to the repository.
  useEffect(() => {
    if (!target || target.hostId !== hostId || !target.path) return
    const rel = relativeTo(root, target.path)
    if (rel) setSelected({ path: rel, untracked: false })
    store.clearDiffTarget()
  }, [target?.seq])

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

/** How far back to look when deciding whether the branch has anything to review. */
const SCOPE_PROBE = 50

/**
 * The scope the user last chose for this session, if it still parses. Stored
 * rather than derived because it is a preference, and dropped silently when it
 * is not readable — a bad entry is not worth an error over a panel that has a
 * perfectly good default.
 */
function readScope(key: string): Scope | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Scope
    if (parsed.kind === 'working') return parsed
    if (parsed.kind === 'review' && typeof parsed.base === 'string') return parsed
    if (parsed.kind === 'commit' && typeof parsed.to === 'string') return parsed
    return null
  } catch {
    return null
  }
}

function writeScope(key: string, scope: Scope): void {
  try {
    localStorage.setItem(key, JSON.stringify(scope))
  } catch {
    // A full or disabled store costs the preference, nothing else.
  }
}

/** The last two segments: the parent directory is what distinguishes them. */
function tail(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length <= 2 ? path : `…/${parts.slice(-2).join('/')}`
}
