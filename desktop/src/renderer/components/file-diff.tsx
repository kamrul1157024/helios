import { useCallback, useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { languageForPath } from '../markdown.ts'
import { gitDiffQuery } from '../queries.ts'
import type { DiffAt } from '../../shared/models.ts'
import { DiffView, type DiffLayout } from './diff-view.tsx'
import { describeFileError } from './file-errors.ts'
import { ReviewComment } from './review.tsx'

/**
 * A file tab's Diff view (spec 65, B8): the file's patch in the Changes mode's
 * scope, where the Git panel used to show it.
 *
 * Lines can be picked — click, ⇧-click to extend — and sent to the agent as a
 * review comment, as the Git panel's review offered. F7 and ⇧F7 step through
 * the runs of changed lines.
 */
export function FileDiff({
  hostId,
  cwd,
  path,
  rel,
  at,
  layout,
  line,
  sessionId,
  active,
}: {
  hostId: string
  /** Any path inside the repository: what git runs in. */
  cwd: string
  /** Absolute, for the language and the comment. */
  path: string
  /** From the repository's top, as git names it. */
  rel: string
  at: DiffAt
  layout: DiffLayout
  /** A line of the new file an agent asked to be shown. */
  line?: number
  /** Whose agent a comment goes to. */
  sessionId: string
  /** Whether this view has the panel's keys: shown, in a panel on screen. */
  active: boolean
}): JSX.Element {
  const { data, error, isLoading } = useQuery(gitDiffQuery(hostId, cwd, rel, at))
  const diff = data?.diff ?? ''
  const lines = useMemo(() => diff.split('\n'), [diff])
  const [picked, setPicked] = useState<{ anchor: number; head: number } | null>(null)
  const [current, setCurrent] = useState(-1)
  const [runs, setRuns] = useState(0)

  // A new patch is a new set of lines: a pick or a position in the last one
  // names lines that are not there any more.
  useEffect(() => {
    setPicked(null)
    setCurrent(-1)
  }, [diff])

  useEffect(() => {
    if (!active) return
    const onKey = (event: KeyboardEvent): void => {
      if (event.key !== 'F7' || runs === 0) return
      event.preventDefault()
      setCurrent((at) => {
        if (event.shiftKey) return at <= 0 ? runs - 1 : at - 1
        return at >= runs - 1 ? 0 : at + 1
      })
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [active, runs])

  const pick = useCallback((index: number, extend: boolean) => {
    setPicked((was) => (extend && was ? { ...was, head: index } : { anchor: index, head: index }))
  }, [])

  if (error) return <p className="empty-note">{describeFileError(error)}</p>
  if (isLoading) {
    return (
      <div className="tree-loading">
        <span className="spinner tiny" />
      </div>
    )
  }

  const from = picked ? Math.min(picked.anchor, picked.head) : null
  const to = picked ? Math.max(picked.anchor, picked.head) : null

  return (
    <div className="file-diff">
      <div className="file-diff-body">
        <DiffView
          diff={diff}
          empty="No textual changes — binary, or a mode change."
          language={languageForPath(path)}
          layout={layout}
          line={line}
          picked={from !== null && to !== null ? { from, to } : null}
          onPick={pick}
          current={current}
          onChanges={setRuns}
          stack={false}
        />
      </div>
      {from !== null && to !== null && (
        <ReviewComment
          hostId={hostId}
          sessionId={sessionId}
          path={path}
          lines={lines}
          from={from}
          to={to}
          onDone={() => setPicked(null)}
        />
      )}
    </div>
  )
}
