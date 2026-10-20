import { BrowserWindow, shell, type WebContents } from 'electron'
import WebSocket from 'ws'

import type { HostRegistry } from './hosts.ts'

/** What the renderer asks for when it opens a desktop tunnel (spec 66, D2). */
export interface OpenDesktopRequest {
  tunnelId: string
  hostId: string
  width: number
  height: number
  dpi: number
}

/**
 * The close codes the daemon ends a tunnel with, and the HTTP answers to an
 * upgrade that never became a tunnel, folded onto the same numbers so the
 * renderer has one table to read.
 */
const UPGRADE_CODES: Record<number, number> = { 401: 4401, 403: 4401, 409: 4503, 429: 4429, 503: 4503 }

/** No answer at all — a dropped network rather than a refusal. */
const ABNORMAL = 1006
const GUACD_FAILURE = 1011

const DIAL_TIMEOUT = 5_000
const PING_INTERVAL = 10_000
const PONG_TIMEOUT = 5_000

interface Tunnel {
  hostId: string
  owner: WebContents
  ws: WebSocket
  ping: NodeJS.Timeout | null
  pong: NodeJS.Timeout | null
}

/** Where a desktop window loads from, and how it should look on arrival. */
export interface DesktopWindowOptions {
  rendererDir: string
  preload: string
  icon: string
  background: () => string
  zoom: () => number
}

/**
 * Every open desktop tunnel, and the windows the desktop has been popped out
 * into.
 *
 * The tunnel is dialled here for the same reason the terminals are: the
 * renderer has `connect-src 'none'` and no credentials. The renderer gets the
 * Guacamole text frames over IPC and sends its own back the same way; nothing
 * here reads them. Each tunnel answers to the window that opened it, so a
 * popped-out desktop and the main window can each hold one.
 *
 * Events go straight to the owner: `desktop:open` ({tunnelId}),
 * `desktop:message` ({tunnelId, data}), `desktop:closed` ({tunnelId, code,
 * reason}).
 */
export class DesktopManager {
  private tunnels = new Map<string, Tunnel>()
  private popouts = new Map<string, BrowserWindow>()
  /** Windows already being watched for going away, so each is watched once. */
  private watched = new WeakSet<WebContents>()
  private readonly hosts: HostRegistry
  private readonly windowOptions: DesktopWindowOptions

  constructor(hosts: HostRegistry, windowOptions: DesktopWindowOptions) {
    this.hosts = hosts
    this.windowOptions = windowOptions
  }

  /**
   * Gets a ticket and dials. Never rejects: every way this can fail arrives as
   * a `desktop:closed` with a code, so the renderer decides what to do about
   * all of them in one place.
   */
  async open(owner: WebContents, req: OpenDesktopRequest): Promise<void> {
    this.close(req.tunnelId)
    const host = this.hosts.require(req.hostId)
    if (!this.watched.has(owner)) {
      this.watched.add(owner)
      owner.once('destroyed', () => this.closeOwner(owner))
    }

    let ticket: string
    try {
      ticket = (await host.api.desktopTicket()).ticket
    } catch (err) {
      const status = (err as { status?: unknown }).status
      const code = typeof status === 'number' ? (UPGRADE_CODES[status] ?? GUACD_FAILURE) : ABNORMAL
      emit(owner, 'desktop:closed', { tunnelId: req.tunnelId, code, reason: message(err) })
      return
    }

    const query = new URLSearchParams({
      ticket,
      width: String(req.width),
      height: String(req.height),
      dpi: String(req.dpi),
    })
    const ws = new WebSocket(`${host.record.url.replace(/^http/, 'ws')}/api/desktop/tunnel?${query}`, 'guacamole', {
      handshakeTimeout: DIAL_TIMEOUT,
      // A full-screen redraw is one frame of PNG blobs, and a big screen makes
      // a big frame.
      maxPayload: 64 << 20,
    })
    const tunnel: Tunnel = { hostId: req.hostId, owner, ws, ping: null, pong: null }
    this.tunnels.set(req.tunnelId, tunnel)

    let finished = false
    const finish = (code: number, reason: string): void => {
      if (finished) return
      finished = true
      this.stopHeartbeat(tunnel)
      // Closed from this side already: the renderer asked, and needs no answer.
      if (this.tunnels.get(req.tunnelId) !== tunnel) return
      this.tunnels.delete(req.tunnelId)
      emit(owner, 'desktop:closed', { tunnelId: req.tunnelId, code, reason })
    }

    ws.on('open', () => {
      emit(owner, 'desktop:open', { tunnelId: req.tunnelId })
      this.startHeartbeat(tunnel, () => finish(ABNORMAL, 'the host stopped answering'))
    })
    ws.on('message', (data) => emit(owner, 'desktop:message', { tunnelId: req.tunnelId, data: data.toString() }))
    ws.on('pong', () => {
      if (tunnel.pong) clearTimeout(tunnel.pong)
      tunnel.pong = null
    })
    ws.on('unexpected-response', (request, response) => {
      const status = response.statusCode ?? 0
      if (status === 401 || status === 403) host.api.invalidate()
      finish(UPGRADE_CODES[status] ?? GUACD_FAILURE, `the tunnel answered ${status}`)
      request.destroy()
    })
    ws.on('close', (code: number, reason: Buffer) => finish(code || ABNORMAL, reason.toString()))
    ws.on('error', (err: Error) => finish(ABNORMAL, err.message))
  }

