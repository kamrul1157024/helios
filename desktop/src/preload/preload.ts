import { contextBridge, ipcRenderer } from 'electron'

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
  app: {
    onActivateNotification: (fn: (payload: unknown) => void) => on('app:activate-notification', fn),
    onOpenPairing: (fn: (payload: unknown) => void) => on('app:open-pairing', fn),
  },
}

contextBridge.exposeInMainWorld('helios', helios)

export type HeliosBridge = typeof helios
