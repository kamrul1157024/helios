import { useEffect, useState } from 'react'

import { bridge } from '../bridge.ts'
import { renderMarkdown } from '../markdown.ts'
import { store } from '../store.ts'
import type { ReleaseNote, UpdateInfo } from '../../shared/models.ts'
import { Modal } from './newsession.tsx'

/**
 * What arrived, once, when a newer release exists.
 *
 * This was a line across the top of the window saying a version number and
 * nothing else — the reader had to leave for GitHub to find out whether it was
 * worth the download, and somebody three releases behind got the same one
 * line. The notes are what the announcement is for, so they are in it.
 *
 * Shown once per release: the main process remembers the version dismissed, so
 * the check answers null on every launch after this one. Closing by any route
 * counts as reading it — a dialog that returns tomorrow because it was closed
 * with Escape is a dialog nobody trusts.
 */
export function ReleaseNotes(): JSX.Element | null {
  const [update, setUpdate] = useState<UpdateInfo | null>(null)

  useEffect(() => {
    void bridge.updates.check().then(setUpdate)
  }, [])

  if (!update) return null

  const close = (): void => {
    void bridge.updates.dismiss(update.version)
    setUpdate(null)
  }

  return (
    <Modal title={`Helios ${update.version} is out`} onClose={close}>
      {/* The half of the job this dialog used to leave out: every paired
          machine runs its own daemon, and a session's behaviour comes from
          that daemon rather than from the window looking at it. */}
      <p className="pane-note">
        Update the daemon on each paired machine too — that is what runs the sessions. Nothing here
        updates itself, and updating any part of it keeps running sessions alive.
      </p>

      <div className="release-notes">
        {update.notes.map((note) => (
          <Release key={note.version} note={note} />
        ))}
      </div>

      <div className="pane-actions">
        {/* An anchor rather than a bridge call: the window open handler already
            sends https elsewhere, and nothing in this app navigates itself. */}
        <a className="ext-link" href={update.url} target="_blank" rel="noreferrer noopener">
          Download {update.version}
        </a>
        <button className="ghost" onClick={close}>
          Got it
        </button>
        {/* The pane that names which daemons are behind, which is the question
            this dialog raises and cannot answer itself. */}
        <button
          onClick={() => {
            close()
            store.openSettings('hosts')
          }}
        >
          Review hosts
        </button>
      </div>
    </Modal>
  )
}

function Release({ note }: { note: ReleaseNote }): JSX.Element {
  return (
    <section className="release">
      <h3>
        {note.version}
        {note.publishedAt && <span className="release-date">{released(note.publishedAt)}</span>}
      </h3>
      {note.body ? (
        <div className="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(note.body) }} />
      ) : (
        <p className="pane-note">No notes for this one.</p>
      )}
    </section>
  )
}

/** The date alone. A release is news for a day; the hour it was cut is not. */
function released(iso: string): string {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}