  send(tunnelId: string, data: string): void {
    const tunnel = this.tunnels.get(tunnelId)
    if (tunnel?.ws.readyState === WebSocket.OPEN) tunnel.ws.send(data)
  }

  close(tunnelId: string): void {
    const tunnel = this.tunnels.get(tunnelId)
    if (!tunnel) return
    this.tunnels.delete(tunnelId)
    this.stopHeartbeat(tunnel)
    if (tunnel.ws.readyState === WebSocket.CONNECTING) tunnel.ws.terminate()
    else tunnel.ws.close(1000)
  }

  closeHost(hostId: string): void {
    for (const [id, tunnel] of [...this.tunnels]) {
      if (tunnel.hostId === hostId) this.close(id)
    }
    this.popouts.get(hostId)?.close()
  }

  /** Everything a window held, once it has gone. */
  private closeOwner(owner: WebContents): void {
    for (const [id, tunnel] of [...this.tunnels]) {
      if (tunnel.owner === owner) this.close(id)
    }
  }

  closeAll(): void {
    for (const id of [...this.tunnels.keys()]) this.close(id)
  }

  /** The popped-out windows, which hear the host events the main window does. */
  windows(): BrowserWindow[] {
    return [...this.popouts.values()].filter((win) => !win.isDestroyed())
  }

  /**
   * Opens a host's desktop in a window of its own, so it can sit beside the
   * session. One per host: asking again brings the one already open forward.
   */
  popOut(hostId: string): void {
    this.hosts.require(hostId)
    const existing = this.popouts.get(hostId)
    if (existing && !existing.isDestroyed()) {
      existing.show()
      existing.focus()
      return
    }

    const opts = this.windowOptions
    const win = new BrowserWindow({
      width: 1280,
      height: 860,
      minWidth: 640,
      minHeight: 480,
      icon: opts.icon,
      backgroundColor: opts.background(),
      webPreferences: {
        preload: opts.preload,
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        webviewTag: false,
        spellcheck: false,
      },
    })
    this.popouts.set(hostId, win)
    const contents = win.webContents
    win.on('closed', () => this.popouts.delete(hostId))
    contents.setWindowOpenHandler(({ url }) => {
      if (url.startsWith('https://') || url.startsWith('http://')) void shell.openExternal(url)
      return { action: 'deny' }
    })
    contents.on('will-navigate', (event, url) => {
      if (url !== contents.getURL()) event.preventDefault()
    })
    contents.on('did-finish-load', () => contents.setZoomFactor(opts.zoom()))
    void win.loadFile(`${opts.rendererDir}/index.html`, { query: { desktop: hostId } })
  }

  private startHeartbeat(tunnel: Tunnel, onDead: () => void): void {
    tunnel.ping = setInterval(() => {
      if (tunnel.pong) return
      tunnel.pong = setTimeout(() => {
        tunnel.ws.terminate()
        onDead()
      }, PONG_TIMEOUT)
      tunnel.ws.ping()
    }, PING_INTERVAL)
  }

  private stopHeartbeat(tunnel: Tunnel): void {
    if (tunnel.ping) clearInterval(tunnel.ping)
    if (tunnel.pong) clearTimeout(tunnel.pong)
    tunnel.ping = null
    tunnel.pong = null
  }
}

function emit(owner: WebContents, channel: string, payload: unknown): void {
  if (!owner.isDestroyed()) owner.send(channel, payload)
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
