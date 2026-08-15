/**
 * The VS Code colour theme format, and the colour arithmetic needed to make one
 * usable.
 *
 * Themes in the wild are partial: the format has several hundred colour keys
 * and a theme sets whichever ones its author cared about. Everything here
 * exists so a missing key is a question the resolver can answer rather than a
 * hole in the UI.
 */

export interface TokenColor {
  /** A scope selector, a list of them, or a comma-separated string of them. */
  scope?: string | string[]
  settings?: { foreground?: string; fontStyle?: string }
}

/**
 * Helios' own addition to the format: how far the window lets the desktop
 * through, per surface. Values are the opacity the surface keeps, so 1 is
 * solid and 0 is clear.
 *
 * Carried inside the theme file under a namespaced key, which VS Code ignores,
 * so a glass theme is still a theme anyone can drop into VS Code.
 */
export interface GlassSpec {
  sidebar: number
  panel: number
  terminal: number
}

/** One soft blob of the backdrop. Omitted fields fall back to the slot's own. */
export interface BackdropStop {
  /** Hex, as everywhere else in a theme file. Omitted means the derived colour. */
  color?: string
  /** CSS position, `<x>% <y>%`. */
  at?: string
  /** CSS size, `<w>% <h>%`. */
  size?: string
}

/**
 * How the gradients are arranged, or 'desktop' for a theme that would rather
 * show whatever is behind the window than paint anything itself.
 */
export type BackdropStyle = 'desktop' | 'mesh' | 'corner' | 'wash' | 'aurora'

/**
 * The gradient a glass theme paints behind itself, so the look does not depend
 * on the desktop showing through. Namespaced like `helios.glass`, and ignored
 * by VS Code for the same reason.
 */
export interface BackdropSpec {
  style?: BackdropStyle
  /** Alpha of the strongest layer; the rest are scaled from it. */
  intensity?: number
  /** Replaces the derived palette outright. */
  stops?: BackdropStop[]
}

export interface VSCodeTheme {
  name?: string
  'helios.glass'?: Partial<GlassSpec>
  'helios.backdrop'?: BackdropSpec
  /** Absent more often than not; the resolver falls back to luminance. */
  type?: string
  /** A sibling theme file this one layers on top of. */
  include?: string
  colors?: Record<string, string>
  tokenColors?: TokenColor[]
}

export interface Rgb {
  r: number
  g: number
  b: number
  /** 0–1. Themes use 8-digit hex freely, including for whole backgrounds. */
  a: number
}

/**
 * JSON with comments and trailing commas, which is what VS Code accepts and
 * therefore what theme authors write. Strings are tracked so a `//` inside one
 * is not mistaken for a comment.
 */
export function parseJsonc(text: string): unknown {
  let out = ''
  let i = 0
  while (i < text.length) {
    const ch = text[i]
    if (ch === '"') {
      const start = i
      i++
      while (i < text.length) {
        if (text[i] === '\\') i += 2
        else if (text[i] === '"') {
          i++
          break
        } else i++
      }
      out += text.slice(start, i)
      continue
    }
    if (ch === '/' && text[i + 1] === '/') {
      while (i < text.length && text[i] !== '\n') i++
      continue
    }
    if (ch === '/' && text[i + 1] === '*') {
      i += 2
      while (i < text.length && !(text[i] === '*' && text[i + 1] === '/')) i++
      i += 2
      continue
    }
    out += ch
    i++
  }
  return JSON.parse(out.replace(/,(\s*[}\]])/g, '$1'))
}

const HEX = /^#([0-9a-f]{3,8})$/i

/** Parses the hex forms VS Code allows. Anything else is not a colour. */
export function parseColor(value: string | undefined): Rgb | null {
  if (!value) return null
  const match = HEX.exec(value.trim())
  if (!match) return null
  const hex = match[1] as string
  const expand = (s: string): number => parseInt(s.length === 1 ? s + s : s, 16)
  if (hex.length === 3 || hex.length === 4) {
    return {
      r: expand(hex[0] as string),
      g: expand(hex[1] as string),
      b: expand(hex[2] as string),
      a: hex.length === 4 ? expand(hex[3] as string) / 255 : 1,
    }
  }
  if (hex.length === 6 || hex.length === 8) {
    return {
      r: parseInt(hex.slice(0, 2), 16),
      g: parseInt(hex.slice(2, 4), 16),
      b: parseInt(hex.slice(4, 6), 16),
      a: hex.length === 8 ? parseInt(hex.slice(6, 8), 16) / 255 : 1,
    }
  }
  return null
}

export function rgb(r: number, g: number, b: number, a = 1): Rgb {
  return { r, g, b, a }
}

const clamp = (n: number): number => Math.max(0, Math.min(255, Math.round(n)))

