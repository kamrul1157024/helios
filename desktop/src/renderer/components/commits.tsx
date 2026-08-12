import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '../bridge.ts'
import { DiffView } from './diff-view.tsx'
import { PathLabel } from './path-label.tsx'
import { timeAgo, type Commit, type GitChanges, type GitDiff, type GitLog } from '../../shared/models.ts'

const PAGE = 50

interface Selection {
  /** The newer end of the comparison — a lone commit when `from` is null. */
  to: string
  from: string | null
  /** How many commits the range spans, for the header. */
  span: number
}

/**
 * The commit history of the current branch, and what each commit changed.
 *
 * Clicking a commit shows that commit; ⌘- or shift-clicking a second one shows
 * everything between them, which is the "what did this branch do" view.
 */
export function CommitsView({ hostId, root }: { hostId: string; root: string }): JSX.Element {
  const [log, setLog] = useState<GitLog | null>(null)
  const [commits, setCommits] = useState<Commit[]>([])
  const [all, setAll] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selection, setSelection] = useState<Selection | null>(null)
  const commitsRef = useRef<Commit[]>(commits)
  commitsRef.current = commits

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    setSelection(null)
    api(hostId)
      .gitLog(root, { all: all || undefined, limit: PAGE })
      .then((result) => {
        if (cancelled) return
        setLog(result)
        setCommits(result.commits)
        // The newest commit is what you came to look at.
        if (result.commits[0]) setSelection({ to: result.commits[0].sha, from: null, span: 1 })
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [hostId, root, all])

  const loadMore = async (): Promise<void> => {
    if (!log) return
    setLoading(true)
    try {
      const next = await api(hostId).gitLog(root, { all: all || undefined, limit: PAGE, skip: commits.length })
      setCommits((current) => [...current, ...next.commits])
      setLog(next)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }

  const pick = useCallback((sha: string, extend: boolean): void => {
    setSelection((current) => {
      if (!extend || !current) return { to: sha, from: null, span: 1 }
      const list = commitsRef.current
      const anchor = list.findIndex((commit) => commit.sha === current.to)
      const picked = list.findIndex((commit) => commit.sha === sha)
      if (anchor < 0 || picked < 0 || anchor === picked) return { to: sha, from: null, span: 1 }
      // Lower in the list is older, and the older end is what we diff from.
      const newer = Math.min(anchor, picked)
      const older = Math.max(anchor, picked)
      const to = list[newer]?.sha
      const from = list[older]?.sha
      if (!to || !from) return { to: sha, from: null, span: 1 }
      return { to, from, span: older - newer + 1 }
    })
  }, [])

  if (error) return <p className="empty-note">{error}</p>

  return (
    <div className="git-body">
      <div className="commit-list">
        {log && (
          <div className="commit-scope">
            <button className={all ? 'ws-view' : 'ws-view on'} onClick={() => setAll(false)}>
              Branch
            </button>
            <button className={all ? 'ws-view on' : 'ws-view'} onClick={() => setAll(true)}>
              All
            </button>
            <span className="muted" title={`Compared against ${log.base || 'nothing'}`}>
              {log.scope === 'branch' && log.base ? `vs ${log.base}` : 'full history'}
            </span>
          </div>
        )}

        {commits.map((commit) => (
          <button
            key={commit.sha}
            className={`commit-row ${rowClass(commit.sha, selection)}`}
            onClick={(event) => pick(commit.sha, event.metaKey || event.ctrlKey || event.shiftKey)}
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
        {!loading && commits.length === 0 && <p className="empty-note">No commits.</p>}
        {log?.has_more && !loading && (
          <button className="commit-more" onClick={() => void loadMore()}>
            Load more
          </button>
        )}
      </div>

      {selection ? (
        <CommitDetail hostId={hostId} root={root} selection={selection} />
      ) : (
        <div className="git-diff">
          <p className="empty-note">Pick a commit.</p>
        </div>
      )}
    </div>
  )
}

/** The files a commit or a range touched, and the patch for whichever is open. */
function CommitDetail({
  hostId,
  root,
  selection,
}: {
  hostId: string
  root: string
  selection: Selection
}): JSX.Element {
  const [changes, setChanges] = useState<GitChanges | null>(null)
  const [file, setFile] = useState<string | null>(null)
  const [diff, setDiff] = useState<GitDiff | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setChanges(null)
    setDiff(null)
    setError(null)
    api(hostId)
      .gitChanges(root, selection.to, selection.from ?? undefined)
      .then((result) => {
        if (cancelled) return
        setChanges(result)
        setFile(result.files[0]?.path ?? null)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [hostId, root, selection.to, selection.from])

  useEffect(() => {
    if (!file) {
      setDiff(null)
      return
    }
    let cancelled = false
    api(hostId)
      .gitDiff(root, file, { from: selection.from ?? undefined, to: selection.to })
      .then((result) => {
        if (!cancelled) setDiff(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [hostId, root, file, selection.to, selection.from])

  if (error) return <p className="empty-note">{error}</p>
  if (!changes) return <p className="empty-note">Loading…</p>

  return (
    <div className="commit-detail">
      <header className="commit-head">
        <span className="commit-title">{changes.single ? changes.subject : `${selection.span} commits`}</span>
        <span className="commit-meta">
          {changes.single ? (
            <>
              <span className="commit-sha">{short(changes.to)}</span>
              {changes.author && <span>{changes.author}</span>}
              {changes.date && <span>{timeAgo(changes.date)}</span>}
            </>
          ) : (
            <span className="commit-sha">
              {short(changes.from)}…{short(changes.to)}
            </span>
          )}
          <span className="d-add">+{changes.insertions}</span>
          <span className="d-del">−{changes.deletions}</span>
        </span>
      </header>

      {changes.body && <pre className="commit-body">{changes.body}</pre>}

      <div className="commit-split">
        <div className="commit-files">
          {changes.files.map((entry) => (
            <button
              key={entry.path}
              className={`git-file ${file === entry.path ? 'selected' : ''}`}
              onClick={() => setFile(entry.path)}
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
          {diff ? <DiffView diff={diff.diff} empty="No textual changes — binary, or a mode change." /> : null}
        </div>
      </div>
    </div>
  )
}

function rowClass(sha: string, selection: Selection | null): string {
  if (!selection) return ''
  if (sha === selection.to || sha === selection.from) return 'selected'
  return ''
}

function short(sha: string): string {
  return sha.slice(0, 7)
}
