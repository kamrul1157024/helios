import { useEffect, useMemo, useRef, useState } from 'react'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'

import { gitChangesQuery, gitDiffQuery, gitLogPagesQuery } from '../queries.ts'
import { useStore } from '../store.ts'
import { DiffView } from './diff-view.tsx'
import { Chevron } from './icons.tsx'
import { PathLabel } from './path-label.tsx'
import { timeAgo, type Commit, type GitStatus } from '../../shared/models.ts'

/** A lone commit when `from` is null, otherwise everything from `from` to `to`. */
export interface CommitScope {
  kind: 'commit'
  to: string
  from: string | null
  /** How many commits the range spans, for the label. */
  span: number
  /** What the picker button shows — resolved when picked, so the log need not be loaded to render it. */
  label: string
}

/**
 * Everything this branch changed against the branch it left — the review a
 * pull request would show, before there is a pull request to show it.
 */
export interface ReviewScope {
  kind: 'review'
  base: string
  span: number
  label: string
}

/** What the git panel is looking at: the working tree, or some history. */
export type Scope = { kind: 'working' } | CommitScope | ReviewScope

/**
 * The control that chooses what the panel shows. Closed it is a one-line
 * summary; open it is the commit log, with the working tree pinned at the top
 * as just another thing to look at.
 *
 * The log is fetched on first open rather than on mount: most visits to the
 * panel are to read uncommitted changes and never touch history.
 */
export function ScopePicker({
  hostId,
  root,
  scope,
  status,
  onPick,
}: {
  hostId: string
  root: string
  scope: Scope
  status: GitStatus | null
  onPick: (scope: Scope) => void
}): JSX.Element {
  const [at, setAt] = useState<Anchor | null>(null)
  const button = useRef<HTMLButtonElement | null>(null)

  // A different repository is a different history; the menu must not reopen
  // onto the last one's commits.
  useEffect(() => {
    setAt(null)
  }, [hostId, root])

  // The menu is positioned from a rect taken when it opened, so anything that
  // moves the button underneath it has to dismiss it.
  useEffect(() => {
    if (!at) return
    const close = (): void => setAt(null)
    window.addEventListener('resize', close)
    return () => window.removeEventListener('resize', close)
  }, [at])

  return (
    <div className="scope">
      <button
        ref={button}
        className={at ? 'scope-button on' : 'scope-button'}
        aria-expanded={at !== null}
        onClick={() => setAt(at ? null : anchorFor(button.current))}
      >
        <span className="scope-label">{scope.kind === 'working' ? 'Uncommitted changes' : scope.label}</span>
        {scope.kind === 'working' && dirtyCount(status) > 0 && (
          <span className="pill">{dirtyCount(status)}</span>
        )}
        <Chevron className="scope-caret" dir="down" />
      </button>

      {at && (
        <ScopeMenu
          hostId={hostId}
          root={root}
          scope={scope}
          status={status}
          at={at}
          onPick={(next, close) => {
            onPick(next)
            if (close) setAt(null)
          }}
          onClose={() => setAt(null)}
        />
      )}
    </div>
  )
}

/** Where the open menu sits, in viewport coordinates. */
interface Anchor {
  left: number
  top: number
  width: number
  maxHeight: number
}

const MENU_WIDTH = 440
const MARGIN = 12

/**
 * The menu is `position: fixed` rather than absolute because `.panel-body`
 * clips its overflow, and the git panel is often shorter than the menu — an
 * absolutely positioned popover loses its bottom half whenever the terminal is
 * open below it.
 */
function anchorFor(button: HTMLButtonElement | null): Anchor | null {
  if (!button) return null
  const rect = button.getBoundingClientRect()
  const width = Math.min(MENU_WIDTH, window.innerWidth - MARGIN * 2)
  const top = rect.bottom + 6
  return {
    left: Math.max(MARGIN, Math.min(rect.left, window.innerWidth - width - MARGIN)),
    top,
    width,
    maxHeight: Math.max(220, window.innerHeight - top - MARGIN),
  }
}

