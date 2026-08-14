import { ipcMain, type BrowserWindow, type IpcMainInvokeEvent } from 'electron'

import { ApiError, type ApiClient } from './api.ts'
import type { HostRegistry } from './hosts.ts'
import type { Notifier } from './notify.ts'
import type { PrefsStore } from './prefs.ts'
import type { TerminalManager } from './terminals.ts'

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
  const { hosts, terminals, notifier, prefs } = deps

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
