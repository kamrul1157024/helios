import type { Density } from '../models.ts'
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

/**
 * How much of a session the sidebar shows.
 *
 * An attribute rather than variables, for the same reason as glass: density
 * changes which parts of a card are drawn, not what colour or size they are.
 */
export function applyDensity(root: HTMLElement, density: Density): void {
  root.dataset.density = density
}

export function applyTheme(root: HTMLElement, theme: Pick<HeliosTheme, 'vars'>, glass = false): void {
  for (const [name, value] of Object.entries(theme.vars)) root.style.setProperty(name, value)
  // Every other variable is emitted by every theme, so writing over them is
  // enough. These are the ones a theme may decline to set, and a stale value
  // left behind is the app painting over the desktop it was just asked to
  // show, or frosting a window that asked to be clear.
  for (const name of ['--backdrop', '--glass-frost', '--glass-frost-soft']) {
    if (!theme.vars[name]) root.style.removeProperty(name)
  }
  // An attribute rather than more variables: glass changes which surfaces
  // paint at all, not what colour they are.
  if (glass) root.dataset.glass = 'on'
  else delete root.dataset.glass
}
