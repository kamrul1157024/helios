import { useEffect, useMemo, useRef, useState } from 'react'

import { api } from '../bridge.ts'
import { store } from '../store.ts'
import { diffClass } from './diff-view.tsx'
import { PathLabel } from './path-label.tsx'
import type { ReviewScope } from './commits.tsx'
import type { CommitFile, GitChanges } from '../../shared/models.ts'

/**
 * Everything the branch changed, in one page: the review a pull request would
 * give, for work that has not been pushed anywhere yet.
 *
 * The panel's other views answer "what is in this commit". This one answers
 * "is what the agent did any good", which is a question about the whole branch
 * and is unanswerable one file at a time behind a picker.
 */
export function ReviewView({
  hostId,
  root,
  scope,
  sessionId,
}: {
  hostId: string
  root: string
  scope: ReviewScope
  /** Absent when the panel outlives its session; disables commenting. */
  sessionId?: string
}): JSX.Element {
  const [changes, setChanges] = useState<GitChanges | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [viewed, setViewed] = useState<Set<string>>(new Set())

  useEffect(() => {
    let cancelled = false
    setChanges(null)
    setError(null)
    setViewed(new Set())
    // Persisted in the daemon, not here: a review survives a restart, and an
    // agent asks what has been read so it can skip it.
    api(hostId)
      .reviewedFiles(root, scope.base)
      .then((files) => {
        if (!cancelled) setViewed(new Set(files))
      })
      .catch(() => {
        // An older daemon has no reviewed state; ticking still works locally.
      })
    api(hostId)
      // Merge base, not the branch tip: commits landed on the base since this
      // branch was cut are not this branch's work, and a two-dot diff reports
      // them as changes it undid.
      .gitChanges(root, 'HEAD', scope.base, true)
      .then((result) => {
        if (!cancelled) setChanges(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [hostId, root, scope.base])

  const toggleViewed = (path: string): void => {
    const next = new Set(viewed)
    const reviewed = !next.has(path)
    if (reviewed) next.add(path)
    else next.delete(path)
    setViewed(next)
    void api(hostId)
      .setReviewed(root, scope.base, path, reviewed)
      .catch(() => {
        // The tick is the user's, so it stands even if the daemon refused it.
      })
  }

  if (error) return <p className="empty-note">{error}</p>
  if (!changes) {
    return (
      <div className="panel-loading">
        <span className="spinner" />
        <span>Loading review…</span>
      </div>
    )
  }
  if (changes.files.length === 0) {
    return <p className="empty-note">Nothing to review — this branch matches {scope.base}.</p>
  }

  return (
    <div className="review">
      <header className="review-head">
        <span className="review-count">
          {changes.files.length} file{changes.files.length === 1 ? '' : 's'} changed
        </span>
        <span className="commit-counts">
          <span className="d-add">+{changes.insertions}</span>
          <span className="d-del">−{changes.deletions}</span>
        </span>
        <span className="muted">
          {scope.base} ← {scope.span} commit{scope.span === 1 ? '' : 's'}
        </span>
        <span className="review-progress">
          {viewed.size}/{changes.files.length} viewed
        </span>
      </header>

      <div className="review-body">
        <nav className="review-files">
          {changes.files.map((file) => (
            <button
              key={file.path}
              className={viewed.has(file.path) ? 'review-file done' : 'review-file'}
              onClick={() => document.getElementById(anchorFor(file.path))?.scrollIntoView()}
            >
              <PathLabel path={file.path} />
              <span className="review-file-stat">
                {file.insertions > 0 && <span className="d-add">+{file.insertions}</span>}
                {file.deletions > 0 && <span className="d-del">−{file.deletions}</span>}
              </span>
            </button>
          ))}
        </nav>

        <div className="review-diffs">
          {changes.files.map((file) => (
            <FileReview
              key={file.path}
              hostId={hostId}
              root={root}
              base={scope.base}
              file={file}
              sessionId={sessionId}
              viewed={viewed.has(file.path)}
              onToggleViewed={() => toggleViewed(file.path)}
            />
          ))}
        </div>
      </div>
    </div>
  )
}

function anchorFor(path: string): string {
  return `review-${path.replace(/[^a-zA-Z0-9]/g, '-')}`
}

/**
 * One file's patch. The diff is fetched when the card first comes into view:
 * a branch of forty files is forty requests if they all load at once, and the
 * reader is looking at one of them.
 */
function FileReview({
  hostId,
  root,
  base,
  file,
  sessionId,
  viewed,
  onToggleViewed,
}: {
  hostId: string
  root: string
  base: string
  file: CommitFile
  sessionId?: string
  viewed: boolean
  onToggleViewed: () => void
}): JSX.Element {
  const [diff, setDiff] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [wanted, setWanted] = useState(false)
  const card = useRef<HTMLElement | null>(null)

  useEffect(() => {
    const el = card.current
    if (!el || wanted) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) setWanted(true)
      },
      { rootMargin: '400px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [wanted])

  useEffect(() => {
    if (!wanted || viewed) return
    let cancelled = false
    api(hostId)
      .gitDiff(root, file.path, { from: base, to: 'HEAD', mergeBase: true })
      .then((result) => {
        if (!cancelled) setDiff(result.diff)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [wanted, viewed, hostId, root, base, file.path])

  return (
    <section className="review-card" id={anchorFor(file.path)} ref={card}>
      <header className="review-card-head">
        <span className={`review-status s-${file.status.toLowerCase()}`}>{file.status}</span>
        <PathLabel path={file.path} />
        <span className="review-file-stat">
          {file.insertions > 0 && <span className="d-add">+{file.insertions}</span>}
          {file.deletions > 0 && <span className="d-del">−{file.deletions}</span>}
        </span>
        <label className="check review-viewed">
          <input type="checkbox" checked={viewed} onChange={onToggleViewed} />
          Viewed
        </label>
      </header>

      {!viewed &&
        (error ? (
          <p className="empty-note">{error}</p>
        ) : diff === null ? (
          <p className="empty-note">Loading…</p>
        ) : (
          <ReviewDiff hostId={hostId} path={file.path} diff={diff} sessionId={sessionId} />
        ))}
    </section>
  )
}

/**
 * A patch whose lines can be picked out and handed back to the agent.
 *
 * This is the part a hosted review cannot do: the author is still at the
 * keyboard, so a comment is not a note for later — it is the next prompt.
 */
function ReviewDiff({
  hostId,
  path,
  diff,
  sessionId,
}: {
  hostId: string
  path: string
  diff: string
  sessionId?: string
}): JSX.Element {
  const lines = useMemo(() => diff.split('\n'), [diff])
  const [anchor, setAnchor] = useState<number | null>(null)
  const [head, setHead] = useState<number | null>(null)
  const [comment, setComment] = useState('')
  const [sending, setSending] = useState(false)

  const from = anchor !== null && head !== null ? Math.min(anchor, head) : null
  const to = anchor !== null && head !== null ? Math.max(anchor, head) : null

  const clear = (): void => {
    setAnchor(null)
    setHead(null)
    setComment('')
  }

  const select = (index: number, extend: boolean): void => {
    if (extend && anchor !== null) {
      setHead(index)
      return
    }
    setAnchor(index)
    setHead(index)
  }

  const send = async (): Promise<void> => {
    if (!sessionId || from === null || to === null || sending) return
    const text = comment.trim()
    if (!text) return
    setSending(true)
    try {
      const excerpt = lines.slice(from, to + 1).join('\n')
      await api(hostId).sendPrompt(
        sessionId,
        `Review comment on ${path} (diff lines ${from + 1}–${to + 1}):\n\n` +
          '```diff\n' +
          `${excerpt}\n` +
          '```\n\n' +
          text,
      )
      store.notify('Sent to the agent')
      clear()
    } catch (err) {
      store.fail(err)
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="review-diff">
      <pre>
        {lines.map((line, index) => {
          const picked = from !== null && to !== null && index >= from && index <= to
          return (
            <span
              key={index}
              className={`review-line ${diffClass(line)} ${picked ? 'picked' : ''}`}
              onMouseDown={(event) => {
                if (!sessionId) return
                event.preventDefault()
                select(index, event.shiftKey)
              }}
            >
              {line || ' '}
              {'\n'}
            </span>
          )
        })}
      </pre>

      {sessionId && from !== null && to !== null && (
        <div className="review-comment">
          <span className="muted">
            Lines {from + 1}–{to + 1} · ⇧-click to extend
          </span>
          <textarea
            autoFocus
            rows={2}
            value={comment}
            placeholder="What should the agent change here?"
            onChange={(event) => setComment(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') clear()
              if (event.key !== 'Enter' || event.nativeEvent.isComposing || event.shiftKey) return
              event.preventDefault()
              void send()
            }}
          />
          <div className="review-comment-actions">
            <button className="ghost" onClick={clear}>
              Cancel
            </button>
            <button className="filled" disabled={!comment.trim() || sending} onClick={() => void send()}>
              {sending ? 'Sending…' : 'Send to agent'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
