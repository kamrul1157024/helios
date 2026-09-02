/**
 * What the session status line may show, and in what order it shows it.
 *
 * The stored value is the enabled segments, in the order they are drawn.
 * Absence is how a segment is hidden — there is no second list of the ones that
 * are off, because the catalogue below already says what the whole set is and
 * anything missing from the stored order is the difference.
 *
 * Shared rather than renderer-only: the main process sanitises the preference
 * on the way off disk, and it cannot do that against a list it does not have.
 */

export type SegmentId = 'cwd' | 'branch' | 'model' | 'mode' | 'status' | 'memory' | 'host' | 'id'

/** Every segment there is, in the order the settings list offers them. */
export const SEGMENTS: { id: SegmentId; label: string }[] = [
  { id: 'cwd', label: 'Working directory' },
  { id: 'branch', label: 'Git branch' },
  { id: 'model', label: 'Model' },
  { id: 'mode', label: 'Permission mode' },
  { id: 'status', label: 'Status' },
  { id: 'memory', label: 'Memory' },
  { id: 'host', label: 'Host' },
  { id: 'id', label: 'Session id' },
]

/**
 * What a new install shows.
 *
 * The five that answer "where am I, what is running, and is it busy" — which is
 * what the deleted header was for. Memory, host and id are for a specific
 * question rather than a glance, so they are off until asked for.
 */
export const DEFAULT_STATUS_LINE: SegmentId[] = ['cwd', 'branch', 'model', 'mode', 'status']

const KNOWN = new Set<string>(SEGMENTS.map((segment) => segment.id))

/**
 * A stored order, or the default if it is anything else.
 *
 * An empty array is a real answer — it means the user turned everything off —
 * so only a non-array falls back. Unknown ids are dropped rather than rejecting
 * the whole list: a preference written by a later version that knew about one
 * more segment should lose that segment, not every segment.
 */
export function parseStatusLine(raw: unknown): SegmentId[] {
  if (!Array.isArray(raw)) return [...DEFAULT_STATUS_LINE]
  const seen = new Set<SegmentId>()
  for (const value of raw) {
    if (typeof value === 'string' && KNOWN.has(value)) seen.add(value as SegmentId)
  }
  return [...seen]
}

/** Turns a segment off, or back on at the end of the line. */
export function toggleSegment(order: SegmentId[], id: SegmentId): SegmentId[] {
  return order.includes(id) ? order.filter((one) => one !== id) : [...order, id]
}

/**
 * Moves a segment to an index, counted in the list as it was before the move.
 *
 * The index is where the drop landed among the rows on screen, so it is read
 * before the dragged row is taken out — dropping row 0 onto the gap below row 2
 * means index 2, and removing it first would make that mean row 3.
 */
export function moveSegment(order: SegmentId[], id: SegmentId, index: number): SegmentId[] {
  const from = order.indexOf(id)
  if (from < 0) return order
  const target = Math.max(0, Math.min(index, order.length))
  const rest = order.filter((one) => one !== id)
  rest.splice(from < target ? target - 1 : target, 0, id)
  return rest
}

/** The segments that are off, in catalogue order. */
export function hiddenSegments(order: SegmentId[]): SegmentId[] {
  return SEGMENTS.map((segment) => segment.id).filter((id) => !order.includes(id))
}
