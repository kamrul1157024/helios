/**
 * Files held in the composer on their way to a session.
 *
 * An attachment is uploaded once and remembers where it landed. The send that
 * follows can fail — a cold session that never acknowledges the prompt is the
 * common one — and the composer keeps its chips so the user can try again;
 * without the stored path that retry would upload the same bytes a second
 * time, and the daemon, which will not overwrite a name it already has, would
 * leave a `shot-1.png` behind for every attempt.
 */
export interface Attachment {
  id: number
  name: string
  type: string
  size: number
  bytes: Uint8Array
  /** Object URL for images, so the chip shows what was pasted. */
  preview: string | null
  /** Where the daemon put it, once it has. Null until uploaded. */
  path: string | null
}

/**
 * When a pasted block is big enough to be worth offering as a file instead.
 *
 * Not a correctness limit — the daemon delivers a prompt of any size — but a
 * wall of pasted log lines reads better to the agent as a path it can open than
 * as ten thousand characters of prompt.
 */
export const LARGE_PASTE_CHARS = 2000
export const LARGE_PASTE_LINES = 50

export function isLargePaste(text: string): boolean {
  return text.length >= LARGE_PASTE_CHARS || text.split('\n').length >= LARGE_PASTE_LINES
}

/** A pasted block as an attachment, so it takes the path every file takes. */
export function pastedTextAttachment(id: number, text: string, at = new Date()): Attachment {
  const stamp = at.toISOString().replace(/[-:]/g, '').replace(/\..+/, '')
  const bytes = new TextEncoder().encode(text)
  return {
    id,
    name: `pasted-${stamp}.txt`,
    type: 'text/plain',
    size: bytes.length,
    bytes,
    preview: null,
    path: null,
  }
}

/**
 * Takes the pasted block back out of the draft, once only.
 *
 * The paste has already landed in the composer by the time the offer can be
 * accepted — the default is deliberately left to run, so ignoring the offer
 * changes nothing — and removing every occurrence would also delete an
 * identical block the user pasted earlier on purpose.
 */
export function removeFirst(draft: string, pasted: string): string {
  const at = draft.indexOf(pasted)
  if (at === -1) return draft
  return (draft.slice(0, at) + draft.slice(at + pasted.length)).trim()
}

/** Those the daemon has not stored yet. */
export function needingUpload(attachments: Attachment[]): Attachment[] {
  return attachments.filter((attachment) => attachment.path === null)
}

/**
 * Writes the daemon's paths back onto the attachments that were just sent.
 *
 * Matched by position: the daemon answers in the order it received the parts,
 * and names cannot be matched on because it renames a name already taken.
 */
export function withStoredPaths(
  attachments: Attachment[],
  uploaded: Attachment[],
  paths: string[],
): Attachment[] {
  const stored = new Map(uploaded.map((attachment, index) => [attachment.id, paths[index]]))
  return attachments.map((attachment) => {
    const path = stored.get(attachment.id)
    return path ? { ...attachment, path } : attachment
  })
}

/**
 * The prompt as the agent receives it: a line naming each file, then whatever
 * was typed. The path is the whole mechanism — the agent opens it with Read,
 * so the bytes never enter the context.
 */
export function promptWithAttachments(attachments: Attachment[], text: string): string {
  const lines = attachments
    .filter((attachment) => attachment.path !== null)
    .map((attachment) => `Attached: ${attachment.path}`)
  if (lines.length === 0) return text
  return [...lines, '', text].join('\n').trim()
}
