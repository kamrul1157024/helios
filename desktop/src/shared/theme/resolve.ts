/**
 * Turns a VS Code colour theme into the CSS variables and terminal palette the
 * app draws with.
 *
 * Pure: no filesystem, no Electron. Reading and merging theme files is the
 * registry's job in the main process; everything here is a function of the
 * parsed object, which is what makes the awkward cases testable.
 */

import {
  ANSI_NAMES,
  DEFAULT_ANSI,
  SYNTAX_FALLBACK,
  SYNTAX_SCOPES,
  UI_ROLES,
  ansiKey,
  overlayVars,
  type AnsiName,
  type DeriveContext,
} from './mapping.ts'
import {
  DEFAULT_BLUR,
  backdropValue,
  clampBlur,
  clampIntensity,
  derivedStops,
  imageName,
  styleOf,
} from './backdrop.ts'
import {
  composite,
  contrast,
  toRgba,
  toHexAlpha,
  ensureContrast,
  luminance,
  mix,
  parseColor,
  rgb,
  toHex,
  type BackdropStyle,
  type GlassSpec,
  type Rgb,
  type VSCodeTheme,
} from './vscode.ts'

export type ThemeMode = 'dark' | 'light'

export interface XtermTheme extends Record<AnsiName, string> {
  background: string
  foreground: string
  cursor: string
  selectionBackground: string
}

export interface HeliosTheme {
  id: string
  name: string
  mode: ThemeMode
  /** CSS custom properties, including the leading `--`. */
  vars: Record<string, string>
  ansi: XtermTheme
  /**
   * Set only by a theme that asks to be translucent. Null is the ordinary case
   * and means every surface stays opaque.
   */
  glass: GlassSpec | null
  /**
   * The gradient the theme paints behind its glass. Null is the theme asking
   * for whatever the OS puts behind the window instead, which is the macOS
   * material or, anywhere else, nothing.
   */
  backdrop: {
    style: BackdropStyle
    intensity: number
    /** Colours the theme named itself, or null where they are derived. */
    stops: string[] | null
    /** File name of the image behind an 'image' style, null for the rest. */
    image: string | null
  } | null
  /**
   * The colours a backdrop of this theme is drawn from, whether or not one is
   * showing — the picker draws its swatches from them, and a swatch has to be
   * able to show a style the theme is not currently using.
   */
  backdropPalette: string[]
  /**
   * How far the glass surfaces blur what is behind them, in px. Zero for an
   * opaque theme, and for a glass theme that asked for none.
   */
  backdropBlur: number
}

const FALLBACK_BG: Record<ThemeMode, Rgb> = { dark: rgb(0x11, 0x13, 0x18), light: rgb(0xff, 0xff, 0xff) }
const FALLBACK_FG: Record<ThemeMode, Rgb> = { dark: rgb(0xe2, 0xe2, 0xe6), light: rgb(0x1f, 0x1f, 0x1f) }

/**
 * The four palette entries left exactly as the theme states them.
 *
 * The other twelve are text: a CLI writes its warnings in yellow and its
 * timestamps in bright black, and a light theme's yellow on a white terminal
 * measures around 2:1. These four are the poles, and a program that asks for
 * them is usually asking for a fill or for the inverse of one — forcing
 * `brightWhite` to stand off a white terminal would turn white-on-blue text
 * black.
 */
const TERMINAL_POLES = new Set<AnsiName>(['black', 'white', 'brightWhite'])

/** The floor for terminal text, which is dense and small. */
const TERMINAL_CONTRAST = 4.5

const readable = (colour: Rgb, background: Rgb): Rgb => ensureContrast(colour, background, TERMINAL_CONTRAST)

/**
 * `uiTheme` from the extension's `package.json`, for the many themes that omit
 * the top-level `type` field.
 */
export function modeFromUiTheme(uiTheme: string | undefined): ThemeMode | null {
  if (uiTheme === 'vs-dark' || uiTheme === 'hc-black') return 'dark'
  if (uiTheme === 'vs' || uiTheme === 'hc-light') return 'light'
  return null
}

function declaredMode(theme: VSCodeTheme, hint: string | undefined): ThemeMode | null {
  const type = theme.type?.toLowerCase()
  if (type === 'dark' || type === 'hc' || type === 'hc-black') return 'dark'
  if (type === 'light' || type === 'hclight' || type === 'hc-light') return 'light'
  return modeFromUiTheme(hint)
}

function clampAlpha(value: number): number {
  return Number.isFinite(value) ? Math.max(0.25, Math.min(1, value)) : 1
}

/** A theme rule's scope may be a list, a comma-separated string, or both. */
function scopesOf(scope: string | string[] | undefined): string[] {
  if (!scope) return []
  const parts = Array.isArray(scope) ? scope : [scope]
  return parts
    .flatMap((part) => part.split(','))
    .map((part) => part.trim())
    // A descendant selector such as `meta.function entity.name` keys off its
    // last segment, which is the scope actually being coloured.
    .map((part) => part.split(/\s+/).pop() ?? '')
    .filter(Boolean)
}

