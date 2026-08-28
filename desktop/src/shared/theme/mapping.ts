/**
 * Which VS Code colour keys and TextMate scopes feed which part of Helios.
 *
 * Every entry is a priority list plus a derivation, because a theme that sets
 * the key is the lucky case. The derivation is what makes a theme with nothing
 * but a background and a foreground still produce a usable app.
 */

import { ensureContrast, mix, pickReadable, toHex, toRgba, type Rgb } from './vscode.ts'

export const ANSI_NAMES = [
  'black',
  'red',
  'green',
  'yellow',
  'blue',
  'magenta',
  'cyan',
  'white',
  'brightBlack',
  'brightRed',
  'brightGreen',
  'brightYellow',
  'brightBlue',
  'brightMagenta',
  'brightCyan',
  'brightWhite',
] as const

export type AnsiName = (typeof ANSI_NAMES)[number]

/** `terminal.ansiBrightBlack` and friends; the key is the name capitalised. */
export function ansiKey(name: AnsiName): string {
  return `terminal.ansi${name[0]?.toUpperCase()}${name.slice(1)}`
}

/**
 * Used when a theme sets no `terminal.ansi*` keys at all. The dark palette is
 * the one the app shipped with before themes existed, so a theme that says
 * nothing about the terminal leaves it looking as it always did.
 */
export const DEFAULT_ANSI: Record<'dark' | 'light', Record<AnsiName, string>> = {
  dark: {
    black: '#1c1c24',
    red: '#ff6b6b',
    green: '#7ddc8a',
    yellow: '#ffb03a',
    blue: '#6aa9ff',
    magenta: '#c58cff',
    cyan: '#5fd7d7',
    white: '#d8d8e0',
    brightBlack: '#5a5a68',
    brightRed: '#ff8f8f',
    brightGreen: '#a4eaad',
    brightYellow: '#ffca70',
    brightBlue: '#9cc5ff',
    brightMagenta: '#dcb4ff',
    brightCyan: '#8ee8e8',
    brightWhite: '#ffffff',
  },
  light: {
    black: '#24292e',
    red: '#d1383d',
    green: '#2d8a4e',
    yellow: '#a06b00',
    blue: '#1f6feb',
    magenta: '#8250df',
    cyan: '#0f7c8a',
    white: '#6e7781',
    brightBlack: '#57606a',
    brightRed: '#cf222e',
    brightGreen: '#1a7f37',
    brightYellow: '#7d4e00',
    brightBlue: '#0969da',
    brightMagenta: '#6639ba',
    brightCyan: '#106b75',
    brightWhite: '#24292f',
  },
}

/**
 * Syntax roles, and the TextMate scopes that stand for them, most specific
 * first. Roles are named for what a token means so that one resolution feeds
 * both highlight.js in the transcript and CodeMirror in the editor.
 */
export const SYNTAX_SCOPES: Record<string, string[]> = {
  keyword: ['keyword.control', 'keyword', 'storage.type', 'storage.modifier'],
  comment: ['comment', 'punctuation.definition.comment'],
  string: ['string.quoted', 'string'],
  regexp: ['string.regexp', 'string'],
  number: ['constant.numeric', 'constant'],
  literal: ['constant.language.boolean', 'constant.language', 'constant'],
  constant: ['constant.language', 'constant.character', 'constant.other', 'constant'],
  function: ['entity.name.function', 'support.function', 'meta.function-call'],
  type: ['entity.name.type', 'entity.name.class', 'support.type', 'support.class'],
  variable: ['variable.other.readwrite', 'variable.other', 'variable'],
  property: ['variable.other.property', 'support.variable.property', 'meta.object-literal.key'],
  attribute: ['entity.other.attribute-name'],
  tag: ['entity.name.tag', 'meta.tag'],
  meta: ['meta.preprocessor', 'keyword.other.preprocessor', 'markup.heading', 'entity.name.section'],
  operator: ['keyword.operator'],
  punctuation: ['punctuation.separator', 'punctuation'],
}

/**
 * What a syntax role falls back to when the theme's `tokenColors` say nothing
 * about it — which includes themes that ship none at all. The terminal palette
 * is the right source: it is the one place every theme states what it thinks
 * "red" and "green" look like.
 */
export const SYNTAX_FALLBACK: Record<string, (ansi: Record<AnsiName, Rgb>, fg: Rgb, bg: Rgb) => Rgb> = {
  keyword: (a) => a.magenta,
  comment: (_a, fg, bg) => mix(fg, bg, 0.55),
  string: (a) => a.green,
  regexp: (a) => a.green,
  number: (a) => a.yellow,
  literal: (a) => a.cyan,
  constant: (a) => a.cyan,
  function: (a) => a.blue,
  type: (a) => a.brightYellow,
  variable: (_a, fg) => fg,
  property: (a) => a.cyan,
  attribute: (a) => a.green,
  tag: (a) => a.red,
  meta: (a) => a.blue,
  operator: (_a, fg) => fg,
  punctuation: (_a, fg) => fg,
}

