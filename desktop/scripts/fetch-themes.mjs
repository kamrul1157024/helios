// Refreshes the bundled colour themes in the repo-root `themes/` directory.
//
// Themes are vendored rather than fetched at runtime, and flattened rather than
// stored as their authors publish them: an upstream theme is often a two- or
// three-file `include` chain rooted at a VS Code built-in, which is fine inside
// VS Code and unresolvable anywhere else. Each file written here is a complete,
// standalone VS Code colour theme, so the same file works whether it ships with
// the app or a user drops it into ~/.helios/themes.
//
// Usage: node scripts/fetch-themes.mjs

import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync, mkdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { parseJsonc } from '../src/shared/theme/vscode.ts'
import { mergeThemes, modeFromUiTheme } from '../src/shared/theme/resolve.ts'

const out = resolve(dirname(fileURLToPath(import.meta.url)), '../../themes')

const VSCODE = 'https://raw.githubusercontent.com/microsoft/vscode/main/extensions'
const APOS = 'https://raw.githubusercontent.com/Apostolique/AposTheme/master/themes'

/** Themes published as a plain JSON file in a git repo. */
const RAW = [
  { id: 'dark-modern', url: `${VSCODE}/theme-defaults/themes/dark_modern.json`, type: 'dark' },
  { id: 'light-modern', url: `${VSCODE}/theme-defaults/themes/light_modern.json`, type: 'light' },
  { id: 'monokai', url: `${VSCODE}/theme-monokai/themes/monokai-color-theme.json`, type: 'dark' },
  { id: 'solarized-dark', url: `${VSCODE}/theme-solarized-dark/themes/solarized-dark-color-theme.json`, type: 'dark' },
  { id: 'solarized-light', url: `${VSCODE}/theme-solarized-light/themes/solarized-light-color-theme.json`, type: 'light' },
  {
    id: 'tokyo-night',
    url: 'https://raw.githubusercontent.com/enkia/tokyo-night-vscode-theme/master/themes/tokyo-night-color-theme.json',
    type: 'dark',
  },
  {
    id: 'nord',
    url: 'https://raw.githubusercontent.com/nordtheme/visual-studio-code/develop/themes/nord-color-theme.json',
    type: 'dark',
  },
  {
    id: 'one-dark-pro',
    url: 'https://raw.githubusercontent.com/Binaryify/OneDark-Pro/master/themes/OneDark-Pro.json',
    type: 'dark',
  },
  { id: 'apos', url: `${APOS}/AposTheme-color-theme.json`, type: 'dark' },
  { id: 'apos-gray', url: `${APOS}/AposTheme-gray-color-theme.json`, type: 'dark' },
  { id: 'apos-green', url: `${APOS}/AposTheme-green-color-theme.json`, type: 'dark' },
  { id: 'apos-red', url: `${APOS}/AposTheme-red-color-theme.json`, type: 'dark' },
]

/**
 * Themes whose repos generate their JSON at package time, so the published
 * extension is the only place the finished file exists.
 */
const EXTENSIONS = [
  { publisher: 'Catppuccin', name: 'catppuccin-vsc', pick: { 'catppuccin-mocha': /mocha/i, 'catppuccin-latte': /latte/i } },
  { publisher: 'dracula-theme', name: 'theme-dracula', pick: { dracula: /^Dracula Theme$/ } },
  { publisher: 'GitHub', name: 'github-vscode-theme', pick: { 'github-dark': /^GitHub Dark$/, 'github-light': /^GitHub Light$/ } },
  { publisher: 'jdinhlife', name: 'gruvbox', pick: { 'gruvbox-dark': /Dark Medium/i, 'gruvbox-light': /Light Medium/i } },
]

async function fetchText(url) {
  const response = await fetch(url)
  if (!response.ok) throw new Error(`${response.status} ${url}`)
  return response.text()
}

/** Follows an `include` up its chain, merging each parent under its child. */
async function flattenRemote(url) {
  const theme = parseJsonc(await fetchText(url))
  if (!theme.include) return theme
  const parentUrl = new URL(theme.include, url).toString()
  return mergeThemes(await flattenRemote(parentUrl), theme)
}

function flattenLocal(dir, file) {
  const theme = parseJsonc(readFileSync(resolve(dir, file), 'utf8'))
  if (!theme.include) return theme
  return mergeThemes(flattenLocal(resolve(dir, dirname(file)), theme.include), theme)
}

function write(id, theme, type) {
  const body = {
    name: theme.name ?? id,
    type: theme.type ?? type,
    colors: theme.colors ?? {},
    tokenColors: theme.tokenColors ?? [],
  }
  writeFileSync(resolve(out, `${id}.json`), JSON.stringify(body, null, 2) + '\n')
  const keys = Object.keys(body.colors).length
  console.log(`  ${id.padEnd(18)} ${body.type.padEnd(5)} ${String(keys).padStart(4)} colours  ${body.tokenColors.length} token rules`)
}

async function fromExtension(spec) {
  const meta = await (await fetch(`https://open-vsx.org/api/${spec.publisher}/${spec.name}/latest`)).json()
  const url = meta.files?.download
  if (!url) throw new Error(`no download for ${spec.publisher}.${spec.name}`)
  const dir = mkdtempSync(resolve(tmpdir(), 'helios-theme-'))
  try {
    const vsix = resolve(dir, 'ext.vsix')
    writeFileSync(vsix, Buffer.from(await (await fetch(url)).arrayBuffer()))
    execFileSync('unzip', ['-qo', vsix, '-d', dir])
    const pkg = JSON.parse(readFileSync(resolve(dir, 'extension/package.json'), 'utf8'))
    const contributed = pkg.contributes?.themes ?? []
    for (const [id, match] of Object.entries(spec.pick)) {
      const entry = contributed.find((t) => match.test(t.label ?? '') || match.test(t.path ?? ''))
      if (!entry) {
        console.log(`  ${id.padEnd(18)} SKIPPED — no match in ${spec.publisher}.${spec.name}`)
        continue
      }
      const theme = flattenLocal(resolve(dir, 'extension'), entry.path)
      write(id, theme, modeFromUiTheme(entry.uiTheme) ?? 'dark')
    }
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

mkdirSync(out, { recursive: true })
console.log(`writing to ${out}`)
for (const spec of RAW) write(spec.id, await flattenRemote(spec.url), spec.type)
for (const spec of EXTENSIONS) await fromExtension(spec)
console.log(`\n${readdirSync(out).filter((f) => f.endsWith('.json')).length} themes`)
