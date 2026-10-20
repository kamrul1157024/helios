import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'

import { gitLogPagesQuery, gitLogQuery } from '../queries.ts'
import { Chevron } from './icons.tsx'
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

/** What the Changes view is looking at: the working tree, or some history. */
export type Scope = { kind: 'working' } | CommitScope | ReviewScope

/** How far back to look when deciding whether the branch has anything to review. */
const SCOPE_PROBE = 50

/**
 * Which changes the Files panel's Changes mode shows, and how to change that.
 *
 * Moved here from the Git panel when that panel went (spec 65, B8), with its
 * rules intact. The question the user has when they look at changes during a
 * session is "what has the agent done to this branch", so the default is the
 * review against the base when the branch has commits ahead of it — the
 * uncommitted-only view answers a narrower question and was the wrong one to
 * answer first. A choice made afterwards is remembered per session, under the
 * key the Git panel used, so nobody's choice is lost by the move.
 *
 * `pick` is a person choosing, and is remembered. `adopt` is an agent pointing
 * at something through helios_show, which is a visit rather than a preference.
 * Null while the default is still being worked out: rendering the working tree
 * meanwhile would flip the scope under the reader.
 */
export function useScope(
  hostId: string,
  root: string,
  sessionId: string,
  enabled: boolean,
): { scope: Scope | null; pick: (scope: Scope) => void; adopt: (scope: Scope) => void } {
  const scopeKey = `helios.git.scope.${hostId}.${sessionId || root}`
  const stored = useMemo(() => readScope(scopeKey), [scopeKey])
  const probing = !stored && root !== ''
  const probe = useQuery({ ...gitLogQuery(hostId, root, { limit: SCOPE_PROBE }), enabled: probing && enabled })

  const fallback = useMemo<Scope | null>(() => {
    if (stored) return stored
    if (probing && probe.isPending) return null
    const log = probe.data
    // No history, no base branch, not a repository — the working tree is the
    // answer that needs nothing to be true.
    if (!log || log.scope !== 'branch' || !log.base || log.commits.length === 0) return { kind: 'working' }
    return { kind: 'review', base: log.base, span: log.commits.length, label: `Review vs ${log.base}` }
  }, [stored, probing, probe.isPending, probe.data])

  const [picked, setPicked] = useState<Scope | null>(null)
  useEffect(() => setPicked(null), [scopeKey])

  const pick = useCallback(
    (next: Scope) => {
      setPicked(next)
      writeScope(scopeKey, next)
    },
    [scopeKey],
  )
  return { scope: picked ?? fallback, pick, adopt: setPicked }
}

/**
 * The scope the user last chose for this session, if it still parses. Stored
 * rather than derived because it is a preference, and dropped silently when it
 * is not readable — a bad entry is not worth an error over a view that has a
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
 * clips its overflow, and the explorer is often shorter than the menu — an
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
  const { data, error, isFetching, isFetchingNextPage, hasNextPage, fetchNextPage } = useInfiniteQuery(
    gitLogPagesQuery(hostId, root, all),
  )
  const commits = useMemo(() => data?.pages.flatMap((page) => page.commits) ?? [], [data])
  /**
   * A fetch the reader is waiting on: the first page, or the next one.
   *
   * Not every fetch is. A working-tree write invalidates the whole `git`
   * prefix, so this log refetches whenever the agent touches a file — and
   * saying so pulled "Load more" out from under the pointer and put a
   * "Loading…" line in its place, for an answer nobody had asked for.
   */
  const loading = isFetchingNextPage || (isFetching && commits.length === 0)
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

          {loading && <p className="empty-note">Loading…</p>}
          {!loading && !error && commits.length === 0 && <p className="empty-note">No commits.</p>}
          {hasNextPage && !loading && (
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

