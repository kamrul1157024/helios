// The composer's files: the ⊕, the drop target, the paste, and the chips they
// all end up as.
//
// Written out of chat.tsx when the new-session dialog became the second box
// that takes a prompt, and would otherwise have become the second copy of this.
// The order matters more than any of the pieces — bytes are read at attach
// time because a dropped File does not stay valid, and the upload happens
// before the send because a prompt naming a path the daemon never stored sends
// the agent looking for a file that is not there.
import { useEffect, useRef, useState } from 'react'

import { api } from '../bridge.ts'
import { Paperclip } from './icons.tsx'
import {
  isLargePaste,
  needingUpload,
  pastedName,
  pastedTextAttachment,
  promptWithAttachments,
  withStoredPaths,
  type Attachment,
} from '../attachments.ts'

export interface AttachmentBag {
  /** What is on the composer right now. */
  files: Attachment[]
  /** Reads the files into memory now: a dropped File is not valid for long. */
  attach: (files: FileList | File[] | null) => Promise<void>
  remove: (attachment: Attachment) => void
  /** The block just pasted, while the offer to file it instead is up. */
  pasted: string | null
  /** Notes what was pasted, so a large block can be offered as a file. */
  noticePaste: (text: string) => void
  /** Files the pasted block, and answers with the text to take out of the draft. */
  fileThePaste: () => string | null
  keepThePaste: () => void
  /**
   * Stores whatever the daemon has not stored yet, and answers with the prompt
   * that names it.
   *
   * Only what is unstored: a send can fail after a successful upload — a cold
   * session that never acknowledges the prompt is the usual way — and
   * uploading the same bytes on the retry would leave a numbered copy behind
   * for every attempt.
   */
  store: (hostId: string, sessionId: string, text: string) => Promise<string>
  /** After a send has landed: the chips go and their previews are released. */
  clear: () => void
}

export function useAttachments(): AttachmentBag {
  const [files, setFiles] = useState<Attachment[]>([])
  const [pasted, setPasted] = useState<string | null>(null)
  const next = useRef(0)

  const attach = async (chosen: FileList | File[] | null): Promise<void> => {
    const picked = [...(chosen ?? [])]
    if (picked.length === 0) return
    const read = await Promise.all(
      picked.map(async (file) => ({
        id: ++next.current,
        // A pasted screenshot arrives unnamed, and the name becomes the
        // filename on disk and the path in the prompt.
        name: file.name || pastedName(file.type),
        type: file.type,
        size: file.size,
        bytes: new Uint8Array(await file.arrayBuffer()),
        preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : null,
        path: null,
      })),
    )
    setFiles((current) => [...current, ...read])
  }

  const remove = (attachment: Attachment): void => {
    if (attachment.preview) URL.revokeObjectURL(attachment.preview)
    setFiles((current) => current.filter((a) => a.id !== attachment.id))
  }

  const fileThePaste = (): string | null => {
    if (!pasted) return null
    setFiles((current) => [...current, pastedTextAttachment(++next.current, pasted)])
    setPasted(null)
    return pasted
  }

  const store = async (hostId: string, sessionId: string, text: string): Promise<string> => {
    let ready = files
    const pending = needingUpload(files)
    if (pending.length > 0) {
      const stored = await api(hostId).uploadFiles(
        sessionId,
        pending.map(({ name, type, bytes }) => ({ name, type, bytes })),
      )
      ready = withStoredPaths(files, pending, stored.map((file) => file.path))
      // Recorded before the send, which is the call that fails.
      setFiles(ready)
    }
    return promptWithAttachments(ready, text)
  }

  const clear = (): void => {
    for (const attachment of files) {
      if (attachment.preview) URL.revokeObjectURL(attachment.preview)
    }
    setFiles([])
    setPasted(null)
  }

  return {
    files,
    attach,
    remove,
    pasted,
    // A large block is only offered, never intercepted: the paste lands in the
    // composer as it always has, and ignoring the offer leaves the prompt
    // exactly as the user typed it.
    noticePaste: (text) => setPasted(isLargePaste(text) ? text : null),
    fileThePaste,
    keepThePaste: () => setPasted(null),
    store,
    clear,
  }
}

