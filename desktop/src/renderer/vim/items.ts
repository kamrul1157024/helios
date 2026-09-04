import type { Zone } from './types.ts'

/**
 * Moving a keyboard cursor over whatever a zone has drawn.
 *
 * The rows a list shows come from React Query and live inside the component
 * that renders them, so a command — which holds no component — cannot ask the
 * store what is on screen. It can ask the document, and the document is the
 * better source anyway: the order the elements are in *is* the order the eye
 * reads, filters and folds included.
 *
 * A zone marks its container with `data-vim-zone` and its rows with
 * `data-vim-item`. Nothing else is required of it.
 */
export function zoneRoot(zone: Zone): HTMLElement | null {
  return document.querySelector<HTMLElement>(`[data-vim-zone="${zone}"]`)
}

export function items(zone: Zone): HTMLElement[] {
  const root = zoneRoot(zone)
  if (!root) return []
  return [...root.querySelectorAll<HTMLElement>('[data-vim-item]')].filter(
    // A folded group's rows are in the document and not on screen. Landing on
    // one would move a cursor nobody can see.
    (element) => element.offsetParent !== null,
  )
}

export function currentIndex(list: HTMLElement[]): number {
  const active = document.activeElement
  if (!(active instanceof HTMLElement)) return -1
  return list.findIndex((element) => element === active || element.contains(active))
}

/**
 * Where the cursor was when a zone was last left.
 *
 * Vim keeps a cursor per window and puts you back on it. Without this, crossing
 * to the rail and back lands on the first row every time, so the two motions
 * that should cancel out instead lose your place.
 */
const lastSeen = new Map<Zone, number>()

export function focusAt(list: HTMLElement[], index: number, zone?: Zone): void {
  const at = Math.max(0, Math.min(index, list.length - 1))
  const target = list[at]
  if (!target) return
  if (zone) lastSeen.set(zone, at)
  target.focus({ preventScroll: true })
  target.scrollIntoView({ block: 'nearest' })
}

export function move(zone: Zone, by: number): void {
  const list = items(zone)
  if (list.length === 0) return
  const at = currentIndex(list)
  focusAt(list, at === -1 ? (by > 0 ? 0 : list.length - 1) : at + by, zone)
}

export function jump(zone: Zone, to: 'first' | 'last'): void {
  const list = items(zone)
  if (list.length === 0) return
  focusAt(list, to === 'first' ? 0 : list.length - 1, zone)
}

/**
 * Runs whatever the focused row does when clicked.
 *
 * `click()` rather than a store call: a row's meaning belongs to the component
 * that drew it, and half of them are already buttons.
 */
export function activate(zone: Zone): void {
  const list = items(zone)
  const at = currentIndex(list)
  list[at === -1 ? 0 : at]?.click()
}

/** The text surface `i` puts the cursor in, if the zone has one. */
export function textSurface(zone: Zone): HTMLElement | null {
  const root = zoneRoot(zone)
  return root?.querySelector<HTMLElement>('[data-vim-insert]') ?? null
}

/**
 * Puts the keyboard cursor inside a zone that has just been moved to.
 *
 * Without this the mode line names the new zone while the ring is still drawn
 * on a row in the old one, and the next `j` moves something the eye is not
 * looking at. Already being inside counts as arrived: crossing back and forth
 * should not lose the row you were on.
 */
export function enterZone(zone: Zone): void {
  const list = items(zone)
  if (list.length === 0) {
    zoneRoot(zone)?.focus?.({ preventScroll: true })
    return
  }
  if (currentIndex(list) !== -1) return
  focusAt(list, lastSeen.get(zone) ?? 0, zone)
}