/** The open popover: working tree, then the log, with range selection. */
function ScopeMenu({
  hostId,
  root,
  scope,
  status,
  at,
  onPick,
  onClose,
}: {
  hostId: string
  root: string
  scope: Scope
  status: GitStatus | null
  at: Anchor
  /** `close` is false for a range extension, which takes a second click. */
  onPick: (scope: Scope, close: boolean) => void
  onClose: () => void
}): JSX.Element {
  const [all, setAll] = useState(false)
  const { data, error, isFetching, hasNextPage, fetchNextPage } = useInfiniteQuery(
    gitLogPagesQuery(hostId, root, all),
  )
  const commits = useMemo(() => data?.pages.flatMap((page) => page.commits) ?? [], [data])
  // The newest page answers for the branch: base and scope do not move between
  // pages, and has_more is the last page's to report.
  const log = data?.pages[data.pages.length - 1] ?? null
  const commitsRef = useRef<Commit[]>(commits)
  commitsRef.current = commits

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  /**
   * Plain click picks one commit and closes. Shift or ⌘ extends from whatever
   * is already picked, and keeps the menu open so the range can be adjusted.
   */
  const pick = (commit: Commit, extend: boolean): void => {
    const single = (): void => {
      onPick({ kind: 'commit', to: commit.sha, from: null, span: 1, label: commit.subject }, true)
    }
    if (!extend || scope.kind !== 'commit') {
      single()
      return
    }
    const list = commitsRef.current
    const anchor = list.findIndex((entry) => entry.sha === scope.to)
    const picked = list.findIndex((entry) => entry.sha === commit.sha)
    if (anchor < 0 || picked < 0 || anchor === picked) {
      single()
      return
    }
    // Lower in the list is older, and the older end is what we diff from.
    const newer = Math.min(anchor, picked)
    const older = Math.max(anchor, picked)
    const to = list[newer]
    const from = list[older]
    if (!to || !from) {
      single()
      return
    }
    const span = older - newer + 1
    onPick({ kind: 'commit', to: to.sha, from: from.sha, span, label: `${span} commits` }, false)
  }

  return (
    <>
      <div className="scope-backdrop" onMouseDown={onClose} />
      <div
        className="scope-menu"
        style={{ left: at.left, top: at.top, width: at.width, maxHeight: at.maxHeight }}
      >
        <button
          className={`scope-row ${scope.kind === 'working' ? 'selected' : ''}`}
          onClick={() => onPick({ kind: 'working' }, true)}
        >
          <span>Uncommitted changes</span>
          {status && (
            <span className="scope-row-meta">
              {status.dirty ? `${dirtyCount(status)} files` : 'clean'}
            </span>
          )}
        </button>

        {/* The whole branch, offered before the individual commits: reviewing
            what an agent did is a question about the branch, and picking the
            commits one at a time is a worse way to ask it. */}
        {log?.scope === 'branch' && log.base && commits.length > 0 && (
          <button
            className={`scope-row ${scope.kind === 'review' ? 'selected' : ''}`}
            onClick={() =>
              onPick(
                {
                  kind: 'review',
                  base: log.base,
                  span: commits.length,
                  label: `Review vs ${log.base}`,
                },
                true,
              )
            }
          >
            <span>Review vs {log.base}</span>
            <span className="scope-row-meta">
              {commits.length} commit{commits.length === 1 ? '' : 's'}
            </span>
          </button>
        )}

        <div className="scope-sep" />

        <div className="commit-scope">
          <button className={all ? 'ws-view' : 'ws-view on'} onClick={() => setAll(false)}>
            Branch
          </button>
          <button className={all ? 'ws-view on' : 'ws-view'} onClick={() => setAll(true)}>
            All
          </button>
          <span className="muted" title={`Compared against ${log?.base || 'nothing'}`}>
            {log?.scope === 'branch' && log.base ? `vs ${log.base}` : 'full history'}
          </span>
        </div>

        <div className="scope-list">
          {error && <p className="empty-note">{error.message}</p>}
          {commits.map((commit, index) => (
            <button
              key={commit.sha}
              className={`commit-row ${rowClass(commit.sha, index, commits, scope)}`}
              onClick={(event) => pick(commit, event.metaKey || event.ctrlKey || event.shiftKey)}
            >
              <span className="commit-subject">{commit.subject}</span>
              <span className="commit-meta">
                <span className="commit-sha">{commit.short}</span>
                <span>{commit.author}</span>
                <span>{timeAgo(commit.date)}</span>
                {commit.insertions > 0 && <span className="d-add">+{commit.insertions}</span>}
                {commit.deletions > 0 && <span className="d-del">−{commit.deletions}</span>}
              </span>
            </button>
          ))}

          {isFetching && <p className="empty-note">Loading…</p>}
          {!isFetching && !error && commits.length === 0 && <p className="empty-note">No commits.</p>}
          {hasNextPage && !isFetching && (
            <button className="commit-more" onClick={() => void fetchNextPage()}>
              Load more
            </button>
          )}
        </div>

        {commits.length > 1 && <p className="scope-hint">⇧-click a second commit to compare a range.</p>}
      </div>
    </>
  )
}

/**
 * The files a commit or a range touched, and the patch for whichever is open —
 * in the same files-left, diff-right shape the working tree uses.
 */
