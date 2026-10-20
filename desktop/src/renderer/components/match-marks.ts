// Where a match sits in a line of text, and how to cut the text around it.
//
// Pure, for the same reason as the other rule modules here: the offsets come
// from the daemon in UTF-16 code units, and an off-by-one is the kind of thing
// that reads fine on ASCII and goes wrong on the first emoji.

/** A run of text, matched or not. */
export interface Segment {
  text: string
  hit: boolean
}

/**
 * Splits text into runs at the given character offsets. `positions` are single
 * characters (quick open's fuzzy match) and `offset` shifts them, so the same
 * list can mark the name and the folder of one path separately.
 */
export function segmentsAt(text: string, positions: readonly number[] | undefined, offset = 0): Segment[] {
  if (!positions || positions.length === 0) return text ? [{ text, hit: false }] : []
  const marked = new Set<number>()
  for (const at of positions) {
    const local = at - offset
    if (local >= 0 && local < text.length) marked.add(local)
  }
  return runs(text, (index) => marked.has(index))
}

/** Splits text into runs at [start, end) ranges, as grep reports them. */
export function segmentsIn(text: string, ranges: readonly (readonly [number, number])[] | undefined): Segment[] {
  if (!ranges || ranges.length === 0) return text ? [{ text, hit: false }] : []
  return runs(text, (index) => ranges.some(([start, end]) => index >= start && index < end))
}

function runs(text: string, hit: (index: number) => boolean): Segment[] {
  const out: Segment[] = []
  for (let index = 0; index < text.length; index++) {
    const on = hit(index)
    const last = out[out.length - 1]
    if (last && last.hit === on) last.text += text[index]
    else out.push({ text: text[index] as string, hit: on })
  }
  return out
}

/**
 * How many characters of lead-in a search hit keeps before its first match.
 * Enough to show what the match is part of; few enough that the match itself
 * is still in a narrow panel's view.
 */
export const SNIPPET_LEAD = 24

/**
 * A line of a search hit, cut so the first match is in view.
 *
 * Leading indentation goes first, as it always did. Past that, a match deep in
 * a long line is reached by dropping its start and saying so with an ellipsis,
 * and the ranges move with the text.
 */
export function snippet(
  text: string,
  ranges: readonly (readonly [number, number])[] | undefined,
  lead = SNIPPET_LEAD,
): { clipped: boolean; text: string; ranges: [number, number][] } {
  const indent = text.length - text.trimStart().length
  const first = ranges && ranges.length > 0 ? Math.min(...ranges.map(([start]) => start)) : indent
  const cut = Math.max(indent, first - lead)
  const clippedText = text.slice(cut).trimEnd()
  const moved = (ranges ?? [])
    .map(([start, end]) => [Math.max(start - cut, 0), Math.min(end - cut, clippedText.length)] as [number, number])
    .filter(([start, end]) => end > start)
  return { clipped: cut > indent, text: clippedText, ranges: moved }
}

/** A quick-open query with the line it named taken off, when it named one. */
export interface LineQuery {
  query: string
  line?: number
  column?: number
}

/**
 * Reads `foo.go:42` and `foo.go:42:7`. The suffix is not part of any file name
 * worth searching for, so it comes off before the search and is kept for the
 * open. Anything else is the query as typed.
 */
export function parseLineQuery(raw: string): LineQuery {
  const match = /^(.*?):(\d+)(?::(\d+))?$/.exec(raw.trim())
  if (!match || !match[1]) return { query: raw }
  const line = Number(match[2])
  if (line < 1) return { query: raw }
  const column = match[3] ? Math.max(Number(match[3]), 1) : undefined
  return { query: match[1], line, ...(column ? { column } : {}) }
}