/**
 * Scope to colour, last rule winning — the same precedence VS Code applies when
 * two rules name the same scope.
 */
function indexTokenColors(theme: VSCodeTheme, bg: Rgb): Map<string, Rgb> {
  const index = new Map<string, Rgb>()
  for (const rule of theme.tokenColors ?? []) {
    const colour = parseColor(rule.settings?.foreground)
    if (!colour) continue
    for (const scope of scopesOf(rule.scope)) index.set(scope, composite(colour, bg))
  }
  return index
}

/** Longest matching prefix: `keyword.control.flow` falls back to `keyword`. */
function lookupScope(index: Map<string, Rgb>, scope: string): Rgb | null {
  let probe = scope
  for (;;) {
    const hit = index.get(probe)
    if (hit) return hit
    const cut = probe.lastIndexOf('.')
    if (cut < 0) return null
    probe = probe.slice(0, cut)
  }
}

export function resolveTheme(
  id: string,
  theme: VSCodeTheme,
  options: { name?: string; uiTheme?: string } = {},
): HeliosTheme {
  const colors = theme.colors ?? {}
  const rawBg = parseColor(colors['editor.background'])

  // Order matters: a theme that states its type is believed, and one that does
  // not is judged by how dark its editor is. Only a theme with neither gets the
  // arbitrary answer.
  const mode: ThemeMode =
    declaredMode(theme, options.uiTheme) ?? (rawBg ? (luminance(rawBg) < 0.4 ? 'dark' : 'light') : 'dark')

  const bg = rawBg ? composite(rawBg, FALLBACK_BG[mode]) : FALLBACK_BG[mode]
  const rawFg = parseColor(colors['editor.foreground']) ?? parseColor(colors['foreground'])
  const fg = rawFg ? composite(rawFg, bg) : FALLBACK_FG[mode]

  const colour = (key: string): Rgb | null => {
    const parsed = parseColor(colors[key])
    return parsed ? composite(parsed, bg) : null
  }

  const ansi = {} as Record<AnsiName, Rgb>
  for (const name of ANSI_NAMES) {
    ansi[name] = colour(ansiKey(name)) ?? (parseColor(DEFAULT_ANSI[mode][name]) as Rgb)
  }

  const resolved = new Map<string, Rgb>()
  /**
   * The same roles before the contrast floor moved them.
   *
   * Only the backdrop reads these. A mesh painted behind glass carries no text,
   * so nudging its colours towards white to make them legible buys nothing and
   * costs the theme the palette it was designed around.
   */
  const stated = new Map<string, Rgb>()
  const ctx: DeriveContext = {
    bg,
    fg,
    isDark: mode === 'dark',
    ladder: (t) => mix(bg, fg, t),
    ansi,
    role: (name) => resolved.get(name) ?? bg,
  }

  const vars: Record<string, string> = {}
  for (const [name, spec] of UI_ROLES) {
    const against = spec.against ? spec.against(ctx) : bg
    let value: Rgb | null = null
    let declared: Rgb | null = null
    for (const key of spec.keys) {
      const candidate = colour(key)
      if (!candidate) continue
      declared ??= candidate
      // Prefer a key that already works over nudging one that does not.
      if (spec.minContrast && contrast(candidate, against) < spec.minContrast) continue
      value = candidate
      break
    }
    const chosen = value ?? spec.derive(ctx)
    const final = spec.minContrast ? ensureContrast(chosen, against, spec.minContrast) : chosen
    resolved.set(name, final)
    stated.set(name, declared ?? chosen)
    vars[`--${name}`] = toHex(final)
  }

  // A theme's token colours are chosen against its editor background, and the
  // app puts them on a code block that is a rung further up the surface ladder.
  // Held to the same floor as the rest of the UI, with comments allowed to
  // recede further because receding is what they are for.
  const tokens = indexTokenColors(theme, bg)
  const codeBg = resolved.get('code-bg') ?? bg
  vars['--syn-fg'] = toHex(ensureContrast(fg, codeBg, 4.5))
  for (const [role, candidates] of Object.entries(SYNTAX_SCOPES)) {
    let value: Rgb | null = null
    for (const scope of candidates) {
      value = lookupScope(tokens, scope)
      if (value) break
    }
    const fallback = SYNTAX_FALLBACK[role]
    const chosen = value ?? (fallback ? fallback(ansi, fg, bg) : fg)
    vars[`--syn-${role}`] = toHex(ensureContrast(chosen, codeBg, role === 'comment' ? 3 : 3.5))
  }

  Object.assign(vars, overlayVars(mode === 'dark'))
  vars['--color-scheme'] = mode

  // Opacities the theme asked for, clamped: a surface at zero is a pane of
  // nothing, and text on it is unreadable however good the wallpaper.
  const declared = theme['helios.glass']
  const glass: GlassSpec | null = declared
    ? {
        sidebar: clampAlpha(declared.sidebar ?? 0.6),
        panel: clampAlpha(declared.panel ?? 0.7),
        terminal: clampAlpha(declared.terminal ?? 0.7),
      }
    : null

  if (glass) {
    // Emitted beside the opaque values rather than replacing them. The opaque
    // ones stay the fallback for a platform with no backdrop to show, and for
    // the surfaces that stay solid whatever the theme says.
    vars['--glass-sidebar'] = toRgba(resolved.get('surface-low') ?? bg, glass.sidebar)
    vars['--glass-panel'] = toRgba(resolved.get('surface-low') ?? bg, glass.panel)
    vars['--glass-container'] = toRgba(resolved.get('surface-container') ?? bg, glass.panel)
  }

  // Only meaningful under glass: the backdrop is what the translucent surfaces
  // are translucent onto, and painting one behind an opaque app would be work
  // no pixel ever shows.
  const palette = derivedStops(stated.get('primary') ?? bg, ansi.magenta, ansi.cyan, mode === 'dark')
  const spec = glass ? theme['helios.backdrop'] : undefined
  const style = styleOf(spec)

  // The frosting, which is a property of the glass rather than of the backdrop:
  // a window showing the desktop is blurred by the same rule as one showing a
  // gradient. It starts at zero for the desktop only because the OS material
  // has already blurred what is behind, and frosting it twice is just haze.
  const blur = glass ? clampBlur(spec?.blur ?? (style === 'desktop' ? 0 : DEFAULT_BLUR)) : 0
  if (blur > 0) {
    // Saturation rises with the radius because blurring towards an average
    // pulls the colour out of whatever is behind.
    vars['--glass-frost'] = `blur(${blur}px) saturate(1.5)`
    // Cards are a sheet on a sheet; blurring them as hard again flattens the
    // difference between the two.
    vars['--glass-frost-soft'] = `blur(${Math.round(blur / 2)}px)`
  }
  let backdrop: HeliosTheme['backdrop'] = null
  if (spec && style !== 'desktop') {
    vars['--backdrop'] = backdropValue(spec, resolved.get('surface') ?? bg, palette)
    // Normalised to plain hex on the way out: the picker puts these in a colour
    // input, which takes nothing else.
    const named = spec.stops?.map((stop) => parseColor(stop.color ?? '')).filter((c): c is Rgb => c !== null)
    backdrop = {
      style,
      intensity: clampIntensity(spec.intensity),
      stops: named?.length ? named.map(toHex) : null,
      image: style === 'image' ? imageName(spec) : null,
    }
  }

  const terminalBg = colour('terminal.background') ?? bg
  const ansiTheme = {
    // Eight-digit hex rather than rgb(): xterm parses this itself, and does
    // not accept the slash form.
    background: glass ? toHexAlpha(terminalBg, glass.terminal) : toHex(terminalBg),
    foreground: toHex(readable(colour('terminal.foreground') ?? fg, terminalBg)),
    // A caret is a shape rather than a word, so it is held to the non-text
    // floor — but it still has to be findable.
    cursor: toHex(
      ensureContrast(colour('terminalCursor.foreground') ?? colour('editorCursor.foreground') ?? fg, terminalBg, 3),
    ),
    selectionBackground: toHex(
      colour('terminal.selectionBackground') ?? colour('editor.selectionBackground') ?? mix(terminalBg, fg, 0.25),
    ),
  } as XtermTheme
  for (const name of ANSI_NAMES) {
    ansiTheme[name] = toHex(TERMINAL_POLES.has(name) ? ansi[name] : readable(ansi[name], terminalBg))
  }

  return {
    id,
    name: options.name ?? theme.name ?? id,
    mode,
    vars,
    ansi: ansiTheme,
    glass,
    backdrop,
    backdropPalette: palette.map(toHex),
    backdropBlur: blur,
  }
}

/**
 * Layers a theme over the one its `include` names. Child keys win; the parent
 * supplies everything the child left out.
 */
export function mergeThemes(parent: VSCodeTheme, child: VSCodeTheme): VSCodeTheme {
  return {
    name: child.name ?? parent.name,
    type: child.type ?? parent.type,
    'helios.glass': child['helios.glass'] ?? parent['helios.glass'],
    'helios.backdrop': child['helios.backdrop'] ?? parent['helios.backdrop'],
    colors: { ...(parent.colors ?? {}), ...(child.colors ?? {}) },
    tokenColors: [...(parent.tokenColors ?? []), ...(child.tokenColors ?? [])],
  }
}
