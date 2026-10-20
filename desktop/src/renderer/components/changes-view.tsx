import { useMemo } from 'react'

import { iconKindOf } from '../filetype.ts'
import { timeAgo, type GitChanges, type GitStatus, type TouchedFile } from '../../shared/models.ts'
import { ScopePicker, type ReviewScope, type Scope } from './commits.tsx'
import { FileIcon } from './icons.tsx'
import { useReviewed } from './review.tsx'
import type { ScopeFile } from './scope-files.ts'
import { baseName } from './tab-strip.ts'
import { GIT_LETTER_TITLES, type AgentMarks, type GitLetter } from './tree-rows.ts'

/**
 * What changed, and who changed it (spec 65, B6 and B8).
 *
 * The scope picker the Git panel had sits at the top, and the list follows it.
 * On the working tree it is two lists: what this session's agent wrote, from
 * its own tool calls, newest first, and everything else git calls uncommitted
 * — the `Bash` writes the transcript cannot attribute, another session in the
 * same worktree, the person's own edits. On a review, a commit or a range it is
 * the files that scope changed, with the agent's mark on the ones it wrote.
 *
 * A click opens the file with its Diff on.
 */
export function ChangesView({
  hostId,
  root,
  scope,
  onPickScope,
  status,
  touched,
  files,
  changes,
  agent,
  loading,
  onOpen,
}: {
  hostId: string
  /** Where git runs: the panel's root. */
  root: string
  scope: Scope | null
  onPickScope: (scope: Scope) => void
  status: GitStatus | undefined
  touched: TouchedFile[]
  /** The files the scope changed, by absolute path. */
  files: Map<string, ScopeFile>
  /** The history scope's own answer, for its header. */
  changes: GitChanges | undefined
  agent: AgentMarks
  /** Nothing to list yet. */
  loading: boolean
  onOpen: (path: string, line?: number) => void
}): JSX.Element {
  return (
    <div className="changes">
      <div className="changes-scope">
        {scope ? (
          <ScopePicker hostId={hostId} root={root} scope={scope} status={status ?? null} onPick={onPickScope} />
        ) : (
          <span className="spinner tiny" />
        )}
        {status && <span className="branch">{status.branch}</span>}
      </div>
      {loading || !scope ? (
        <div className="tree-loading">
          <span className="spinner tiny" />
        </div>
      ) : scope.kind === 'working' ? (
        <WorkingTree status={status} touched={touched} files={files} root={root} onOpen={onOpen} />
      ) : (
        <ScopeList
          hostId={hostId}
          root={root}
          scope={scope}
          files={files}
          changes={changes}
          agent={agent}
          onOpen={onOpen}
        />
      )}
    </div>
  )
}

/** The uncommitted scope: the agent's own writes, then everything else. */
function WorkingTree({
  status,
  touched,
  files,
  root,
  onOpen,
}: {
  status: GitStatus | undefined
  touched: TouchedFile[]
  files: Map<string, ScopeFile>
  root: string
  onOpen: (path: string, line?: number) => void
}): JSX.Element {
  const others = useMemo(() => {
    const mine = new Set(touched.map((file) => file.path))
    return [...files.values()].filter((file) => !mine.has(file.path)).sort((a, b) => a.path.localeCompare(b.path))
  }, [touched, files])

  return (
    <>
      <section className="changes-section">
        <h3 className="changes-head">
          Edited by this session <span className="find-count">{touched.length}</span>
        </h3>
        {touched.length === 0 && <p className="empty-note">This session has not written a file yet.</p>}
        {touched.map((file) => (
          <ChangeRow
            key={file.path}
            path={file.path}
            root={root}
            deleted={Boolean(file.deleted)}
            letter={files.get(file.path)?.letter}
            when={timeAgo(file.last_at)}
            title={`${file.tool}, ${file.count} ${file.count === 1 ? 'call' : 'calls'}, ${timeAgo(file.last_at)} ago`}
            onClick={() => onOpen(file.path, file.line)}
          />
        ))}
      </section>
      <section className="changes-section">
        <h3 className="changes-head">
          Other uncommitted changes <span className="find-count">{others.length}</span>
        </h3>
        {!status && <p className="empty-note">Not a git repository.</p>}
        {status && others.length === 0 && <p className="empty-note">Nothing else.</p>}
        {others.map((file) => (
          <ChangeRow
            key={file.path}
            path={file.path}
            root={root}
            deleted={file.letter === 'D'}
            letter={file.letter}
            title={`${GIT_LETTER_TITLES[file.letter]}: ${file.path}`}
            onClick={() => onOpen(file.path)}
          />
        ))}
      </section>
    </>
  )
}

