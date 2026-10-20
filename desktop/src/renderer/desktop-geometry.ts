/**
 * How big the remote screen is, and where it sits in the view (spec 66, D9).
 *
 * Two separate questions, kept apart the way guacd keeps them apart: how many
 * pixels the remote desktop has, which is asked of the daemon, and how those
 * pixels are drawn here, which nobody else hears about. Pure functions, so the
 * arithmetic is asserted directly rather than through a canvas.
 */

export interface Size {
  width: number
  height: number
}

/** What the daemon clamps every request to. Clamped here too, so the menu never offers what it would refuse. */
export const LIMITS = { minWidth: 640, maxWidth: 7680, minHeight: 480, maxHeight: 4320 } as const

export const ASPECTS = ['16:9', '16:10', '4:3', '3:2', '21:9'] as const
export type Aspect = (typeof ASPECTS)[number]

export const HEIGHTS = [720, 900, 1080, 1440] as const

export const DPI_PRESETS = [
  { dpi: 96, label: '100 %' },
  { dpi: 120, label: '125 %' },
  { dpi: 144, label: '150 %' },
  { dpi: 192, label: '200 %' },
] as const
export const DPI_LIMITS = { min: 72, max: 288 } as const

/**
 * Fit follows the view; aspect fixes the shape and takes a height; custom is
 * an exact size.
 */
export type DisplayMode = 'fit' | 'aspect' | 'custom'

export interface DisplayPrefs {
  mode: DisplayMode
  aspect: Aspect
  /** A height in pixels, or 'fit' to take it from the view. */
  height: number | 'fit'
  custom: Size
  /** One remote pixel to one device pixel, rather than scaled into the view. */
  exact: boolean
}

/** The desktop app starts with Fit (D9); the rest is what the other modes open on. */
export const DEFAULT_PREFS: DisplayPrefs = {
  mode: 'fit',
  aspect: '16:10',
  height: 900,
  custom: { width: 1280, height: 800 },
  exact: false,
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

/**
 * Inside the limits, in whole pixels, and with an even width: RDP's display
 * update refuses an odd one (MS-RDPEDISP 2.2.2.2.1), and xrdp would drop the
 * resize rather than round it.
 */
export function clampSize(size: Size): Size {
  const width = clamp(Math.round(size.width), LIMITS.minWidth, LIMITS.maxWidth)
  return {
    width: width - (width % 2),
    height: clamp(Math.round(size.height), LIMITS.minHeight, LIMITS.maxHeight),
  }
}

/** Width over height. */
export function ratioOf(aspect: Aspect): number {
  const [w, h] = aspect.split(':').map(Number) as [number, number]
  return w / h
}

/**
 * A size of the given shape near the given height, kept inside the limits
 * without bending the shape: a 21:9 screen 4320 high would be wider than any
 * width allowed, so it is made shorter instead of narrower.
 */
export function sizeForAspect(ratio: number, height: number): Size {
  let h = clamp(height, LIMITS.minHeight, LIMITS.maxHeight)
  let w = h * ratio
  if (w > LIMITS.maxWidth) {
    w = LIMITS.maxWidth
    h = w / ratio
  }
  if (w < LIMITS.minWidth) {
    w = LIMITS.minWidth
    h = w / ratio
  }
  return clampSize({ width: w, height: h })
}

/**
 * How many pixels to ask the remote screen for.
 *
 * Fit asks for the view itself, in device pixels when the reader wants exact
 * ones — otherwise a 1:1 view of a CSS-sized screen would fill a quarter of a
 * HiDPI window.
 */
export function remoteSize(prefs: DisplayPrefs, view: Size, pixelRatio = 1): Size {
  switch (prefs.mode) {
    case 'fit': {
      const factor = prefs.exact ? pixelRatio : 1
      return clampSize({ width: view.width * factor, height: view.height * factor })
    }
    case 'aspect':
      return sizeForAspect(ratioOf(prefs.aspect), prefs.height === 'fit' ? view.height : prefs.height)
    case 'custom':
      return clampSize(prefs.custom)
  }
}

/** The largest scale at which the whole remote screen fits in the view. */
export function fitScale(view: Size, remote: Size): number {
  if (remote.width <= 0 || remote.height <= 0 || view.width <= 0 || view.height <= 0) return 1
  return Math.min(view.width / remote.width, view.height / remote.height)
}

/** Where the screen is drawn: its scale, its drawn size, and its offset in the view. */
export interface Placement {
  scale: number
  width: number
  height: number
  left: number
  top: number
}

/**
 * Letterboxes the remote screen in the view.
 *
 * Centred on both axes, with the rest left to the background. A screen larger
 * than the view — exact pixels on a big remote — starts at the corner instead,
 * so the view scrolls over it rather than cutting off its left edge.
 */
export function placement(view: Size, remote: Size, exact: boolean, pixelRatio = 1): Placement {
  const scale = exact ? 1 / pixelRatio : fitScale(view, remote)
  const width = Math.round(remote.width * scale)
  const height = Math.round(remote.height * scale)
  return {
    scale,
    width,
    height,
    left: Math.max(0, Math.floor((view.width - width) / 2)),
    top: Math.max(0, Math.floor((view.height - height) / 2)),
  }
}

export function clampDpi(dpi: number): number {
  return clamp(Math.round(dpi), DPI_LIMITS.min, DPI_LIMITS.max)
}

/**
 * Reads saved preferences, keeping what is valid and defaulting the rest. A
 * file written by an older build, or edited by hand, must not leave the view
 * asking for a size the daemon would refuse.
 */
export function parsePrefs(raw: unknown): DisplayPrefs {
  if (!raw || typeof raw !== 'object') return DEFAULT_PREFS
  const value = raw as Partial<Record<keyof DisplayPrefs, unknown>>
  const mode = (['fit', 'aspect', 'custom'] as const).find((one) => one === value.mode) ?? DEFAULT_PREFS.mode
  const aspect = ASPECTS.find((one) => one === value.aspect) ?? DEFAULT_PREFS.aspect
  const height =
    value.height === 'fit'
      ? 'fit'
      : typeof value.height === 'number' && Number.isFinite(value.height)
        ? clamp(Math.round(value.height), LIMITS.minHeight, LIMITS.maxHeight)
        : DEFAULT_PREFS.height
  const custom = value.custom as Partial<Size> | undefined
  return {
    mode,
    aspect,
    height,
    custom:
      typeof custom?.width === 'number' && typeof custom.height === 'number'
        ? clampSize({ width: custom.width, height: custom.height })
        : DEFAULT_PREFS.custom,
    exact: value.exact === true,
  }
}
