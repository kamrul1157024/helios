// The resolver's job is to survive incomplete themes, which is what almost
// every real theme is. These cover the cases that actually turn up in the wild:
// a theme with nothing but a background, one that states no type, one whose
// colours carry alpha, and one with no syntax rules at all.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { test } from 'node:test'

import { resolveTheme, mergeThemes, modeFromUiTheme } from '../src/shared/theme/resolve.ts'
import {
  composite,
  contrast,
  ensureContrast,
  luminance,
  mix,
  parseColor,
  parseJsonc,
  pickReadable,
  toHex,
  type BackdropStyle,
  type Rgb,
  type VSCodeTheme,
} from '../src/shared/theme/vscode.ts'

test('parseJsonc tolerates comments and trailing commas', () => {
  const parsed = parseJsonc(`{
    // a line comment
    "name": "T", /* and a block one */
    "colors": { "editor.background": "#000000", },
  }`) as { name: string; colors: Record<string, string> }
  assert.equal(parsed.name, 'T')
  assert.equal(parsed.colors['editor.background'], '#000000')
})

test('parseJsonc does not treat // inside a string as a comment', () => {
  const parsed = parseJsonc('{"url": "https://example.com/x"}') as { url: string }
  assert.equal(parsed.url, 'https://example.com/x')
})

test('parseColor handles every hex width', () => {
  assert.deepEqual(parseColor('#abc'), { r: 0xaa, g: 0xbb, b: 0xcc, a: 1 })
  assert.equal(parseColor('#ff0000ff')?.a, 1)
  assert.equal(parseColor('#00000000')?.a, 0)
  assert.equal(parseColor('rebeccapurple'), null)
  assert.equal(parseColor(undefined), null)
})

test('composite flattens alpha onto the background', () => {
  const half = { r: 255, g: 255, b: 255, a: 0.5 }
  assert.equal(toHex(composite(half, { r: 0, g: 0, b: 0, a: 1 })), '#808080')
})

test('pickReadable prefers the higher contrast candidate', () => {
  const white = { r: 255, g: 255, b: 255, a: 1 }
  const black = { r: 0, g: 0, b: 0, a: 1 }
  assert.equal(toHex(pickReadable(black, [black, white])), '#ffffff')
  assert.equal(toHex(pickReadable(white, [black, white])), '#000000')
})

test('a theme with only a background and foreground yields a climbing surface ladder', () => {
  const theme = resolveTheme('bare', {
    colors: { 'editor.background': '#101014', 'editor.foreground': '#d8d8e0' },
  })
  const steps = ['--surface', '--surface-low', '--surface-container', '--surface-high', '--surface-highest']
  const lums = steps.map((name) => luminance(parseColor(theme.vars[name]) as never))
  for (let i = 1; i < lums.length; i++) {
    assert.ok((lums[i] as number) > (lums[i - 1] as number), `${steps[i]} must sit above ${steps[i - 1]}`)
  }
})

test('the ladder still climbs on a light theme', () => {
  const theme = resolveTheme('bare-light', {
    type: 'light',
    colors: { 'editor.background': '#ffffff', 'editor.foreground': '#1f1f1f' },
  })
  const low = luminance(parseColor(theme.vars['--surface-low']) as never)
  const high = luminance(parseColor(theme.vars['--surface-highest']) as never)
  assert.ok(low > high, 'a light ladder climbs by getting darker')
  assert.equal(theme.mode, 'light')
  assert.equal(theme.vars['--color-scheme'], 'light')
})

test('mode is inferred from background luminance when the theme declares none', () => {
  assert.equal(resolveTheme('a', { colors: { 'editor.background': '#080d14' } }).mode, 'dark')
  assert.equal(resolveTheme('b', { colors: { 'editor.background': '#fafafa' } }).mode, 'light')
})

test('a declared type beats luminance', () => {
  const theme = resolveTheme('odd', { type: 'dark', colors: { 'editor.background': '#fafafa' } })
  assert.equal(theme.mode, 'dark')
})