export function CommitChanges({
  hostId,
  root,
  scope,
}: {
  hostId: string
  root: string
  scope: CommitScope
}): JSX.Element {
  const wanted = useStore((s) => s.diffTarget)
  const changesQuery = useQuery(gitChangesQuery(hostId, root, scope.to, scope.from ?? undefined))
  const changes = changesQuery.data

  // Which file the reader clicked, if they have. Cleared when the commit or the
  // agent's request changes, so the default below gets to answer again.
  const [picked, setPicked] = useState<string | null>(null)
  useEffect(() => {
    setPicked(null)
  }, [hostId, root, scope.to, scope.from, wanted?.seq])

  // An agent that named a file meant that file, not the first one in the
  // commit. It says so absolutely; git lists paths from the repo root.
  const fallback = useMemo(() => {
    if (!changes) return null
    const asked = wanted?.path && relativeToRoot(root, wanted.path)
    if (asked && changes.files.some((f) => f.path === asked)) return asked
    return changes.files[0]?.path ?? null
  }, [changes, root, wanted?.path, wanted?.seq])

  const file = picked ?? fallback
  const diffQuery = useQuery(
    gitDiffQuery(hostId, root, file ?? '', { from: scope.from ?? undefined, to: scope.to }),
  )
  const diff = diffQuery.data
  const error = changesQuery.error ?? diffQuery.error

  if (error) return <p className="empty-note">{error.message}</p>
  if (!changes) return <p className="empty-note">Loading…</p>

  return (
    <>
      <div className="git-substatus">
        {changes.single ? (
          <>
            <span className="commit-sha">{short(changes.to)}</span>
            {changes.author && <span className="muted">{changes.author}</span>}
            {changes.date && <span className="muted">{timeAgo(changes.date)}</span>}
          </>
        ) : (
          <span className="commit-sha">
            {short(changes.from)}…{short(changes.to)}
          </span>
        )}
        <span className="commit-counts">
          <span className="d-add">+{changes.insertions}</span>
          <span className="d-del">−{changes.deletions}</span>
        </span>
      </div>

      {changes.body && <pre className="commit-body">{changes.body}</pre>}

      <div className="git-body">
        <div className="git-files">
          {changes.files.map((entry) => (
            <button
              key={entry.path}
              className={`git-file ${file === entry.path ? 'selected' : ''}`}
              onClick={() => setPicked(entry.path)}
              title={entry.from ? `${entry.from} → ${entry.path}` : entry.path}
            >
              <span className={`git-status s${entry.status}`}>{entry.status}</span>
              <PathLabel path={entry.path} className="git-path" />
              <span className="commit-counts">
                {entry.insertions > 0 && <span className="d-add">+{entry.insertions}</span>}
                {entry.deletions > 0 && <span className="d-del">−{entry.deletions}</span>}
              </span>
            </button>
          ))}
          {changes.files.length === 0 && <p className="empty-note">No files — a merge commit.</p>}
          {changes.truncated && <p className="empty-note">Showing the first {changes.files.length} files.</p>}
        </div>

        <div className="git-diff">
          {diff ? (
            <>
              <header>
                <span>{diff.file}</span>
                <span className="muted">{diff.stat}</span>
              </header>
              <DiffView
                diff={diff.diff}
                empty="No textual changes — binary, or a mode change."
                layout={wanted?.layout ?? 'split'}
                line={wanted?.line}
              />
            </>
          ) : (
            <p className="empty-note">Pick a file.</p>
          )}
        </div>
      </div>
    </>
  )
}

/**
 * An absolute path as git would list it, or null when it is outside the repo.
 * An agent names files absolutely; git lists them from the repository root.
 */
function relativeToRoot(root: string, absolute: string): string | null {
  const base = root.replace(/\/+$/, '')
  return absolute.startsWith(`${base}/`) ? absolute.slice(base.length + 1) : null
}

/** Endpoints of a range are selected; what lies between them is merely in it. */
function rowClass(sha: string, index: number, commits: Commit[], scope: Scope): string {
  if (scope.kind !== 'commit') return ''
  if (sha === scope.to || sha === scope.from) return 'selected'
  if (!scope.from) return ''
  const newer = commits.findIndex((entry) => entry.sha === scope.to)
  const older = commits.findIndex((entry) => entry.sha === scope.from)
  if (newer < 0 || older < 0) return ''
  return index > newer && index < older ? 'in-range' : ''
}

function dirtyCount(status: GitStatus | null): number {
  if (!status) return 0
  return (status.staged?.length ?? 0) + (status.unstaged?.length ?? 0) + (status.untracked?.length ?? 0)
}

function short(sha: string): string {
  return sha.slice(0, 7)
}