/**
 * Files dropped on a box, without the box swallowing every other drag.
 *
 * All three handlers are gated on the drag carrying files, because a session
 * dragged out of the sidebar is a drag too and the composer is not where it
 * goes.
 */
export function useDropTarget(onFiles: (files: FileList) => void): {
  dropping: boolean
  handlers: {
    onDragOver: (event: React.DragEvent) => void
    onDragLeave: (event: React.DragEvent) => void
    onDrop: (event: React.DragEvent) => void
  }
} {
  const [dropping, setDropping] = useState(false)
  return {
    dropping,
    handlers: {
      onDragOver: (event) => {
        if (!event.dataTransfer.types.includes('Files')) return
        event.preventDefault()
        setDropping(true)
      },
      onDragLeave: (event) => {
        if (event.currentTarget.contains(event.relatedTarget as Node | null)) return
        setDropping(false)
      },
      onDrop: (event) => {
        if (!event.dataTransfer.types.includes('Files')) return
        event.preventDefault()
        setDropping(false)
        onFiles(event.dataTransfer.files)
      },
    },
  }
}

/** The paperclip, and the file input hiding behind it. */
export function AttachButton({
  onFiles,
  disabled = false,
  shortcut = false,
  icon = <Paperclip />,
}: {
  onFiles: (files: FileList | null) => void
  disabled?: boolean
  /**
   * Whether ⌘U opens the picker as well as the click.
   *
   * Off by default because the listener is on the window: a panel that is
   * mounted per tab has several of itself alive at once, and every one of them
   * would answer the keystroke with a dialog of its own.
   */
  shortcut?: boolean
  /**
   * The glyph. A paperclip beside a running session means "add to this turn";
   * the new-session dialog is building a first turn out of nothing, and a plus
   * is what adding to nothing looks like.
   */
  icon?: React.ReactNode
}): JSX.Element {
  const picker = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    if (!shortcut || disabled) return
    const onKey = (event: KeyboardEvent): void => {
      if (!(event.metaKey || event.ctrlKey) || event.key !== 'u') return
      event.preventDefault()
      picker.current?.click()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [shortcut, disabled])

  return (
    <>
      <input
        ref={picker}
        type="file"
        multiple
        hidden
        onChange={(event) => {
          onFiles(event.target.files)
          // Cleared so picking the same file twice fires onChange twice.
          event.target.value = ''
        }}
      />
      <button
        className="icon-btn attach-btn"
        title={shortcut ? 'Add attachment (⌘U) — or paste and drop them here' : 'Add attachment — or paste and drop them here'}
        aria-label="Add attachment"
        disabled={disabled}
        onClick={() => picker.current?.click()}
      >
        {icon}
      </button>
    </>
  )
}

export function AttachmentChips({
  files,
  onRemove,
}: {
  files: Attachment[]
  onRemove: (attachment: Attachment) => void
}): JSX.Element | null {
  if (files.length === 0) return null
  return (
    <div className="attachments">
      {files.map((attachment) => (
        <span key={attachment.id} className="attachment">
          {attachment.preview ? (
            <img className="attachment-thumb" src={attachment.preview} alt="" />
          ) : (
            <span className="attachment-icon">◫</span>
          )}
          <span className="attachment-name" title={attachment.name}>
            {attachment.name}
          </span>
          <span className="attachment-size">{fileSize(attachment.size)}</span>
          <button
            className="attachment-drop"
            aria-label={`Remove ${attachment.name}`}
            title="Remove"
            onClick={() => onRemove(attachment)}
          >
            ✕
          </button>
        </span>
      ))}
    </div>
  )
}

export function PasteOffer({
  text,
  onFile,
  onKeep,
}: {
  text: string
  onFile: () => void
  onKeep: () => void
}): JSX.Element {
  return (
    <div className="paste-offer">
      <span className="paste-offer-text">Pasted {fileSize(new Blob([text]).size)} of text</span>
      <button className="ghost" onClick={onFile}>
        Attach as file
      </button>
      <button
        className="attachment-drop"
        aria-label="Keep the paste in the prompt"
        title="Keep it in the prompt"
        onClick={onKeep}
      >
        ✕
      </button>
    </div>
  )
}

export function fileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