test('uiTheme from package.json supplies the mode when type is absent', () => {
  assert.equal(modeFromUiTheme('vs-dark'), 'dark')
  assert.equal(modeFromUiTheme('vs'), 'light')
  assert.equal(modeFromUiTheme(undefined), null)
  const theme = resolveTheme('x', { colors: { 'editor.background': '#fafafa' } }, { uiTheme: 'vs-dark' })
  assert.equal(theme.mode, 'dark')
})

test('translucent theme colours are flattened, never emitted with alpha', () => {
  const theme = resolveTheme('alpha', {
    colors: {
      'editor.background': '#000000',
      'editor.foreground': '#ffffff',
      'list.activeSelectionBackground': '#ffffff80',
    },
  })
  for (const [name, value] of Object.entries(theme.vars)) {
    if (!name.startsWith('--s') && !name.startsWith('--o') && !name.startsWith('--c')) continue
    if (name.startsWith('--shadow') || name.startsWith('--scrim') || name === '--color-scheme') continue
    assert.match(value, /^#[0-9a-f]{6}$/, `${name} must be opaque hex, got ${value}`)
  }
  assert.equal(theme.vars['--primary-container'], '#808080')
})

test('a theme with no tokenColors still gets syntax roles, from its terminal palette', () => {
  const theme = resolveTheme('no-tokens', {
    colors: {
      'editor.background': '#080d14',
      'editor.foreground': '#efefef',
      'terminal.ansiGreen': '#00ff00',
      'terminal.ansiMagenta': '#ff00ff',
    },
  })
  assert.equal(theme.vars['--syn-string'], '#00ff00')
  assert.equal(theme.vars['--syn-keyword'], '#ff00ff')
  assert.equal(theme.vars['--syn-fg'], '#efefef')
})

test('syntax roles match a token scope by its longest prefix', () => {
  const theme = resolveTheme('tokens', {
    colors: { 'editor.background': '#000000', 'editor.foreground': '#ffffff' },
    // Legible against the code bed, so what is asserted here is the scope
    // matching rather than the contrast floor that would otherwise move them.
    tokenColors: [
      { scope: 'keyword', settings: { foreground: '#aaaaaa' } },
      { scope: ['comment', 'punctuation.definition.comment'], settings: { foreground: '#bbbbbb' } },
      { scope: 'string.quoted, string.template', settings: { foreground: '#cccccc' } },
      // A descendant selector keys off its final segment.
      { scope: 'meta.function entity.name.function', settings: { foreground: '#dddddd' } },
    ],
  })
  // `keyword.control` is tried first and is absent, so it falls back to `keyword`.
  assert.equal(theme.vars['--syn-keyword'], '#aaaaaa')
  assert.equal(theme.vars['--syn-comment'], '#bbbbbb')
  assert.equal(theme.vars['--syn-string'], '#cccccc')
  assert.equal(theme.vars['--syn-function'], '#dddddd')
})

test('the last rule naming a scope wins', () => {
  const theme = resolveTheme('dupes', {
    colors: { 'editor.background': '#000000' },
    tokenColors: [
      { scope: 'keyword', settings: { foreground: '#111111' } },
      { scope: 'keyword', settings: { foreground: '#999999' } },
    ],
  })
  assert.equal(theme.vars['--syn-keyword'], '#999999')
})

test('the terminal palette falls back per colour, not all or nothing', () => {
  const theme = resolveTheme('partial-ansi', {
    colors: { 'editor.background': '#101014', 'terminal.ansiRed': '#abcdef' },
  })
  assert.equal(theme.ansi.red, '#abcdef')
  assert.equal(theme.ansi.green, '#7ddc8a')
  // No terminal.background, so the editor's stands in.
  assert.equal(theme.ansi.background, '#101014')
})

test('status colours come from the terminal palette and stay distinct', () => {
  const theme = resolveTheme('status', {
    colors: { 'editor.background': '#101014', 'editor.foreground': '#d8d8e0' },
  })
  const status = ['--s-starting', '--s-active', '--s-compacting', '--s-waiting', '--s-idle', '--s-error']
  const values = status.map((name) => theme.vars[name])
  assert.equal(new Set(values).size, values.length, 'each session state needs its own colour')
})

// Border keys point in both directions in the wild: Nord sets one a hair off
// its background, Dracula uses its accent purple. The emphatic border has to
// come out louder than the subtle one either way.
for (const [label, border] of [
  ['a near-invisible border key', '#ffffff1a'],
  ['a border key set to a loud accent', '#bd93f9'],
] as const) {
  test(`outline stays louder than outline-variant given ${label}`, () => {
    const theme = resolveTheme('borders', {
      colors: { 'editor.background': '#282a36', 'editor.foreground': '#f8f8f2', 'panel.border': border },
    })
    const bg = parseColor('#282a36') as never
    const outline = contrast(parseColor(theme.vars['--outline']) as never, bg)
    const variant = contrast(parseColor(theme.vars['--outline-variant']) as never, bg)
    assert.ok(outline > variant, `outline ${outline.toFixed(2)} must beat variant ${variant.toFixed(2)}`)
  })
}

test('status colours are pushed off a light background rather than left invisible', () => {
  const theme = resolveTheme('pale', {
    type: 'light',
    colors: {
      'editor.background': '#ffffff',
      'editor.foreground': '#1f1f1f',
      // Legible as terminal output on this background, hopeless as a 6px dot.
      'terminal.ansiYellow': '#f0e68c',
    },
  })
  const white = parseColor('#ffffff') as never
  assert.ok(contrast(parseColor(theme.vars['--s-waiting']) as never, white) >= 3)
})

test('an on- role is measured against its container, not the surface', () => {
  const theme = resolveTheme('containers', {
    colors: {
      'editor.background': '#2e3440',
      'editor.foreground': '#d8dee9',
      // Container and error are the same colour here, which is what leaves the
      // naive derivation writing red on red.
      'inputValidation.errorBackground': '#bf616a',
      'errorForeground': '#bf616a',
    },
  })
  const container = parseColor(theme.vars['--error-container']) as never
  assert.ok(contrast(parseColor(theme.vars['--on-error-container']) as never, container) >= 4)
})

test('ensureContrast keeps a colour that already passes', () => {
  const black = { r: 0, g: 0, b: 0, a: 1 }
  const white = { r: 255, g: 255, b: 255, a: 1 }
  assert.equal(toHex(ensureContrast(white, black, 4.5)), '#ffffff')
  // Mid grey on white has to darken to get there.
  const grey = { r: 160, g: 160, b: 160, a: 1 }
  assert.ok(contrast(ensureContrast(grey, white, 4.5), white) >= 4.5)
})

test('code background differs from the surface it sits on', () => {
  const theme = resolveTheme('code', { colors: { 'editor.background': '#101014', 'editor.foreground': '#ffffff' } })
  assert.notEqual(theme.vars['--code-bg'], theme.vars['--surface'])
})

test('an empty theme resolves rather than throwing', () => {
  const theme = resolveTheme('empty', {})
  assert.equal(theme.mode, 'dark')
  assert.equal(theme.name, 'empty')
  assert.match(theme.vars['--surface'] as string, /^#[0-9a-f]{6}$/)
  assert.match(theme.ansi.brightWhite, /^#[0-9a-f]{6}$/)
})

test('a theme with no glass block stays wholly opaque', () => {
  const theme = resolveTheme('solid', { colors: { 'editor.background': '#101014' } })
  assert.equal(theme.glass, null)
  assert.match(theme.ansi.background, /^#[0-9a-f]{6}$/)
  assert.equal(theme.vars['--glass-sidebar'], undefined)
})

test('a glass theme emits translucent surfaces beside the opaque ones', () => {
  const theme = resolveTheme('glassy', {
    'helios.glass': { sidebar: 0.5, panel: 0.6, terminal: 0.55 },
    colors: { 'editor.background': '#0d0f13', 'editor.foreground': '#e8eaed' },
  })
  assert.deepEqual(theme.glass, { sidebar: 0.5, panel: 0.6, terminal: 0.55 })
  // The opaque ladder survives: it is the fallback wherever no backdrop exists.
  assert.match(theme.vars['--surface'] as string, /^#[0-9a-f]{6}$/)
  assert.match(theme.vars['--glass-sidebar'] as string, /^rgb\(.* \/ 50%\)$/)
  // Eight-digit hex, not rgb(): xterm parses this one itself, and 0.55 of 255
  // is 140 — 0x8c.
  assert.match(theme.ansi.background, /^#[0-9a-f]{6}8c$/)
})

test('glass opacities are clamped away from invisible', () => {
  const theme = resolveTheme('clear', {
    'helios.glass': { sidebar: 0, panel: -1, terminal: 5 },
    colors: { 'editor.background': '#0d0f13' },
  })
  assert.equal(theme.glass?.sidebar, 0.25)
  assert.equal(theme.glass?.panel, 0.25)
  assert.equal(theme.glass?.terminal, 1)
})

test('an omitted surface in the glass block gets a default rather than nothing', () => {
  const theme = resolveTheme('partial', {
    'helios.glass': { sidebar: 0.4 },
    colors: { 'editor.background': '#0d0f13' },
  })
  assert.equal(theme.glass?.sidebar, 0.4)
  assert.ok((theme.glass?.panel ?? 0) > 0)
  assert.ok((theme.glass?.terminal ?? 0) > 0)
})

test('the glass block survives an include chain', () => {
  const merged = mergeThemes({ 'helios.glass': { sidebar: 0.5 }, colors: {} }, { name: 'Child', colors: {} })
  assert.equal(merged['helios.glass']?.sidebar, 0.5)
})

const GLASS = { sidebar: 0.34, panel: 0.42, terminal: 0.34 }
const DARK = { 'editor.background': '#0d0f13', 'editor.foreground': '#e8eaed', 'textLink.foreground': '#7cb7ff' }

test('a glass theme with no backdrop block leaves the window to the OS', () => {
  const theme = resolveTheme('os-glass', { 'helios.glass': GLASS, colors: DARK })
  assert.equal(theme.backdrop, null)
  assert.equal(theme.vars['--backdrop'], undefined)
})

test('a backdrop block becomes one gradient per layer over the theme surface', () => {
  const theme = resolveTheme('meshy', {
    'helios.glass': GLASS,
    'helios.backdrop': { intensity: 0.45 },
    colors: DARK,
  })
  // Mesh is what a block with no style asked for, from the derived palette.
  assert.deepEqual(theme.backdrop, { style: 'mesh', intensity: 0.45, stops: null, image: null })
  const value = theme.vars['--backdrop'] as string
  assert.equal(value.match(/radial-gradient\(/g)?.length, 4)
  // The strongest layer is the intensity itself, and the theme's own surface is
  // the bed the gradients fade into.
  assert.match(value, /rgb\(.* \/ 45%\) 0%/)
  assert.match(value, /, rgb\(13 15 19 \/ 100%\)$/)
})

test('each style lays its own gradients out', () => {
  const value = (style: BackdropStyle): string =>
    resolveTheme('styled', {
      'helios.glass': GLASS,
      'helios.backdrop': { style, intensity: 0.4 },
      colors: DARK,
    }).vars['--backdrop'] as string

  assert.equal(value('corner').match(/radial-gradient\(/g)?.length, 2)
  assert.equal(value('wash').match(/radial-gradient\(/g)?.length, 2)
  // Aurora is the one style with bands rather than blobs.
  assert.equal(value('aurora').match(/linear-gradient\(/g)?.length, 2)
  assert.equal(value('aurora').match(/radial-gradient\(/g)?.length, 1)
})

test('the desktop style paints nothing, so the OS shows through', () => {
  const theme = resolveTheme('to-the-os', {
    'helios.glass': GLASS,
    'helios.backdrop': { style: 'desktop' },
    colors: DARK,
  })
  assert.equal(theme.backdrop, null)
  assert.equal(theme.vars['--backdrop'], undefined)
})

test('a backdrop with no glass block is not painted', () => {
  const theme = resolveTheme('opaque', { 'helios.backdrop': { intensity: 0.5 }, colors: DARK })
  assert.equal(theme.backdrop, null)
  assert.equal(theme.vars['--backdrop'], undefined)
})

test('the palette a picker draws its swatches from survives an opaque theme', () => {
  const theme = resolveTheme('solid', { colors: DARK })
  assert.equal(theme.backdropPalette.length, 4)
  for (const colour of theme.backdropPalette) assert.match(colour, /^#[0-9a-f]{6}$/)
})

// A theme's palette is picked to be read as text, which makes it pale; painted
// at partial alpha over near-black a pale blue composites to grey.
test('derived stops are pulled to a lightness that still reads as a hue', () => {
  const theme = resolveTheme('pastel', {
    'helios.glass': GLASS,
    colors: { ...DARK, 'textLink.foreground': '#cfe3ff' },
  })
  const [strong] = theme.backdropPalette as [string]
  assert.notEqual(strong, '#cfe3ff')
  assert.ok(luminance(parseColor(strong) as Rgb) < luminance(parseColor('#cfe3ff') as Rgb))
})

test('backdrop intensity is clamped short of drowning the text', () => {
  const loud = resolveTheme('loud', {
    'helios.glass': GLASS,
    'helios.backdrop': { intensity: 4 },
    colors: DARK,
  })
  assert.match(loud.vars['--backdrop'] as string, /rgb\(.* \/ 60%\) 0%/)
})

test('declared stops replace the derived palette, and place themselves', () => {
  const theme = resolveTheme('custom', {
    'helios.glass': GLASS,
    'helios.backdrop': {
      intensity: 0.4,
      stops: [{ color: '#ff0000', at: '10% 20%', size: '50% 40%' }, { color: '#00ff00' }],
    },
    colors: DARK,
  })
  const value = theme.vars['--backdrop'] as string
  assert.match(value, /radial-gradient\(50% 40% at 10% 20%, rgb\(255 0 0 \/ 40%\)/)
  assert.match(value, /rgb\(0 255 0 \//)
  // Fewer colours than the style has layers: they repeat rather than leaving a
  // corner of the window bare.
  assert.equal(value.match(/radial-gradient\(/g)?.length, 4)
})

// The strings in a stop reach an inline style untouched by any parser, so a
// position that is not plainly a pair of percentages is replaced rather than
// passed on.
test('a stop position that is not a pair of percentages falls back to the layout', () => {
  const theme = resolveTheme('sneaky', {
    'helios.glass': GLASS,
    'helios.backdrop': { stops: [{ color: '#ff0000', at: '0 0), url(https://example.com/x' }] },
    colors: DARK,
  })
  const value = theme.vars['--backdrop'] as string
  assert.ok(!value.includes('url('))
  assert.match(value, /at 12% 8%/)
})

// The picker puts these in a colour input, which takes #rrggbb and nothing
// else — including the shorthand and alpha forms a theme file may use.
test('named stops are reported back as plain hex', () => {
  const theme = resolveTheme('named', {
    'helios.glass': GLASS,
    'helios.backdrop': { stops: [{ color: '#f0a' }, { color: '#00ff0080' }] },
    colors: DARK,
  })
  assert.deepEqual(theme.backdrop?.stops, ['#ff00aa', '#00ff00'])
})

test('a stop whose colour does not parse is left out', () => {
  const theme = resolveTheme('bad-stop', {
    'helios.glass': GLASS,
    'helios.backdrop': { stops: [{ color: 'rebeccapurple' }, { color: '#00ff00' }] },
    colors: DARK,
  })
  const value = theme.vars['--backdrop'] as string
  // Two of the four mesh layers land on the unparseable stop and are dropped;
  // the other two are the green one.
  assert.equal(value.match(/radial-gradient\(/g)?.length, 2)
  assert.ok(!value.includes('rebeccapurple'))
  assert.match(value, /rgb\(0 255 0/)
})

test('blur is emitted as a filter, and only when there is one to apply', () => {
  const frosted = resolveTheme('frosted', { 'helios.glass': GLASS, 'helios.backdrop': { blur: 30 }, colors: DARK })
  assert.equal(frosted.backdropBlur, 30)
  assert.equal(frosted.vars['--glass-frost'], 'blur(30px) saturate(1.5)')
  // Cards are a sheet on a sheet, so they get half.
  assert.equal(frosted.vars['--glass-frost-soft'], 'blur(15px)')

  // No variable rather than blur(0px): the reference goes unresolved and the
  // property falls back to none, instead of promoting every glass surface to a
  // compositing layer to blur it by nothing.
  const sharp = resolveTheme('sharp', { 'helios.glass': GLASS, 'helios.backdrop': { blur: 0 }, colors: DARK })
  assert.equal(sharp.vars['--glass-frost'], undefined)
})

// The OS material has already blurred what is behind the window; frosting it
// again is haze. A painted gradient has had nothing done to it.
test('blur defaults to none for the desktop and to a radius for a gradient', () => {
  const desktop = resolveTheme('os', { 'helios.glass': GLASS, 'helios.backdrop': { style: 'desktop' }, colors: DARK })
  assert.equal(desktop.backdropBlur, 0)

  const painted = resolveTheme('painted', { 'helios.glass': GLASS, 'helios.backdrop': { style: 'mesh' }, colors: DARK })
  assert.ok(painted.backdropBlur > 0)
})

test('an opaque theme frosts nothing, whatever its backdrop block says', () => {
  const theme = resolveTheme('solid', { 'helios.backdrop': { blur: 40 }, colors: DARK })
  assert.equal(theme.backdropBlur, 0)
  assert.equal(theme.vars['--glass-frost'], undefined)
})

test('an image backdrop is served over the media scheme, under a scrim', () => {
  const theme = resolveTheme('pictured', {
    'helios.glass': GLASS,
    'helios.backdrop': { style: 'image', image: 'liquid-glass.png', intensity: 0.3 },
    colors: DARK,
  })
  assert.equal(theme.backdrop?.style, 'image')
  assert.equal(theme.backdrop?.image, 'liquid-glass.png')
  const value = theme.vars['--backdrop'] as string
  assert.match(value, /url\('helios-backdrop:\/\/media\/liquid-glass\.png'\) center \/ cover no-repeat/)
  // The scrim is the theme's own surface at the chosen strength, so a
  // photograph cannot decide how readable the transcript on top of it is.
  assert.match(value, /^linear-gradient\(rgb\(13 15 19 \/ 30%\), rgb\(13 15 19 \/ 30%\)\)/)
})

// The name reaches a url() in an inline style, and the scheme that serves it
// refuses to leave one directory — so anything that is not a bare file name of
// a type we serve is not an image at all.
test('an image name that is a path is refused, and the style falls back', () => {
  for (const image of ['../../secrets.png', '/etc/passwd', 'evil.png\'), url(\'http://x', 'notes.txt']) {
    const theme = resolveTheme('sneaky', {
      'helios.glass': GLASS,
      'helios.backdrop': { style: 'image', image },
      colors: DARK,
    })
    assert.equal(theme.backdrop?.image, null)
    assert.equal(theme.backdrop?.style, 'mesh')
    assert.ok(!(theme.vars['--backdrop'] as string).includes('url('))
  }
})

test('the backdrop block survives an include chain', () => {
  const merged = mergeThemes({ 'helios.backdrop': { intensity: 0.3 }, colors: {} }, { name: 'Child', colors: {} })
  assert.equal(merged['helios.backdrop']?.intensity, 0.3)
})

test('mergeThemes layers a child over what its include supplied', () => {
  const merged = mergeThemes(
    { colors: { 'editor.background': '#000000', 'editor.foreground': '#ffffff' }, tokenColors: [{ scope: 'keyword', settings: { foreground: '#111111' } }] },
    { name: 'Child', colors: { 'editor.background': '#123456' } },
  )
  assert.equal(merged.name, 'Child')
  assert.equal(merged.colors?.['editor.background'], '#123456')
  assert.equal(merged.colors?.['editor.foreground'], '#ffffff')
  assert.equal(merged.tokenColors?.length, 1)
})

test('mix travels the requested fraction', () => {
  const black = { r: 0, g: 0, b: 0, a: 1 }
  const white = { r: 255, g: 255, b: 255, a: 1 }
  assert.equal(toHex(mix(black, white, 0)), '#000000')
  assert.equal(toHex(mix(black, white, 1)), '#ffffff')
  assert.equal(toHex(mix(black, white, 0.5)), '#808080')
})

// Every bundled theme, swept against the surfaces the app actually draws on.
// The resolver used to measure its floors against `editor.background`, which is
// the lowest surface in the app and therefore the easiest one — sixteen of the
// seventeen themes below shipped text under the floor on the raised surfaces
// nobody was checking. Reading the themes off disk rather than fixturing them
// is the point: a theme added later is covered without touching this file.
const THEME_DIR = path.join(import.meta.dirname, '..', '..', 'themes')

/** Text roles and the floor each is held to, against every surface it lands on. */
const TEXT_FLOORS: [string, number][] = [
  ['on-surface', 4.5],
  ['on-surface-variant', 4.5],
  ['primary', 3],
  // Status and error are drawn as words, not only as dots: the "Active" label
  // on a session row is 10.5px and the tick on a tool result is 11.5px.
  ['error', 4.5],
  ['s-starting', 4.5],
  ['s-active', 4.5],
  ['s-compacting', 4.5],
  ['s-waiting', 4.5],
  ['s-idle', 4.5],
  ['s-error', 4.5],
  ['s-off', 4.5],
]

/**
 * Left as the theme states them: the poles are asked for as fills, or as the
 * inverse of one, far more often than as text on the terminal's own background.
 */
const ANSI_POLES = new Set(['black', 'white', 'brightWhite'])

const SURFACES = ['surface', 'surface-low', 'surface-container', 'surface-high', 'surface-highest']

for (const entry of fs.readdirSync(THEME_DIR).filter((f) => f.endsWith('.json')).sort()) {
  const id = entry.slice(0, -'.json'.length)
  test(`${id} keeps its text legible on every surface`, () => {
    const theme = resolveTheme(id, parseJsonc(fs.readFileSync(path.join(THEME_DIR, entry), 'utf8')) as VSCodeTheme)
    const colour = (name: string): Rgb => parseColor(theme.vars[name]) as Rgb

    for (const [role, floor] of TEXT_FLOORS) {
      for (const surface of SURFACES) {
        const ratio = contrast(colour(`--${role}`), colour(`--${surface}`))
        assert.ok(ratio >= floor, `--${role} on --${surface} is ${ratio.toFixed(2)}, below ${floor}`)
      }
    }

    // Syntax has a bed of its own, and a theme may set it to anything.
    const codeBg = colour('--code-bg')
    for (const [name, value] of Object.entries(theme.vars)) {
      if (!name.startsWith('--syn-')) continue
      const floor = name === '--syn-fg' ? 4.5 : name === '--syn-comment' ? 3 : 3.5
      const ratio = contrast(parseColor(value) as Rgb, codeBg)
      assert.ok(ratio >= floor, `${name} on --code-bg is ${ratio.toFixed(2)}, below ${floor}`)
    }

    // The terminal palette is text too. A light theme's yellow measures around
    // 2:1 on a white terminal, which is what a CLI writes its warnings in.
    const termBg = parseColor(theme.ansi.background.slice(0, 7)) as Rgb
    for (const [name, value] of Object.entries(theme.ansi)) {
      if (name === 'background' || name === 'selectionBackground' || name === 'cursor') continue
      if (ANSI_POLES.has(name)) continue
      const ratio = contrast(parseColor((value as string).slice(0, 7)) as Rgb, termBg)
      assert.ok(ratio >= 4.5, `ansi ${name} on the terminal background is ${ratio.toFixed(2)}, below 4.5`)
    }
  })
}
