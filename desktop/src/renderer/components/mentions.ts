/**
 * Turning the `@handle` an agent typed into something the eye catches.
 *
 * Decoration only. The daemon decided who a message named when it was posted,
 * and a message carries that answer; nothing here changes who was woken. The
 * two could in principle disagree — a handle that has since been reassigned
 * would be drawn as a chip and have woken nobody — and when they do, the
 * daemon is right and this is merely ink.
 */

/** The shape of a handle: what slugFor and handleFor in Go can produce. */
const TOKEN = /@([A-Za-z0-9][A-Za-z0-9_-]*)/g

/** Elements whose text is not prose. `@param` in a snippet addresses nobody. */
const CODE = new Set(['CODE', 'PRE', 'KBD', 'SAMP'])

/** The handles in a body, lowercased and without repeats. */
export function findMentionTokens(text: string): string[] {
  const seen = new Set<string>()
  for (const match of text.matchAll(TOKEN)) {
    const token = match[1]?.toLowerCase()
    if (token) seen.add(token)
  }
  return [...seen]
}

/**
 * Wraps every known handle in already sanitised HTML.
 *
 * Walks the text nodes rather than running a regex over the string. A regex
 * would match inside an attribute — an `@` in a href or a title — and inside
 * code, and the first of those would corrupt the markup rather than merely
 * decorate the wrong thing.
 */
export function decorateMentions(html: string, handles: Set<string>): string {
  if (handles.size === 0 || !html.includes('@')) return html

  const doc = new DOMParser().parseFromString(`<div>${html}</div>`, 'text/html')
  const root = doc.body.firstElementChild
  if (!root) return html

  const walker = doc.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const found: Text[] = []
  while (walker.nextNode()) {
    const node = walker.currentNode as Text
    if (!node.nodeValue?.includes('@')) continue
    if (insideCode(node)) continue
    found.push(node)
  }

  for (const node of found) {
    replaceIn(doc, node, handles)
  }
  return root.innerHTML
}

function insideCode(node: Node): boolean {
  for (let at = node.parentElement; at; at = at.parentElement) {
    if (CODE.has(at.tagName)) return true
  }
  return false
}

// Rebuilt as a fragment rather than by setting innerHTML on the parent, which
// would re-parse its siblings and throw away anything already decorated.
function replaceIn(doc: Document, node: Text, handles: Set<string>): void {
  const text = node.nodeValue ?? ''
  const fragment = doc.createDocumentFragment()
  let at = 0

  for (const match of text.matchAll(TOKEN)) {
    const token = match[1]?.toLowerCase()
    if (!token || !handles.has(token) || match.index === undefined) continue

    fragment.append(text.slice(at, match.index))
    const chip = doc.createElement('span')
    chip.className = 'mention-chip'
    chip.textContent = match[0]
    fragment.append(chip)
    at = match.index + match[0].length
  }

  if (at === 0) return
  fragment.append(text.slice(at))
  node.replaceWith(fragment)
}
