import type { ILinkHandler } from '@xterm/xterm'

/**
 * Open a terminal link in the user's browser.
 *
 * The real URL has to reach `window.open`: the main process routes
 * `window.open(url)` to `shell.openExternal` through `setWindowOpenHandler`
 * (see src/main/main.ts). xterm's built-in link handlers instead call
 * `window.open()` with no argument — to null the opener before assigning
 * `location.href` — which hands the main process an empty URL. The https guard
 * misses it, the popup is denied so `window.open()` returns null, and the
 * follow-up `location.href` never runs. The link just dies, behind a scary
 * "potentially dangerous" confirm. Passing the URL up front avoids all of that.
 *
 * `noopener,noreferrer` keeps the (denied) child window from reaching back into
 * this renderer, matching what the built-ins were trying to do with `opener`.
 */
export function openLink(uri: string): void {
  window.open(uri, '_blank', 'noopener,noreferrer')
}

/**
 * Handler for OSC 8 hyperlinks — the Terminal `linkHandler` option. Only
 * `activate` is needed; hover/leave keep xterm's defaults. `allowNonHttpProtocols`
 * stays off, so this only ever receives http(s) URLs.
 */
export const linkHandler: ILinkHandler = {
  activate: (_event, uri) => openLink(uri),
}

/** Handler for plain-text URLs detected by WebLinksAddon. */
export function webLinkActivate(_event: MouseEvent, uri: string): void {
  openLink(uri)
}
