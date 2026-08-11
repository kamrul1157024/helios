import { EventEmitter } from 'node:events'

import { TerminalConn, type Endpoint } from './conn.ts'
import type { HostRegistry } from './hosts.ts'
import type { TabStatus } from '../shared/models.ts'

/**
 * Output is batched on a frame-ish interval rather than forwarded per frame. A
 * build log arrives as thousands of small writes, and one IPC message each
 * would spend more time crossing the process boundary than drawing.
 */
const FLUSH_INTERVAL = 8

/** A cap on the coalescing buffer, so a runaway producer cannot grow it without bound. */
const FLUSH_BYTES = 256 * 1024

export interface OpenTabRequest {
  tabId: string
  hostId: string
  sessionId: string
  cols: number
  rows: number
  /** Warm a cold session before attaching. Never implicit: waking costs a process. */
  wake?: boolean
}

interface Tab {
  id: string
  hostId: string
  sessionId: string
  conn: TerminalConn
  buffered: Uint8Array[]
  bufferedBytes: number
  timer: NodeJS.Timeout | null
  status: TabStatus
}

/**
 * Owns every open terminal connection.
 *
 * Events: `output` ({tabId, data}), `status` ({tabId, status}), `closed`
 * ({tabId, reason}).
 */
export class TerminalManager extends EventEmitter {
  private tabs = new Map<string, Tab>()

  constructor(private readonly hosts: HostRegistry) {
    super()
  }

  async open(req: OpenTabRequest): Promise<void> {
    this.close(req.tabId)

    const host = this.hosts.require(req.hostId)
    const endpoint = await this.resolveEndpoint(req)

    const conn = new TerminalConn({
      endpoint,
      cols: req.cols,
      rows: req.rows,
      role: 'interactive',
      name: 'desktop',
      onAuthFailure: () => host.api.invalidate(),
    })

    const tab: Tab = {
      id: req.tabId,
      hostId: req.hostId,
      sessionId: req.sessionId,
      conn,
      buffered: [],
      bufferedBytes: 0,
      timer: null,
      status: { state: 'connecting' },
    }
    this.tabs.set(req.tabId, tab)

    conn.on('output', (data: Uint8Array) => this.enqueue(tab, data))
    conn.on('snapshot', (data: Uint8Array) => {
      // A snapshot replaces the screen, so anything already queued is stale.
      tab.buffered = []
      tab.bufferedBytes = 0
      this.enqueue(tab, data)
    })
    conn.on('status', (status: { state: string; writer: string; viewers: number; cols: number; rows: number }) => {
      tab.status = {
        ...tab.status,
        hostState: status.state,
        writer: status.writer,
        viewers: status.viewers,
        cols: status.cols,
        rows: status.rows,
      }
      this.emit('status', { tabId: tab.id, status: tab.status })
    })
    conn.on('state', (state: TabStatus['state']) => {
      tab.status = { ...tab.status, state }
      this.emit('status', { tabId: tab.id, status: tab.status })
    })
    conn.on('detail', (detail: string) => {
      tab.status = { ...tab.status, detail }
      this.emit('status', { tabId: tab.id, status: tab.status })
    })
    conn.on('exit', (code: number) => this.emit('exited', { tabId: tab.id, code }))
    conn.on('close', (reason: string) => {
      this.flush(tab)
      this.emit('closed', { tabId: tab.id, reason })
    })

    await conn.start()
  }

  /**
   * Picks the transport. A local host is dialled over its unix socket, which
   * skips the daemon's relay entirely; a remote one goes over the authenticated
   * WebSocket. Same frames either way.
   */
  private async resolveEndpoint(req: OpenTabRequest): Promise<Endpoint> {
    const host = this.hosts.require(req.hostId)

    if (host.record.local) {
      let socket = (await host.api.getSession(req.sessionId)).session.terminal
      if (!socket) {
        if (!req.wake) throw new Error('session has no live terminal')
        socket = (await host.api.wake(req.sessionId)).terminal
      }
      return { kind: 'unix', path: socket }
    }

    const base = host.record.url.replace(/^http/, 'ws')
    const query = req.wake ? '?wake=1' : ''
    return {
      kind: 'ws',
      url: `${base}/api/sessions/${encodeURIComponent(req.sessionId)}/terminal${query}`,
      token: async () => host.api.authHeader().slice('Bearer '.length),
    }
  }

  input(tabId: string, data: Uint8Array): void {
    this.tabs.get(tabId)?.conn.input(data)
  }

  /**
   * Reports a tab's geometry. A backgrounded tab passes 0×0 to withdraw from
   * size negotiation instead of shrinking the PTY for everyone else.
   */
  resize(tabId: string, cols: number, rows: number): void {
    this.tabs.get(tabId)?.conn.resize(cols, rows)
  }

  close(tabId: string): void {
    const tab = this.tabs.get(tabId)
    if (!tab) return
    if (tab.timer) clearTimeout(tab.timer)
    this.tabs.delete(tabId)
    tab.conn.close()
  }

  closeHost(hostId: string): void {
    for (const tab of [...this.tabs.values()]) {
      if (tab.hostId === hostId) this.close(tab.id)
    }
  }

  closeAll(): void {
    for (const id of [...this.tabs.keys()]) this.close(id)
  }

  status(tabId: string): TabStatus | undefined {
    return this.tabs.get(tabId)?.status
  }

  // ─── Output coalescing ─────────────────────────────────────────────────

  private enqueue(tab: Tab, data: Uint8Array): void {
    tab.buffered.push(data)
    tab.bufferedBytes += data.length
    if (tab.bufferedBytes >= FLUSH_BYTES) {
      this.flush(tab)
      return
    }
    if (!tab.timer) {
      tab.timer = setTimeout(() => this.flush(tab), FLUSH_INTERVAL)
    }
  }

  private flush(tab: Tab): void {
    if (tab.timer) clearTimeout(tab.timer)
    tab.timer = null
    if (tab.buffered.length === 0) return

    const merged = new Uint8Array(tab.bufferedBytes)
    let offset = 0
    for (const chunk of tab.buffered) {
      merged.set(chunk, offset)
      offset += chunk.length
    }
    tab.buffered = []
    tab.bufferedBytes = 0

    this.emit('output', { tabId: tab.id, data: merged })
  }
}