export interface DeriveContext {
  /** `editor.background`, the colour everything else is measured against. */
  bg: Rgb
  /** `editor.foreground`. */
  fg: Rgb
  isDark: boolean
  /** A step up the tonal surface ladder: `bg` mixed `t` of the way to `fg`. */
  ladder(t: number): Rgb
  ansi: Record<AnsiName, Rgb>
  /** A UI role resolved earlier in the list. */
  role(name: string): Rgb
}

export interface RoleSpec {
  /** VS Code colour keys to try, in priority order. */
  keys: string[]
  derive(ctx: DeriveContext): Rgb
  /**
   * Reject a key whose colour does not stand off the background by at least
   * this ratio, and carry on down the list. Border keys in particular are
   * routinely set to something like `#ffffff1a`, which flattens to almost the
   * background — faithful to the theme, and invisible in a UI that leans on
   * borders more than VS Code does.
   */
  minContrast?: number
  /**
   * What `minContrast` is measured against. Defaults to the editor background;
   * an `on-` role names the container it is written on instead, which is the
   * only surface its legibility depends on.
   */
  against?(ctx: DeriveContext): Rgb
}

/**
 * The hardest background a role has to survive.
 *
 * `editor.background` is the wrong thing to measure against, and measuring
 * against it was how sixteen of the seventeen bundled themes shipped text below
 * the WCAG floor. The app draws on a ladder of raised surfaces, and every rung
 * is mixed towards the foreground — so the top rung eats the most contrast and
 * is the only one worth checking. Clearing it clears every rung below.
 */
const onRaised = (c: DeriveContext): Rgb => c.role('surface-highest')

/**
 * The WCAG AA floor for text below 18px.
 *
 * The status and error colours were held to 3:1 on the theory that they are
 * drawn as dots. They are not: they are the "Active" label on a session row and
 * the tick on every tool result, at 10.5px and 11.5px. A dot at 3:1 beside a
 * word at 3:1 means the word is the part that fails.
 */
const SMALL_TEXT = 4.5

/**
 * The UI roles, in resolution order — later entries may read earlier ones
 * through `ctx.role`.
 *
 * The surface ladder is derived rather than read from `sideBar.background` and
 * friends on purpose. Those keys are not ordered by elevation: plenty of dark
 * themes make the sidebar darker than the editor, which would invert the ladder
 * and put the app's raised surfaces below its base one. Mixing towards the
 * foreground always climbs, in a light theme as well as a dark one.
 */
