// The resolver's job is to survive incomplete themes, which is what almost
// every real theme is. These cover the cases that actually turn up in the wild:
// a theme with nothing but a background, one that states no type, one whose
// colours carry alpha, and one with no syntax rules at all.
import assert from 'node:assert/strict'
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
    tokenColors: [
      { scope: 'keyword', settings: { foreground: '#111111' } },
      { scope: ['comment', 'punctuation.definition.comment'], settings: { foreground: '#222222' } },
      { scope: 'string.quoted, string.template', settings: { foreground: '#333333' } },
      // A descendant selector keys off its final segment.
      { scope: 'meta.function entity.name.function', settings: { foreground: '#444444' } },
    ],
  })
  // `keyword.control` is tried first and is absent, so it falls back to `keyword`.
  assert.equal(theme.vars['--syn-keyword'], '#111111')
  assert.equal(theme.vars['--syn-comment'], '#222222')
  assert.equal(theme.vars['--syn-string'], '#333333')
  assert.equal(theme.vars['--syn-function'], '#444444')
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
