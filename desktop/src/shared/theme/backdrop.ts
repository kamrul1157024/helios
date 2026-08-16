/**
 * The backdrop a glass theme paints behind itself.
 *
 * Glass began as the macOS window material, which means the wallpaper decides
 * what the app looks like and no other platform gets anything at all. A theme
 * that carries its own backdrop answers both: the window stays opaque, the
 * translucent surfaces have something of the app's own to sit on, and it looks
 * the same everywhere.
 *
 * Static by design. The value is a plain list of gradients, so the compositor
 * paints it once and never again — an animated backdrop would run a repaint
 * behind every session for as long as the window is open.
 */

import { withLightness, parseColor, toRgba, type BackdropSpec, type BackdropStyle, type Rgb } from './vscode.ts'

/**
 * A blob or a band, and how strongly it paints relative to the intensity.
 *
 * `colour` indexes the palette, so two layers may share one colour: a style
 * with more layers than the four derived colours repeats rather than inventing.
 */
type Layer =
  | { kind: 'radial'; at: string; size: string; colour: number; weight: number }
  | { kind: 'linear'; angle: string; from: string; peak: string; to: string; colour: number; weight: number }

/**
 * Two opposing corners carry the mesh and the other two fill the diagonal,
 * which is what keeps it from reading as a plain vignette. The rest are the
 * quieter arrangements of the same idea.
 */
const STYLES: Record<Exclude<BackdropStyle, 'desktop' | 'image'>, Layer[]> = {
  mesh: [
    { kind: 'radial', at: '12% 8%', size: '70% 60%', colour: 0, weight: 1 },
    { kind: 'radial', at: '88% 18%', size: '60% 55%', colour: 1, weight: 0.5 },
    { kind: 'radial', at: '72% 95%', size: '75% 65%', colour: 2, weight: 0.8 },
    { kind: 'radial', at: '25% 88%', size: '60% 50%', colour: 3, weight: 0.5 },
  ],
  corner: [
    { kind: 'radial', at: '8% 0%', size: '120% 90%', colour: 0, weight: 1 },
    { kind: 'radial', at: '98% 100%', size: '110% 85%', colour: 1, weight: 0.55 },
  ],
  wash: [
    { kind: 'radial', at: '50% -10%', size: '150% 70%', colour: 0, weight: 0.7 },
    { kind: 'radial', at: '50% 110%', size: '140% 60%', colour: 2, weight: 0.35 },
  ],
  aurora: [
    { kind: 'linear', angle: '115deg', from: '20%', peak: '38%', to: '55%', colour: 0, weight: 0.9 },
    { kind: 'linear', angle: '115deg', from: '45%', peak: '62%', to: '78%', colour: 1, weight: 0.5 },
    { kind: 'radial', at: '50% 120%', size: '100% 100%', colour: 2, weight: 0.75 },
  ],
}

export const BACKDROP_STYLES = Object.keys(STYLES) as Exclude<BackdropStyle, 'desktop' | 'image'>[]

/** The scheme the main process serves imported images on. */
export const MEDIA_SCHEME = 'helios-backdrop'

/** A bare file name: no separators, no `..`, and an extension we serve. */
const IMAGE_NAME = /^[\w.-]+\.(?:png|jpe?g|webp)$/i

/* Below the floor the backdrop is a waste of a layer; above the ceiling it
   reaches the text, which is the one thing the surfaces above it are for. */
export const MIN_INTENSITY = 0.05
export const MAX_INTENSITY = 0.6
export const DEFAULT_INTENSITY = 0.45

/**
 * A pair of percentages and nothing else.
 *
 * These strings come from a hand-written theme file and end up inside an inline
 * style, so anything that is not plainly a position is dropped rather than
 * passed through — `at` is the one field in the format that is not a colour the
 * parser has already vetted.
 */
const POSITION = /^-?\d{1,3}(?:\.\d+)?% -?\d{1,3}(?:\.\d+)?%$/

function position(value: string | undefined, fallback: string): string {
  return value && POSITION.test(value.trim()) ? value.trim() : fallback
}

export function clampIntensity(value: number | undefined): number {
  if (!Number.isFinite(value)) return DEFAULT_INTENSITY
  return Math.max(MIN_INTENSITY, Math.min(MAX_INTENSITY, value as number))
}

/* Zero is a legitimate answer here — a theme may want its gradient sharp, and
   on macOS the window material has already blurred the desktop. The ceiling is
   where more radius stops changing anything a viewer can see. */