export const UI_ROLES: [string, RoleSpec][] = [
  ['surface', { keys: ['editor.background'], derive: (c) => c.bg }],
  ['surface-low', { keys: [], derive: (c) => c.ladder(0.04) }],
  ['surface-container', { keys: [], derive: (c) => c.ladder(0.08) }],
  ['surface-high', { keys: [], derive: (c) => c.ladder(0.13) }],
  ['surface-highest', { keys: [], derive: (c) => c.ladder(0.19) }],

  [
    'on-surface',
    {
      keys: ['editor.foreground', 'foreground'],
      minContrast: 4.5,
      against: onRaised,
      derive: (c) => c.fg,
    },
  ],
  [
    'on-surface-variant',
    {
      keys: ['descriptionForeground'],
      // Secondary, but still text, and the app sets it at 10.5px on the
      // session rows and 11.5px on every tool summary.
      minContrast: SMALL_TEXT,
      against: onRaised,
      derive: (c) => mix(c.fg, c.bg, 0.28),
    },
  ],
  [
    'outline-variant',
    {
      keys: ['panel.border', 'editorGroup.border', 'widget.border'],
      minContrast: 1.15,
      derive: (c) => mix(c.fg, c.bg, 0.78),
    },
  ],
  // Grown out of the subtle border rather than read from a key of its own.
  // Border keys point both ways — Nord's is a hair off the background, Dracula
  // uses its accent purple — so taking the same source for both and pushing one
  // further is the only way the emphatic border is reliably the louder of the
  // two, whichever the theme handed us.
  ['outline', { keys: [], derive: (c) => ensureContrast(mix(c.role('outline-variant'), c.fg, 0.35), c.bg, 2.2) }],

  // `textLink.foreground` before `button.background`: the accent is read as
  // text on a surface far more often than it is filled behind one, and a
  // button colour chosen to sit under white lettering is frequently too dark
  // to read against the editor background.
  [
    'primary',
    {
      keys: ['textLink.foreground', 'focusBorder', 'button.background', 'progressBar.background'],
      minContrast: SMALL_TEXT,
      against: onRaised,
      derive: (c) => c.ansi.blue,
    },
  ],
  [
    'on-primary',
    {
      keys: ['button.foreground'],
      minContrast: SMALL_TEXT,
      against: (c) => c.role('primary'),
      derive: (c) => pickReadable(c.role('primary'), [c.bg, c.fg]),
    },
  ],
  [
    'primary-container',
    {
      keys: ['list.activeSelectionBackground', 'editor.selectionBackground'],
      derive: (c) => mix(c.bg, c.role('primary'), c.isDark ? 0.32 : 0.22),
    },
  ],
  [
    'on-primary-container',
    {
      keys: ['list.activeSelectionForeground'],
      minContrast: SMALL_TEXT,
      against: (c) => c.role('primary-container'),
      derive: (c) => pickReadable(c.role('primary-container'), [c.fg, c.bg, mix(c.role('primary'), c.fg, 0.6)]),
    },
  ],

  [
    'error',
    {
      keys: ['errorForeground', 'editorError.foreground', 'list.errorForeground'],
      minContrast: SMALL_TEXT,
      against: onRaised,
      derive: (c) => c.ansi.red,
    },
  ],
  [
    'error-container',
    {
      keys: ['inputValidation.errorBackground'],
      derive: (c) => mix(c.bg, c.role('error'), c.isDark ? 0.34 : 0.24),
    },
  ],
  [
    'on-error-container',
    {
      keys: ['inputValidation.errorForeground'],
      minContrast: SMALL_TEXT,
      against: (c) => c.role('error-container'),
      derive: (c) => pickReadable(c.role('error-container'), [c.fg, c.bg, mix(c.role('error'), c.fg, 0.6)]),
    },
  ],

  // Not `editor.background`, which is already `--surface`: a code block that
  // matches the panel behind it has no edges.
  ['code-bg', { keys: ['textCodeBlock.background'], derive: (c) => c.ladder(0.05) }],
  ['inverse-surface', { keys: [], derive: (c) => c.role('on-surface') }],
  ['inverse-on-surface', { keys: [], derive: (c) => c.bg }],

  // Status vocabulary, taken from the terminal palette rather than from
  // `charts.*`: every theme states an opinion about the sixteen terminal
  // colours, few state one about charts, and the terminal set is guaranteed
  // to be mutually distinct.
  //
  // The floor matters here more than anywhere else. These are drawn as small
  // dots and short labels on a panel, and a terminal yellow picked to sit on a
  // light theme's white background is legible as output and invisible as a
  // six-pixel dot.
  ['s-starting', { keys: [], minContrast: SMALL_TEXT, against: onRaised, derive: (c) => c.ansi.cyan }],
  ['s-active', { keys: [], minContrast: SMALL_TEXT, against: onRaised, derive: (c) => c.ansi.green }],
  ['s-compacting', { keys: [], minContrast: SMALL_TEXT, against: onRaised, derive: (c) => c.ansi.magenta }],
  ['s-waiting', { keys: [], minContrast: SMALL_TEXT, against: onRaised, derive: (c) => c.ansi.yellow }],
  ['s-idle', { keys: [], minContrast: SMALL_TEXT, against: onRaised, derive: (c) => c.ansi.blue }],
  ['s-error', { keys: [], minContrast: SMALL_TEXT, against: onRaised, derive: (c) => c.role('error') }],
  ['s-off', { keys: ['disabledForeground'], minContrast: SMALL_TEXT, against: onRaised, derive: (c) => c.role('outline') }],
]

/**
 * Lifted overlays. A light theme still casts a black shadow, just a fainter
 * one — the same alpha that reads as depth on a dark surface reads as dirt on
 * a white one.
 */
export function overlayVars(isDark: boolean): Record<string, string> {
  const black = { r: 0, g: 0, b: 0, a: 1 }
  const alphas = isDark
    ? { soft: 0.45, strong: 0.65, scrim: 0.4, scrimStrong: 0.6 }
    : { soft: 0.16, strong: 0.26, scrim: 0.28, scrimStrong: 0.42 }
  return {
    '--shadow-soft': toRgba(black, alphas.soft),
    '--shadow-strong': toRgba(black, alphas.strong),
    '--scrim': toRgba(black, alphas.scrim),
    '--scrim-strong': toRgba(black, alphas.scrimStrong),
  }
}

export { toHex }
