import { app, nativeTheme } from 'electron'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { mergeThemes, resolveTheme, type HeliosTheme, type ThemeMode } from '../shared/theme/resolve.ts'
import { parseJsonc, type BackdropSpec, type VSCodeTheme } from '../shared/theme/vscode.ts'
import type { AppearancePrefs, ThemeSummary } from '../shared/models.ts'

export const DEFAULT_APPEARANCE: AppearancePrefs = {
  mode: 'system',
  lightTheme: 'light-modern',
  darkTheme: 'dark-modern',
  terminalTheme: 'match',
  proseSize: 14,
}

/* Clamped rather than trusted: the file is hand-editable, and a zero or a
   thousand there is a window with no readable way back to the setting. */
const MIN_PROSE = 10
const MAX_PROSE = 28

function proseSize(value: unknown): number {
  const size = Math.round(Number(value))
  if (!Number.isFinite(size)) return DEFAULT_APPEARANCE.proseSize
  return Math.min(Math.max(size, MIN_PROSE), MAX_PROSE)
}

/**
 * The colour themes available on this machine, and which of them is in force.
 *
 * Themes are VS Code colour theme files: the bundled ones vendored into
 * `themes/`, plus anything the user has dropped into `~/.helios/themes`. A user
 * file whose name matches a bundled theme replaces it, so a bundled theme can
 * be overridden without being deleted.
 */
export class ThemeRegistry {
  private readonly bundledDir: string
  private readonly userDir: string
  private readonly file: string
  private themes = new Map<string, HeliosTheme>()
  private prefs: AppearancePrefs = { ...DEFAULT_APPEARANCE }

  constructor(bundledDir: string, userDataDir = app.getPath('userData'), home = os.homedir()) {
    this.bundledDir = bundledDir
    this.userDir = path.join(home, '.helios', 'themes')
    this.file = path.join(userDataDir, 'appearance.json')
  }

  load(): void {
    this.reload()
    try {
      const parsed = JSON.parse(fs.readFileSync(this.file, 'utf8')) as Partial<AppearancePrefs>
      this.prefs = {
        mode: parsed.mode ?? DEFAULT_APPEARANCE.mode,
        lightTheme: parsed.lightTheme ?? DEFAULT_APPEARANCE.lightTheme,
        darkTheme: parsed.darkTheme ?? DEFAULT_APPEARANCE.darkTheme,
        terminalTheme: parsed.terminalTheme ?? DEFAULT_APPEARANCE.terminalTheme,
        proseSize: proseSize(parsed.proseSize ?? DEFAULT_APPEARANCE.proseSize),
      }
    } catch {
      this.prefs = { ...DEFAULT_APPEARANCE }
    }
  }

  /** Rescans both directories, so an edited theme file does not need a restart. */
  reload(): void {
    this.themes = new Map()
    for (const dir of [this.bundledDir, this.userDir]) this.scan(dir)
  }

  private scan(dir: string): void {
    let entries: string[]
    try {
      entries = fs.readdirSync(dir)
    } catch {
      // A missing ~/.helios/themes is the normal case, not a problem.
      return
    }
    for (const entry of entries) {
      if (!entry.endsWith('.json')) continue
      const id = entry.slice(0, -'.json'.length)
      try {
        const raw = this.read(path.join(dir, entry))
        this.themes.set(id, resolveTheme(id, raw))
      } catch (err) {
        console.error(`theme ${id} failed to load:`, err)
      }
    }
  }

  /**
   * A theme file, with whatever it includes layered underneath it.
   *
   * `include` names a sibling, and a user file may name a bundled one — which
   * is how the backdrop picker saves: a handful of lines in ~/.helios/themes
   * over the bundled theme's several hundred colours, so the override keeps
   * working when those colours change under it.
   */
  private read(file: string, depth = 0): VSCodeTheme {
    const raw = parseJsonc(fs.readFileSync(file, 'utf8')) as VSCodeTheme
    // A file that includes itself is the shape this takes when a user copies an
    // override next to the theme it overrode; the depth cap makes it a theme
    // with no parent rather than a hang at startup.
    if (!raw.include || depth >= 4) return raw
    const sibling = path.basename(raw.include)
    for (const dir of [path.dirname(file), this.bundledDir]) {
      const parent = path.join(dir, sibling)
      if (parent === file || !fs.existsSync(parent)) continue
      return mergeThemes(this.read(parent, depth + 1), raw)
    }
    return raw
  }

