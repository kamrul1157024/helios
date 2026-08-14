import type { HeliosTheme } from './resolve.ts'

/**
 * Writes a theme onto the document root as inline custom properties.
 *
 * Inline rather than a generated stylesheet because inline declarations outrank
 * the `:root` block in styles.css without needing `!important`, and because the
 * preload script can do it before the page has a stylesheet at all.
 *
 * Every value has already been normalised to `#rrggbb` or a fixed `rgb(... / n%)`
 * by the resolver, so a hand-written theme file cannot smuggle anything else
 * into a property here.
 */
export function applyTheme(root: HTMLElement, theme: Pick<HeliosTheme, 'vars'>): void {
  for (const [name, value] of Object.entries(theme.vars)) root.style.setProperty(name, value)
}
