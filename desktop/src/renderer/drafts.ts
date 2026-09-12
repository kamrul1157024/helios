/**
 * What was typed and not yet sent.
 *
 * A prompt half-written is work: switching session to check something, or
 * closing the new-session dialog to look up a path, should not cost it. The
 * panel is unmounted five minutes after it leaves the screen, so holding this
 * in component state means losing it to a coffee break.
 *
 * Kept per session, and swept, because a machine that has touched a thousand
 * sessions should not carry a thousand drafts for ever.
 */

const DRAFTS_KEY = 'helios.drafts'

/** Beyond this the oldest are dropped, newest kept. */
const KEEP = 100

interface Draft {
  text: string
  /** When it was last typed into, which is what decides who gets dropped. */
  at: number
}

function read(): Record<string, Draft> {
  try {
    const raw = localStorage.getItem(DRAFTS_KEY)
    return raw ? (JSON.parse(raw) as Record<string, Draft>) : {}
  } catch {
    return {}
  }
}

function write(drafts: Record<string, Draft>): void {
  try {
    const entries = Object.entries(drafts)
    // Oldest first, so slicing from the end keeps what was touched most
    // recently rather than whatever the object happened to enumerate first.
    const kept =
      entries.length > KEEP
        ? entries.sort((a, b) => a[1].at - b[1].at).slice(entries.length - KEEP)
        : entries
    localStorage.setItem(DRAFTS_KEY, JSON.stringify(Object.fromEntries(kept)))
  } catch {
    // A full or unavailable store costs the draft, not the typing.
  }
}

/** What was left in this box, or '' for a box nobody has typed in. */
export function loadDraft(key: string): string {
  return read()[key]?.text ?? ''
}

/**
 * Remembers what is in the box, or forgets it once it is empty.
 *
 * An empty draft is not worth a record: it would take a slot from a session
 * that has something in it, and reading it back gives the same '' either way.
 */
export function saveDraft(key: string, text: string): void {
  const drafts = read()
  if (text === '') {
    if (drafts[key] === undefined) return
    delete drafts[key]
  } else {
    drafts[key] = { text, at: Date.now() }
  }
  write(drafts)
}

/** Called when the text has gone somewhere: sent, or started as a session. */
export function clearDraft(key: string): void {
  saveDraft(key, '')
}

/** The key a session's prompt is held under. */
export function sessionDraftKey(hostId: string, sessionId: string): string {
  return `session:${hostId}:${sessionId}`
}

/**
 * The new-session dialog's own key.
 *
 * One draft for the dialog rather than one per host: it is a single form the
 * user opens, fills in, and closes — and what they typed into it is the same
 * thought whichever host it ends up starting on.
 */
export const NEW_SESSION_DRAFT = 'new-session'
