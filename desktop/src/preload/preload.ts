import { contextBridge, ipcRenderer } from 'electron'

import { applyTheme } from '../shared/theme/apply.ts'
import type { HeliosTheme, XtermTheme } from '../shared/theme/resolve.ts'

interface ThemeBoot {
  theme: HeliosTheme
  terminal: XtermTheme
}

/**
 * The only surface the renderer gets.
 *
 * contextIsolation is on and nodeIntegration is off, so this file is the entire
 * boundary: no ipcRenderer, no require, no device key. Every call is a named
 * channel, and results carry their errors as data rather than as rejections
 * with a mangled stack.
 */

interface Result<T> {
  ok: boolean
  value?: T
  error?: string
  status?: number
  code?: string
}

/** Unwraps the main process's result envelope, rethrowing failures locally. */
async function call<T>(channel: string, ...args: unknown[]): Promise<T> {
  const result = (await ipcRenderer.invoke(channel, ...args)) as Result<T>
  if (!result.ok) {
    const error = new Error(result.error ?? `${channel} failed`) as Error & { status?: number; code?: string }
    error.status = result.status
    error.code = result.code
    throw error
  }
  return result.value as T
}

function on(channel: string, listener: (payload: unknown) => void): () => void {
  const wrapped = (_event: Electron.IpcRendererEvent, payload: unknown): void => listener(payload)
  ipcRenderer.on(channel, wrapped)
  return () => ipcRenderer.removeListener(channel, wrapped)
}

/**
 * Painted here, in the preload, rather than by the app.
 *
 * This script runs before any of the page's own scripts, so the variables are
 * on <html> before the first frame is composited — React then mounts into an
 * already-themed document. Doing it after mount means one frame of whatever the
 * stylesheet's defaults happen to be, which is a visible flash on every launch.
 */
const boot = ipcRenderer.sendSync('theme:boot') as ThemeBoot

if (document.documentElement) {
  applyTheme(document.documentElement, boot.theme)
} else {
  // Usually this branch: a preload runs before the parser has built <html>, so
  // there is nothing to write to yet. Waiting for DOMContentLoaded would be too
  // late — the stylesheet is parsed by then and its defaults get a frame. The
  // observer fires the moment <html> appears, which is still before <head>.
  const observer = new MutationObserver(() => {
    if (!document.documentElement) return
    applyTheme(document.documentElement, boot.theme)
    observer.disconnect()
  })
  observer.observe(document, { childList: true })
}

const helios = {
  hosts: {
    list: () => call('hosts:list'),
    statuses: () => call('hosts:statuses'),
    pairLocal: (name?: string) => call('hosts:pairLocal', name),
    pairURL: (url: string, name?: string) => call('hosts:pairURL', url, name),
    remove: (id: string) => call<void>('hosts:remove', id),
    rename: (id: string, name: string) => call<void>('hosts:rename', id, name),
    onChanged: (fn: (payload: unknown) => void) => on('hosts:changed', fn),
    onStatus: (fn: (payload: unknown) => void) => on('hosts:status', fn),
    onEvent: (fn: (payload: unknown) => void) => on('hosts:event', fn),
  },
  api: {
    call: <T>(hostId: string, method: string, args: unknown[] = []) =>
      call<T>('api:call', hostId, method, args),
  },
  term: {
    open: (req: unknown) => call<void>('term:open', req),
    input: (tabId: string, data: Uint8Array) => call<void>('term:input', tabId, data),
    resize: (tabId: string, cols: number, rows: number) => call<void>('term:resize', tabId, cols, rows),
    close: (tabId: string) => call<void>('term:close', tabId),
    status: (tabId: string) => call('term:status', tabId),
    onOutput: (fn: (payload: unknown) => void) => on('term:output', fn),
    onStatus: (fn: (payload: unknown) => void) => on('term:status', fn),
    onExited: (fn: (payload: unknown) => void) => on('term:exited', fn),
    onClosed: (fn: (payload: unknown) => void) => on('term:closed', fn),
  },
  prefs: {
    get: () => call('prefs:get'),
    setSound: (enabled: boolean) => call('prefs:setSound', enabled),
    setAlert: (type: string, enabled: boolean) => call('prefs:setAlert', type, enabled),
    reset: () => call('prefs:reset'),
  },
  theme: {
    /**
     * The theme already painted onto <html> by the time the page runs. Returned
     * rather than re-fetched so the renderer starts from exactly what is on
     * screen instead of asking again and risking a second, different answer.
     */
    boot: () => boot,
    list: () => call('theme:list'),
    prefs: () => call('theme:prefs'),
    set: (next: unknown) => call('theme:set', next),
    reload: () => call('theme:reload'),
    onChanged: (fn: (payload: unknown) => void) => on('theme:changed', fn),
  },
  hud: {
    resize: (height: number) => ipcRenderer.send('hud:resize', height),
    dismiss: () => ipcRenderer.send('hud:dismiss'),
    activate: (target: unknown) => ipcRenderer.send('hud:activate', target),
    resolved: (key: string) => ipcRenderer.send('hud:resolved', key),
    onPresent: (fn: (payload: unknown) => void) => on('hud:present', fn),
    onRetract: (fn: (payload: unknown) => void) => on('hud:retract', fn),
  },
  app: {
    quit: () => call<void>('app:quit'),
    onActivateNotification: (fn: (payload: unknown) => void) => on('app:activate-notification', fn),
    onOpenPairing: (fn: (payload: unknown) => void) => on('app:open-pairing', fn),
    onOpenSettings: (fn: () => void) => on('app:open-settings', () => fn()),
  },
}

contextBridge.exposeInMainWorld('helios', helios)

export type HeliosBridge = typeof helios
