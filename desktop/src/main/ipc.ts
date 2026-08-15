import { BrowserWindow, ipcMain, type IpcMainInvokeEvent } from 'electron'

import { ApiError, type ApiClient } from './api.ts'
import type { HostRegistry } from './hosts.ts'
import type { Notifier } from './notify.ts'
import type { PrefsStore } from './prefs.ts'
import type { TerminalManager } from './terminals.ts'
import type { ThemeRegistry } from './themes.ts'
import type { AppearancePrefs } from '../shared/models.ts'

/**
 * REST calls the renderer may make. An allow-list rather than a reflective
 * bridge: the renderer is trusted code, but a typo should fail here rather than
 * reach for something on ApiClient that was never meant to be remote — and the
 * list doubles as the contract between the two processes.
 */
const API_METHODS = new Set<keyof ApiClient>([
  'listSessions',
  'getSession',
  'listDirectories',
  'transcript',
  'subagents',
  'sendPrompt',
  'setSessionOrder',
  'uploadFiles',
  'stop',
  'terminate',
  'resume',
  'wake',
  'openShell',
  'terminals',
  'killTerminal',
  'setPermissionMode',
  'generateTitle',
  'patchSession',
  'deleteSession',
  'createSession',
  'notifications',
  'notificationAction',
  'dismissNotification',
  'gitStatus',
  'gitDiff',
  'gitLog',
  'gitChanges',
  'gitWorktrees',
  'listFiles',
  'readFile',
  'searchFiles',
  'grepFiles',
  'writeFile',
  'providers',
  'models',
  'commands',
  'settings',
  'updateSettings',
  'devices',
  'health',
])

export interface IpcDeps {
  hosts: HostRegistry
  terminals: TerminalManager
  notifier: Notifier
  prefs: PrefsStore
  themes: ThemeRegistry
  quit: () => void
  /** Whether this platform can show the OS backdrop at all. */
  glassSupported: boolean
  /** Re-applies the window material after the preference changes. */
  onAppearanceChange: () => void
  window: () => BrowserWindow | null
}

/**
 * Wires the renderer to the main process.
 *
 * Everything privileged — device keys, sockets, the daemon's REST API — lives
 * on this side; the renderer only ever asks. Handlers translate thrown errors
 * into a plain shape, because an Error crossing IPC arrives as an unhelpful
 * string otherwise.
 */
