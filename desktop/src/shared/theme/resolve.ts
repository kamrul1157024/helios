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
}

const FALLBACK_BG: Record<ThemeMode, Rgb> = { dark: rgb(0x11, 0x13, 0x18), light: rgb(0xff, 0xff, 0xff) }
const FALLBACK_FG: Record<ThemeMode, Rgb> = { dark: rgb(0xe2, 0xe2, 0xe6), light: rgb(0x1f, 0x1f, 0x1f) }

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
    for (const key of spec.keys) {
      const candidate = colour(key)
      if (!candidate) continue
      // Prefer a key that already works over nudging one that does not.
      if (spec.minContrast && contrast(candidate, against) < spec.minContrast) continue
      value = candidate
      break
    }
    const chosen = value ?? spec.derive(ctx)
    const final = spec.minContrast ? ensureContrast(chosen, against, spec.minContrast) : chosen
    resolved.set(name, final)
    vars[`--${name}`] = toHex(final)
  }

  const tokens = indexTokenColors(theme, bg)
  vars['--syn-fg'] = toHex(fg)
  for (const [role, candidates] of Object.entries(SYNTAX_SCOPES)) {
    let value: Rgb | null = null
    for (const scope of candidates) {
      value = lookupScope(tokens, scope)
      if (value) break
    }
    const fallback = SYNTAX_FALLBACK[role]
    vars[`--syn-${role}`] = toHex(value ?? (fallback ? fallback(ansi, fg, bg) : fg))
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

  const terminalBg = colour('terminal.background') ?? bg
  const ansiTheme = {
    // Eight-digit hex rather than rgb(): xterm parses this itself, and does
    // not accept the slash form.
    background: glass ? toHexAlpha(terminalBg, glass.terminal) : toHex(terminalBg),
    foreground: toHex(colour('terminal.foreground') ?? fg),
    cursor: toHex(colour('terminalCursor.foreground') ?? colour('editorCursor.foreground') ?? fg),
    selectionBackground: toHex(
      colour('terminal.selectionBackground') ?? colour('editor.selectionBackground') ?? mix(terminalBg, fg, 0.25),
    ),
  } as XtermTheme
  for (const name of ANSI_NAMES) ansiTheme[name] = toHex(ansi[name])

  return { id, name: options.name ?? theme.name ?? id, mode, vars, ansi: ansiTheme, glass }
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
    colors: { ...(parent.colors ?? {}), ...(child.colors ?? {}) },
    tokenColors: [...(parent.tokenColors ?? []), ...(child.tokenColors ?? [])],
  }
}
