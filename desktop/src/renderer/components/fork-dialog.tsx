import { useState } from 'react'

import { statusOf } from '../errors.ts'
import { store, useStore } from '../store.ts'
import { Modal } from './newsession.tsx'
import { sessionLabel, type Session } from '../../shared/models.ts'

/**
 * How a session should be branched.
 *
 * One field and a button is the whole ordinary path. A new worktree is the
 * default and needs no control: a fork exists to try a second answer to the
 * same question, and two agents editing one checkout is not a second answer,
 * it is a race. Sharing the parent's folder is behind a disclosure because it
 * is the answer to a narrow question, and putting it on the face invites a
 * shared checkout by accident.
 *
 * See docs/specs/64-session-forking.md.
 */
export function ForkDialog(): JSX.Element | null {
  const dialog = useStore((s) => s.forkDialog)
  if (!dialog) return null
  return <Dialog hostId={dialog.hostId} session={dialog.session} />
}

/**
 * The branch name the daemon would pick, so the field is filled rather than
 * empty.
 *
 * Deliberately the same rule as the daemon's own: the field shows what will
 * happen, and leaving it untouched must not produce a different name than not
 * showing it at all. The daemon still has the last word — it walks past a name
 * already taken, which a client cannot know about.
 */
function suggestBranch(session: Session): string {
  const slug = sessionLabel(session)
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^[-.]+|[-.]+$/g, '')
    .slice(0, 40)
    .replace(/[-.]+$/, '')
  return slug || 'fork'
}

function Dialog({ hostId, session }: { hostId: string; session: Session }): JSX.Element {
  const [branch, setBranch] = useState(() => suggestBranch(session))
  const [prompt, setPrompt] = useState('')
  const [sameFolder, setSameFolder] = useState(false)
  const [showMore, setShowMore] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = async (): Promise<void> => {
    setBusy(true)
    setError('')
    try {
      await store.forkSession(hostId, session.session_id, {
        workspace: sameFolder ? 'same' : 'worktree',
        branch: sameFolder ? undefined : branch.trim() || undefined,
        prompt: prompt.trim() || undefined,
      })
      store.closeForkDialog()
    } catch (err) {
      // Stays open on failure. A 409 means this session has no conversation to
      // fork yet and a 501 means the provider cannot fork at all; both are
      // answers the user needs to read, not a dialog that vanishes.
      setError(statusOf(err) === 409
        ? 'This session has not started its conversation yet.'
        : String(err instanceof Error ? err.message : err))
      setBusy(false)
    }
  }

  return (
    <Modal title={`Fork “${sessionLabel(session)}”`} onClose={() => store.closeForkDialog()}>
      <div className="fork-dialog">
        <p className="fork-explain">
          The fork keeps everything said so far and carries on separately.
          {!sameFolder && ' It gets a git worktree of its own to work in.'}
        </p>

        {!sameFolder && (
          <label className="field">
            <span>Branch</span>
            <input
              autoFocus
              value={branch}
              placeholder="fork"
              onChange={(event) => setBranch(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !busy) void submit()
              }}
            />
          </label>
        )}

        <label className="field">
          <span>First message</span>
          <textarea
            rows={3}
            value={prompt}
            placeholder="Optional — what should the fork try instead?"
            onChange={(event) => setPrompt(event.target.value)}
          />
        </label>

        {/* The fork starts at the last commit. Saying so before the click beats
            the agent discovering it the first time it reads a file it
            remembers editing. The daemon warns too, but by then the agent is
            already running. */}
        {!sameFolder && (
          <p className="fork-note">
            The fork starts from the last commit, so anything uncommitted here stays here.
          </p>
        )}

        <button className="fork-more" onClick={() => setShowMore((open) => !open)}>
          {showMore ? '▾' : '▸'} Other options
        </button>
        {showMore && (
          <label className="fork-same">
            <input
              type="checkbox"
              checked={sameFolder}
              onChange={(event) => setSameFolder(event.target.checked)}
            />
            <span>
              Work in this session’s folder instead of a new worktree. Both agents will edit the
              same files.
            </span>
          </label>
        )}

        {error && <p className="fork-error">{error}</p>}

        <div className="modal-actions">
          <button onClick={() => store.closeForkDialog()}>Cancel</button>
          <button className="primary" disabled={busy} onClick={() => void submit()}>
            {busy ? 'Forking…' : 'Fork'}
          </button>
        </div>
      </div>
    </Modal>
  )
}
