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
/** The reading size for rendered markdown, which the .md rules scale from. */
export function applyProseSize(root: HTMLElement, size: number): void {
  root.style.setProperty('--prose-size', `${size}px`)
}

export function applyTheme(root: HTMLElement, theme: Pick<HeliosTheme, 'vars'>, glass = false): void {
  for (const [name, value] of Object.entries(theme.vars)) root.style.setProperty(name, value)
  // An attribute rather than more variables: the backdrop changes which
  // surfaces paint at all, not what colour they are.
  if (glass) root.dataset.glass = 'on'
  else delete root.dataset.glass
}