/** A review, a commit or a range: the files it changed. */
function ScopeList({
  hostId,
  root,
  scope,
  files,
  changes,
  agent,
  onOpen,
}: {
  hostId: string
  root: string
  scope: Exclude<Scope, { kind: 'working' }>
  files: Map<string, ScopeFile>
  changes: GitChanges | undefined
  agent: AgentMarks
  onOpen: (path: string) => void
}): JSX.Element {
  const list = [...files.values()]
  return (
    <section className="changes-section">
      {changes && <ScopeSummary changes={changes} />}
      {scope.kind === 'review' ? (
        <ReviewRows hostId={hostId} root={root} scope={scope} list={list} agent={agent} onOpen={onOpen} />
      ) : (
        list.map((file) => (
          <ChangeRow
            key={file.path}
            path={file.path}
            root={root}
            deleted={file.letter === 'D'}
            letter={file.letter}
            mine={agent.files.has(file.path)}
            counts={file}
            title={file.from ? `${file.from} → ${file.rel}` : file.rel}
            onClick={() => onOpen(file.path)}
          />
        ))
      )}
      {list.length === 0 && (
        <p className="empty-note">
          {scope.kind === 'review' ? `Nothing to review — this branch matches ${scope.base}.` : 'No files — a merge commit.'}
        </p>
      )}
      {changes?.truncated && <p className="empty-note">Showing the first {list.length} files.</p>}
    </section>
  )
}

/** A review's rows, each with the Viewed tick the Git panel's review had. */
function ReviewRows({
  hostId,
  root,
  scope,
  list,
  agent,
  onOpen,
}: {
  hostId: string
  root: string
  scope: ReviewScope
  list: ScopeFile[]
  agent: AgentMarks
  onOpen: (path: string) => void
}): JSX.Element {
  const { viewed, toggle } = useReviewed(hostId, root, scope.base)
  return (
    <>
      <h3 className="changes-head">
        {list.length} file{list.length === 1 ? '' : 's'} changed
        <span className="find-count">
          {list.filter((file) => viewed.has(file.rel)).length}/{list.length} viewed
        </span>
      </h3>
      {list.map((file) => (
        <div key={file.path} className={`change-review ${viewed.has(file.rel) ? 'done' : ''}`}>
          <ChangeRow
            path={file.path}
            root={root}
            deleted={file.letter === 'D'}
            letter={file.letter}
            mine={agent.files.has(file.path)}
            counts={file}
            title={file.rel}
            onClick={() => onOpen(file.path)}
          />
          <input
            type="checkbox"
            className="change-viewed"
            title="Viewed"
            aria-label={`Viewed ${file.rel}`}
            checked={viewed.has(file.rel)}
            onChange={() => toggle(file.rel)}
          />
        </div>
      ))}
    </>
  )
}

/** The commit or range in a line: which, who, when, and how much. */
function ScopeSummary({ changes }: { changes: GitChanges }): JSX.Element {
  return (
    <>
      <div className="git-substatus">
        {changes.single ? (
          <>
            <span className="commit-sha">{changes.to.slice(0, 7)}</span>
            {changes.author && <span className="muted">{changes.author}</span>}
            {changes.date && <span className="muted">{timeAgo(changes.date)}</span>}
          </>
        ) : (
          <span className="commit-sha">
            {changes.from.slice(0, 7)}…{changes.to.slice(0, 7)}
          </span>
        )}
        <span className="commit-counts">
          <span className="d-add">+{changes.insertions}</span>
          <span className="d-del">−{changes.deletions}</span>
        </span>
      </div>
      {changes.body && <pre className="commit-body">{changes.body}</pre>}
    </>
  )
}

function ChangeRow({
  path,
  root,
  deleted,
  letter,
  mine = false,
  when,
  counts,
  title,
  onClick,
}: {
  path: string
  root: string
  deleted: boolean
  letter: GitLetter | undefined
  /** Written by this session's agent. */
  mine?: boolean
  when?: string
  counts?: { insertions?: number; deletions?: number }
  title: string
  onClick: () => void
}): JSX.Element {
  const rel = path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path
  const folder = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : ''
  return (
    <button className={`change-row ${deleted ? 'deleted' : ''}`} title={title} onClick={onClick}>
      <FileIcon kind={iconKindOf(path)} className="tree-icon" />
      <span className="change-name">{baseName(path)}</span>
      <span className="change-dir">{folder}</span>
      <span className="grow" />
      {counts && (
        <span className="commit-counts">
          {(counts.insertions ?? 0) > 0 && <span className="d-add">+{counts.insertions}</span>}
          {(counts.deletions ?? 0) > 0 && <span className="d-del">−{counts.deletions}</span>}
        </span>
      )}
      {when && <span className="change-when">{when}</span>}
      {mine && <span className="agent-mark" title="Edited by this session" />}
      {letter && (
        <span className={`tree-badge g-${letter}`} title={GIT_LETTER_TITLES[letter]}>
          {letter}
        </span>
      )}
    </button>
  )
}