/** Flattens a translucent colour onto an opaque one. */
export function composite(front: Rgb, back: Rgb): Rgb {
  if (front.a >= 1) return { ...front, a: 1 }
  const t = front.a
  return {
    r: clamp(front.r * t + back.r * (1 - t)),
    g: clamp(front.g * t + back.g * (1 - t)),
    b: clamp(front.b * t + back.b * (1 - t)),
    a: 1,
  }
}

/** `t` is how far to travel from `a` towards `b`. */
export function mix(a: Rgb, b: Rgb, t: number): Rgb {
  return {
    r: clamp(a.r + (b.r - a.r) * t),
    g: clamp(a.g + (b.g - a.g) * t),
    b: clamp(a.b + (b.b - a.b) * t),
    a: 1,
  }
}

/** WCAG relative luminance, 0–1. */
export function luminance(c: Rgb): number {
  const channel = (v: number): number => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
  }
  return 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b)
}

export function contrast(a: Rgb, b: Rgb): number {
  const la = luminance(a)
  const lb = luminance(b)
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

/** Whichever candidate reads best against `background`. */
export function pickReadable(background: Rgb, candidates: Rgb[]): Rgb {
  let best = candidates[0] as Rgb
  let bestRatio = -1
  for (const candidate of candidates) {
    const ratio = contrast(background, candidate)
    if (ratio > bestRatio) {
      best = candidate
      bestRatio = ratio
    }
  }
  return best
}

/**
 * Nudges a colour towards white or black — whichever the background is not —
 * until it stands off that background by `target`.
 *
 * Needed because a theme's palette is chosen against the one background VS Code
 * puts it on, and Helios puts the same colours on others: a terminal yellow
 * that reads fine as terminal output is close to invisible as a status dot on a
 * light theme's panel. Travelling towards the extreme rather than recolouring
 * keeps the hue, so the result still looks like the theme.
 */
export function ensureContrast(colour: Rgb, background: Rgb, target: number): Rgb {
  if (contrast(colour, background) >= target) return colour
  const toward = luminance(background) > 0.4 ? rgb(0, 0, 0) : rgb(255, 255, 255)
  let result = colour
  for (let t = 0.05; t <= 1.0001; t += 0.05) {
    result = mix(colour, toward, t)
    if (contrast(result, background) >= target) break
  }
  return result
}

/**
 * Recolours to a fixed lightness, keeping the hue and raising the saturation to
 * at least `minSaturation`.
 *
 * A theme's palette is chosen to be read as text, which makes it pale: most
 * dark themes state their blue somewhere around 75% lightness. Painted at
 * partial alpha over a near-black background that pale blue composites to grey,
 * and the hue the stop was chosen for disappears. Pinning the lightness where
 * the colour is at its most saturated is what keeps a blob recognisably blue.
 */
export function withLightness(c: Rgb, lightness: number, minSaturation: number): Rgb {
  const r = c.r / 255
  const g = c.g / 255
  const b = c.b / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const l = (max + min) / 2
  const delta = max - min

  let h = 0
  if (delta > 0) {
    if (max === r) h = ((g - b) / delta) % 6
    else if (max === g) h = (b - r) / delta + 2
    else h = (r - g) / delta + 4
    h *= 60
    if (h < 0) h += 360
  }
  const s = delta === 0 ? 0 : delta / (1 - Math.abs(2 * l - 1))

  return fromHsl(h, Math.max(s, minSaturation), lightness)
}

function fromHsl(h: number, s: number, l: number): Rgb {
  const chroma = (1 - Math.abs(2 * l - 1)) * s
  const x = chroma * (1 - Math.abs(((h / 60) % 2) - 1))
  const m = l - chroma / 2
  const sextant = Math.floor(h / 60) % 6
  const [r, g, b] = (
    [
      [chroma, x, 0],
      [x, chroma, 0],
      [0, chroma, x],
      [0, x, chroma],
      [x, 0, chroma],
      [chroma, 0, x],
    ] as [number, number, number][]
  )[sextant] as [number, number, number]
  return { r: clamp((r + m) * 255), g: clamp((g + m) * 255), b: clamp((b + m) * 255), a: 1 }
}

export function toHex(c: Rgb): string {
  const pair = (n: number): string => clamp(n).toString(16).padStart(2, '0')
  return `#${pair(c.r)}${pair(c.g)}${pair(c.b)}`
}

/**
 * Eight-digit hex, for consumers that parse colours themselves rather than
 * handing them to CSS. xterm is the one that matters here: it does not
 * understand the modern `rgb(r g b / a)` form and silently falls back to black,
 * which is how a translucent terminal ends up an opaque one.
 */
export function toHexAlpha(c: Rgb, alpha: number): string {
  const pair = (n: number): string => clamp(n).toString(16).padStart(2, '0')
  return `${toHex(c)}${pair(Math.round(alpha * 255))}`
}

export function toRgba(c: Rgb, alpha: number): string {
  return `rgb(${clamp(c.r)} ${clamp(c.g)} ${clamp(c.b)} / ${Math.round(alpha * 100)}%)`
}
