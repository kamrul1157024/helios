/**
 * The fonts a reader can pick from, in three places: the interface, code, and
 * the terminal.
 *
 * A catalogue of ids rather than free text. The value is written into a CSS
 * custom property, so a stored string would be a way to put arbitrary CSS into
 * the document from a hand-edited prefs file; an id that does not resolve
 * simply falls back to the default.
 *
 * Only Fira Code and the symbol face are shipped with the app. The rest are
 * `local()` names — offered because they are the fonts people who care about
 * this already have, and reported as missing by the picker when they are not
 * installed.
 */
export type FontSlot = 'ui' | 'code' | 'terminal'

export interface FontChoice {
  id: string
  label: string
  /** What lands in the CSS variable. */
  stack: string
  /** Shipped in the app, so it is there whatever the machine has. */
  bundled?: boolean
  /** The family to ask `document.fonts.check` about, for the ones that are not. */
  probe?: string
}

/* Appended to every monospace stack: the symbol face covers the Private Use
   Area an agent's title or a shell prompt draws its glyphs from, and the
   generic keeps a stack honest when nothing above it resolves. */
const GLYPHS = "'Helios Glyphs', monospace"

const SYSTEM_UI =
  "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Ubuntu, Cantarell, 'Noto Sans', system-ui, sans-serif"

export const MONO_FONTS: FontChoice[] = [
  { id: 'fira-code', label: 'Fira Code', stack: `'Fira Code', ${GLYPHS}`, bundled: true },
  {
    id: 'jetbrains-mono',
    label: 'JetBrains Mono',
    stack: `'JetBrains Mono', 'JetBrainsMono Nerd Font', ${GLYPHS}`,
    probe: 'JetBrains Mono',
  },
  {
    id: 'ibm-plex-mono',
    label: 'IBM Plex Mono',
    stack: `'IBM Plex Mono', ${GLYPHS}`,
    probe: 'IBM Plex Mono',
  },
  {
    id: 'cascadia-code',
    label: 'Cascadia Code',
    stack: `'Cascadia Code', 'Cascadia Mono', ${GLYPHS}`,
    probe: 'Cascadia Code',
  },
  {
    id: 'source-code-pro',
    label: 'Source Code Pro',
    stack: `'Source Code Pro', ${GLYPHS}`,
    probe: 'Source Code Pro',
  },
  {
    id: 'system-mono',
    label: 'System monospace',
    stack: `ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, ${GLYPHS}`,
    bundled: true,
  },
]

export const UI_FONTS: FontChoice[] = [
  { id: 'system-ui', label: 'System', stack: SYSTEM_UI, bundled: true },
  { id: 'inter', label: 'Inter', stack: `Inter, ${SYSTEM_UI}`, probe: 'Inter' },
  { id: 'ibm-plex-sans', label: 'IBM Plex Sans', stack: `'IBM Plex Sans', ${SYSTEM_UI}`, probe: 'IBM Plex Sans' },
  { id: 'fira-code-ui', label: 'Fira Code', stack: `'Fira Code', ${GLYPHS}`, bundled: true },
]

export const DEFAULT_FONTS: Record<FontSlot, string> = {
  ui: 'system-ui',
  code: 'fira-code',
  terminal: 'fira-code',
}

export function choicesFor(slot: FontSlot): FontChoice[] {
  return slot === 'ui' ? UI_FONTS : MONO_FONTS
}

/** The stack an id names, or the slot's default when it names nothing. */
export function fontStack(slot: FontSlot, id: string): string {
  const choices = choicesFor(slot)
  const picked = choices.find((font) => font.id === id)
  if (picked) return picked.stack
  return choices.find((font) => font.id === DEFAULT_FONTS[slot])!.stack
}

/** A stored id, or the default when the file names one that no longer exists. */
export function fontId(slot: FontSlot, value: unknown): string {
  if (typeof value === 'string' && choicesFor(slot).some((font) => font.id === value)) return value
  return DEFAULT_FONTS[slot]
}
