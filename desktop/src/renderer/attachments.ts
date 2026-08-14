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