export const MAX_BLUR = 48
export const DEFAULT_BLUR = 24

export function clampBlur(value: number | undefined): number {
  if (!Number.isFinite(value)) return DEFAULT_BLUR
  return Math.max(0, Math.min(MAX_BLUR, Math.round(value as number)))
}

/** 'desktop' for a theme that asks for the OS material, or has asked for nothing. */
export function styleOf(spec: BackdropSpec | undefined): BackdropStyle {
  if (!spec) return 'desktop'
  if (spec.style === 'desktop') return 'desktop'
  // An image style with no usable image left would otherwise be a blank
  // window; falling back to the gradient keeps something behind the glass.
  if (spec.style === 'image') return imageName(spec) ? 'image' : 'mesh'
  return spec.style && spec.style in STYLES ? spec.style : 'mesh'
}

/** The image a spec names, or null where it names nothing we would serve. */
export function imageName(spec: BackdropSpec | undefined): string | null {
  const name = spec?.image?.trim()
  return name && IMAGE_NAME.test(name) ? name : null
}

/**
 * Two lightnesses per mode: the one a layer is drawn at when it carries the
 * composition, and the lighter one for those filling in behind it. Both sit
 * further from the background in a light theme, where a colour has white to
 * stand against rather than near-black.
 */
const LEVELS = {
  dark: { strong: 0.42, soft: 0.62 },
  light: { strong: 0.5, soft: 0.6 },
}

/**
 * The four colours a theme gets when it asks for a backdrop without naming any.
 *
 * The primary twice, at both lightnesses, so the result reads as one palette
 * lit from two sides rather than two themes fighting; magenta and cyan fill the
 * other diagonal. Those two come from the terminal set, which every theme
 * states an opinion about, unlike the chart or badge colours.
 */
export function derivedStops(primary: Rgb, magenta: Rgb, cyan: Rgb, isDark: boolean): Rgb[] {
  const level = isDark ? LEVELS.dark : LEVELS.light
  return [
    withLightness(primary, level.strong, 0.55),
    withLightness(primary, level.soft, 0.55),
    withLightness(magenta, level.strong, 0.5),
    withLightness(cyan, level.soft, 0.45),
  ]
}

/**
 * The `background` shorthand for the backdrop layer: the style's gradients, and
 * the theme's own surface underneath as the bed they fade into.
 *
 * Empty for 'desktop', which is the theme saying it wants whatever is behind
 * the window rather than anything of its own.
 */
export function backdropValue(spec: BackdropSpec, base: Rgb, derived: Rgb[]): string {
  const style = styleOf(spec)
  if (style === 'desktop') return ''

  // An image is laid under a scrim of the theme's own surface rather than shown
  // raw. A photograph behind a transcript is a contrast problem, and intensity
  // is how far towards the theme it is pulled — the same slider, doing the
  // same job it does for a gradient.
  const image = style === 'image' ? imageName(spec) : null
  if (image) {
    const scrim = toRgba(base, clampIntensity(spec.intensity))
    return [
      `linear-gradient(${scrim}, ${scrim})`,
      `url('${MEDIA_SCHEME}://media/${encodeURIComponent(image)}') center / cover no-repeat`,
      toRgba(base, 1),
    ].join(', ')
  }

  const intensity = clampIntensity(spec.intensity)
  const declared = spec.stops?.length ? spec.stops : null
  const palette: (Rgb | null)[] = declared
    ? declared.map((stop) => (stop.color ? parseColor(stop.color) : null))
    : derived

  const layers: string[] = []
  for (const layer of STYLES[style as keyof typeof STYLES]) {
    // A style may have more layers than the palette has colours, and a stop
    // whose colour does not parse is left out rather than guessed at: one
    // missing blob is a quieter failure than a grey one in the wrong place.
    const colour = palette[layer.colour % Math.max(palette.length, 1)]
    if (!colour) continue
    const alpha = Math.min(1, intensity * layer.weight)
    const paint = toRgba(colour, alpha)
    if (layer.kind === 'linear') {
      layers.push(
        `linear-gradient(${layer.angle}, transparent ${layer.from}, ${paint} ${layer.peak}, transparent ${layer.to})`,
      )
      continue
    }
    const stop = declared?.[layer.colour]
    layers.push(
      `radial-gradient(${position(stop?.size, layer.size)} at ${position(stop?.at, layer.at)}, ${paint} 0%, transparent 60%)`,
    )
  }

  return [...layers, toRgba(base, 1)].join(', ')
}
