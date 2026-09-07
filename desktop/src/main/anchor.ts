/** A rectangle in screen coordinates. */
export interface Rect {
  x: number
  y: number
  width: number
  height: number
}

const WIDTH = 400
const INSET = 12

/**
 * Where the HUD sits, decided once per showing.
 *
 * The card stack reports its own height whenever a card is added or removed,
 * and the window is resized to match. Choosing the corner on each of those
 * reads the pointer again — so a second approval arriving after the user had
 * moved to another display carried the first one across with it, mid-read. The
 * corner is picked when the HUD appears and held until it goes away.
 */
export class HudAnchor {
  private origin: { x: number; y: number } | null = null

  /** Top-right of [workArea] the first time, then the corner already held. */
  place(workArea: Rect, height: number): Rect {
    if (!this.origin) {
      this.origin = {
        x: workArea.x + workArea.width - WIDTH - INSET,
        y: workArea.y + INSET,
      }
    }
    return { ...this.origin, width: WIDTH, height }
  }

  /** Forgets the corner, so the next showing can land where the user now is. */
  release(): void {
    this.origin = null
  }
}