  list(): ThemeSummary[] {
    return [...this.themes.values()]
      .map((theme) => ({
        id: theme.id,
        name: theme.name,
        mode: theme.mode,
        swatch: [
          theme.vars['--surface'] as string,
          theme.vars['--surface-high'] as string,
          theme.vars['--primary'] as string,
          theme.vars['--syn-string'] as string,
          theme.vars['--syn-keyword'] as string,
        ],
      }))
      .sort((a, b) => a.name.localeCompare(b.name))
  }

  getPrefs(): AppearancePrefs {
    return { ...this.prefs }
  }

  /**
   * Saves a theme's backdrop, as a file in ~/.helios/themes rather than a
   * preference of its own.
   *
   * The backdrop is a property of the theme — a mesh drawn from one theme's
   * palette means nothing under another — so it belongs beside the colours it
   * is derived from, where a user can also hand-edit it and where exporting the
   * theme takes the backdrop with it.
   *
   * A bundled theme is not written to. The override includes it and states only
   * what the picker changed, so the bundled colours stay the source and keep
   * being updated by the app.
   */
  setBackdrop(id: string, spec: BackdropSpec): HeliosTheme {
    if (!this.themes.has(id)) throw new Error(`unknown theme: ${id}`)
    const file = path.join(this.userDir, `${id}.json`)
    let doc: VSCodeTheme = { include: `${id}.json` }
    if (fs.existsSync(file)) {
      // Their own theme, or an override written by an earlier pick: edited in
      // place, so nothing else the file says is lost.
      try {
        doc = parseJsonc(fs.readFileSync(file, 'utf8')) as VSCodeTheme
      } catch {
        // A file we cannot parse is one the user cannot have meant to keep.
      }
    }
    doc['helios.backdrop'] = spec
    fs.mkdirSync(this.userDir, { recursive: true })
    fs.writeFileSync(file, `${JSON.stringify(doc, null, 2)}\n`)
    this.reload()
    return this.themes.get(id) as HeliosTheme
  }

  setPrefs(next: Partial<AppearancePrefs>): AppearancePrefs {
    this.prefs = { ...this.prefs, ...next }
    this.prefs.proseSize = proseSize(this.prefs.proseSize)
    fs.mkdirSync(path.dirname(this.file), { recursive: true })
    fs.writeFileSync(this.file, JSON.stringify(this.prefs, null, 2))
    return this.getPrefs()
  }

  /**
   * Calls back when the OS appearance changes, but only while the user is
   * following it — a pinned light or dark theme should not move because someone
   * flipped a system switch.
   */
  onSystemChange(listener: () => void): void {
    nativeTheme.on('updated', () => {
      if (this.prefs.mode === 'system') listener()
    })
  }

  /** Which of the two slots applies right now. */
  activeMode(): ThemeMode {
    if (this.prefs.mode !== 'system') return this.prefs.mode
    return nativeTheme.shouldUseDarkColors ? 'dark' : 'light'
  }

  active(): HeliosTheme {
    const mode = this.activeMode()
    const wanted = mode === 'dark' ? this.prefs.darkTheme : this.prefs.lightTheme
    return (
      this.themes.get(wanted) ??
      this.themes.get(mode === 'dark' ? DEFAULT_APPEARANCE.darkTheme : DEFAULT_APPEARANCE.lightTheme) ??
      // A user who has emptied the themes directory still gets an app.
      resolveTheme('fallback', { type: mode })
    )
  }

  /**
   * The terminal palette, which is a separate choice: 'match' takes it from the
   * UI theme, anything else pins it so a preferred terminal look survives a
   * change of app theme.
   */
  activeTerminal(): HeliosTheme['ansi'] {
    if (this.prefs.terminalTheme === 'match') return this.active().ansi
    return (this.themes.get(this.prefs.terminalTheme) ?? this.active()).ansi
  }
}
