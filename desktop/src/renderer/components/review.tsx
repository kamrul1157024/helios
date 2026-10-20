import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { keys } from '../keys.ts'
import { reviewedQuery } from '../queries.ts'
import { store } from '../store.ts'

/**
 * Which files of a review have been read, and a way to tick one.
 *
 * Persisted in the daemon, not here: a review survives a restart, and an agent
 * asks what has been read so it can skip it. An older daemon has no reviewed
 * state at all, which lands as an error and an empty set — ticking still works
 * locally, which is the point.
 */
export function useReviewed(
  hostId: string,
  root: string,
  base: string,
): { viewed: Set<string>; toggle: (path: string) => void } {
  const client = useQueryClient()
  const { data: viewed = EMPTY } = useQuery({ ...reviewedQuery(hostId, root, base), select: asSet })
  const mark = useMutation({
    mutationFn: ({ path, reviewed }: { path: string; reviewed: boolean }) =>
      api(hostId).setReviewed(root, base, path, reviewed),
    // No rollback and no refetch behind it. The tick is the user's, so it
    // stands even if the daemon refused it — which is what an older one does.
    onMutate: ({ path, reviewed }) => {
      client.setQueryData<string[]>(keys.reviewed(hostId, root, base), (files) => {
        const next = new Set(files ?? [])
        if (reviewed) next.add(path)
        else next.delete(path)
        return [...next]
      })
    },
  })
  return { viewed, toggle: (path) => mark.mutate({ path, reviewed: !viewed.has(path) }) }
}

/** Module scope so the cache can memoise it: a fresh Set per render would make
 *  every row re-render on every keystroke elsewhere in the panel. */
const asSet = (files: string[]): Set<string> => new Set(files)
const EMPTY: Set<string> = new Set()

/**
 * A comment on lines of a patch, sent to the agent as its next prompt.
 *
 * This is the part a hosted review cannot do: the author is still at the
 * keyboard, so a comment is not a note for later — it is the next prompt.
 * `from` and `to` are 0-based indices into the patch's lines, inclusive.
 */
export function ReviewComment({
  hostId,
  sessionId,
  path,
  lines,
  from,
  to,
  onDone,
}: {
  hostId: string
  sessionId: string
  path: string
  lines: string[]
  from: number
  to: number
  onDone: () => void
}): JSX.Element {
  const [comment, setComment] = useState('')
  const [sending, setSending] = useState(false)

  const send = async (): Promise<void> => {
    const text = comment.trim()
    if (!text || sending) return
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
      onDone()
    } catch (err) {
      store.fail(err)
    } finally {
      setSending(false)
    }
  }

  return (
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
          if (event.key === 'Escape') onDone()
          if (event.key !== 'Enter' || event.nativeEvent.isComposing || event.shiftKey) return
          event.preventDefault()
          void send()
        }}
      />
      <div className="review-comment-actions">
        <button className="ghost" onClick={onDone}>
          Cancel
        </button>
        <button className="filled" disabled={!comment.trim() || sending} onClick={() => void send()}>
          {sending ? 'Sending…' : 'Send to agent'}
        </button>
      </div>
    </div>
  )
}
