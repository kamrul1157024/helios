/**
 * A colour per author, so a channel can be followed without reading names.
 *
 * Several sessions talking in one channel used to render every name in the
 * same accent, which made a busy channel a wall of identical headers: telling
 * who said what meant reading each one. A colour carries that at a glance, and
 * it is the cheapest way to do it — no avatars to fetch, nothing to lay out.
 *
 * The hue comes from the session id rather than the title, because the title
 * is generated and can change under a conversation that is already coloured.
 * The id does not, so a session keeps its colour for as long as the channel
 * holds it.
 */

/** How many hues to choose between. Enough to separate a channel of six, and
 *  coarse enough that two adjacent ones are still distinguishable. */
const HUES = 12

/**
 * A person has no colour. `user` is the one author that is not a session, and
 * leaving it in the ordinary text colour is what keeps it distinct from every
 * agent at a glance — more distinct than a thirteenth hue would.
 */
export const AUTHOR_USER = 'user'

/**
 * The colour an author's name and rule are drawn in, or undefined for the
 * person, who is left to the ordinary text colour.
 *
 * `author` is what the daemon sends: "user", or "session:<id>".
 */
export function authorColour(author: string): string | undefined {
  if (author === AUTHOR_USER || author === '') return undefined
  const hue = (hash(author) % HUES) * (360 / HUES)
  // Fixed saturation and lightness: the hue is the only thing allowed to vary,
  // so no session can end up with a name too dim to read on the dark surface.
  return `hsl(${hue} 62% 66%)`
}

/**
 * The one or two letters that stand for an author on its avatar.
 *
 * Taken from the title the daemon resolved rather than the id, because the
 * avatar sits beside the name and initials that do not match the name read as
 * somebody else's. A title like "[INFRA] Debug SSH auth" leads with a bracket,
 * so anything that is not a letter or a digit is skipped rather than shown.
 */
export function authorInitials(from: string): string {
  const words = from.split(/[^\p{L}\p{N}]+/u).filter(Boolean)
  if (words.length === 0) return '?'
  if (words.length === 1) return words[0]!.slice(0, 2).toUpperCase()
  return (words[0]![0]! + words[1]![0]!).toUpperCase()
}

/**
 * FNV-1a, for a hue that is stable across restarts and machines.
 *
 * Not for security, and not for uniqueness: two sessions sharing a hue is a
 * cosmetic collision, and the name beside the colour still says which is which.
 */
function hash(text: string): number {
  let value = 0x811c9dc5
  for (let at = 0; at < text.length; at++) {
    value ^= text.charCodeAt(at)
    value = Math.imul(value, 0x01000193)
  }
  return Math.abs(value)
}