export function registerIpc(deps: IpcDeps): void {
  const { hosts, terminals, notifier, prefs, themes, quit, glassSupported, onAppearanceChange } = deps

  const send = (channel: string, payload: unknown): void => {
    const window = deps.window()
    if (window && !window.isDestroyed()) window.webContents.send(channel, payload)
  }

  // ─── Hosts ─────────────────────────────────────────────────────────────

  handle('hosts:list', async () => hosts.list())
  handle('hosts:statuses', async () => hosts.statuses())
  handle('hosts:pairLocal', (_e, name?: string) => hosts.pairLocal(name))
  handle('hosts:pairURL', (_e, url: string, name?: string) => hosts.pairURL(url, name))
  handle('hosts:remove', async (_e, id: string) => {
    terminals.closeHost(id)
    notifier.clearHost(id)
    hosts.remove(id)
  })
  handle('hosts:rename', async (_e, id: string, name: string) => hosts.rename(id, name))

  /**
   * Brings the tray in line with the daemon.
   *
   * Coming back online means whatever happened while offline was missed, and a
   * missed `notification_resolved` leaves an approval on the tray that nothing
   * else will ever take off it.
   */
  const reconcile = async (hostId: string): Promise<void> => {
    try {
      notifier.seed(hostId, await hosts.require(hostId).api.notifications({ status: 'pending' }))
    } catch {
      // Offline again already; the next status change tries once more.
    }
  }

  hosts.on('hosts', (list) => send('hosts:changed', list))
  hosts.on('status', (status: { id: string; state: string }) => {
    send('hosts:status', status)
    if (status.state === 'online') void reconcile(status.id)
  })
  hosts.on('event', ({ hostId, event }: { hostId: string; event: { type: string; data: Record<string, unknown> } }) => {
    notifier.handleEvent(hostId, event.type, event.data)
    send('hosts:event', { hostId, event })
  })

  // ─── REST ──────────────────────────────────────────────────────────────

  handle('api:call', async (_e, hostId: string, method: string, args: unknown[]) => {
    if (!API_METHODS.has(method as keyof ApiClient)) {
      throw new Error(`method not exposed: ${method}`)
    }
    const api = hosts.require(hostId).api
    const fn = api[method as keyof ApiClient] as (...a: unknown[]) => Promise<unknown>
    return fn.apply(api, args ?? [])
  })

  // ─── Notification preferences ──────────────────────────────────────────

  handle('prefs:get', async () => prefs.get())
  handle('prefs:setSound', async (_e, enabled: boolean) => prefs.setSound(enabled))
  handle('prefs:setAlert', async (_e, type: string, enabled: boolean) => prefs.setAlert(type, enabled))
  handle('prefs:reset', async () => prefs.reset())

  // Quitting for real, as opposed to closing the window, which leaves the app
  // on the tray waiting for approvals.
  handle('app:quit', async () => quit())

  // ─── Appearance ────────────────────────────────────────────────────────

  // Every window, not just the main one: the HUD draws its cards from the same
  // variables and would otherwise keep the old theme until it next opened.
  const themePayload = (): {
    theme: unknown
    terminal: unknown
    glass: boolean
    glassSupported: boolean
    proseSize: number
  } => ({
    theme: themes.active(),
    terminal: themes.activeTerminal(),
    proseSize: themes.getPrefs().proseSize,
    // A property of the chosen theme, not a setting of its own: picking a
    // glass theme is the whole of the request. Gated on the platform, because
    // a preference to show a backdrop that does not exist is not showing one.
    glass: glassSupported && themes.active().glass !== null,
    glassSupported,
  })

  const broadcastTheme = (): ReturnType<typeof themePayload> => {
    const payload = themePayload()
    for (const win of BrowserWindow.getAllWindows()) {
      if (!win.isDestroyed()) win.webContents.send('theme:changed', payload)
    }
    return payload
  }

  themes.onSystemChange(broadcastTheme)

  // Synchronous, and the only channel that is: the preload script reads it
  // before the page has scripts of its own so it can paint the theme onto
  // <html> ahead of the first frame. Going through invoke would put a promise
  // between the document opening and its colours arriving, which is the flash
  // this avoids.
  ipcMain.on('theme:boot', (event) => {
    event.returnValue = themePayload()
  })

  handle('theme:list', async () => themes.list())
  handle('theme:prefs', async () => themes.getPrefs())
  handle('theme:set', async (_e, next: Partial<AppearancePrefs>) => {
    themes.setPrefs(next)
    onAppearanceChange()
    return broadcastTheme()
  })
  handle('theme:reload', async () => {
    themes.reload()
    broadcastTheme()
    return themes.list()
  })

  // ─── Terminals ─────────────────────────────────────────────────────────

  handle('term:open', (_e, req: Parameters<TerminalManager['open']>[0]) => terminals.open(req))
  handle('term:input', async (_e, tabId: string, data: Uint8Array) => terminals.input(tabId, data))
  handle('term:resize', async (_e, tabId: string, cols: number, rows: number) =>
    terminals.resize(tabId, cols, rows),
  )
  handle('term:close', async (_e, tabId: string) => terminals.close(tabId))
  handle('term:status', async (_e, tabId: string) => terminals.status(tabId))

  terminals.on('output', (payload) => send('term:output', payload))
  terminals.on('status', (payload) => send('term:status', payload))
  terminals.on('exited', (payload) => send('term:exited', payload))
  terminals.on('closed', (payload) => send('term:closed', payload))
}

/**
 * Wraps a handler so failures arrive as {error, status, code} instead of an
 * Error serialised to "Error: …" with the stack glued on.
 */
function handle(
  channel: string,
  fn: (event: IpcMainInvokeEvent, ...args: never[]) => Promise<unknown>,
): void {
  ipcMain.handle(channel, async (event, ...args) => {
    try {
      return { ok: true, value: await fn(event, ...(args as never[])) }
    } catch (err) {
      if (err instanceof ApiError) {
        return { ok: false, error: err.message, status: err.status, code: err.code }
      }
      return { ok: false, error: err instanceof Error ? err.message : String(err) }
    }
  })
}
