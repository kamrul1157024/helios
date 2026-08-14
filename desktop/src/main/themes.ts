import { app, nativeTheme } from 'electron'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { resolveTheme, type HeliosTheme, type ThemeMode } from '../shared/theme/resolve.ts'
import { parseJsonc, type VSCodeTheme } from '../shared/theme/vscode.ts'
import type { AppearancePrefs, ThemeSummary } from '../shared/models.ts'

export const DEFAULT_APPEARANCE: AppearancePrefs = {
  mode: 'system',
  lightTheme: 'light-modern',
  darkTheme: 'dark-modern',
  terminalTheme: 'match',
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
        const raw = parseJsonc(fs.readFileSync(path.join(dir, entry), 'utf8')) as VSCodeTheme
        this.themes.set(id, resolveTheme(id, raw))
      } catch (err) {
        console.error(`theme ${id} failed to load:`, err)
      }
    }
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

  setPrefs(next: Partial<AppearancePrefs>): AppearancePrefs {
    this.prefs = { ...this.prefs, ...next }
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
