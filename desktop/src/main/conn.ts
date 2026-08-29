import { EventEmitter } from 'node:events'
import net from 'node:net'
import WebSocket from 'ws'

import {
  FrameParser,
  FrameType,
  decodeExit,
  decodeSnapshot,
  decodeStatus,
  encodeFrame,
  encodeJSONFrame,
  encodeResize,
  type Role,
  type Status,
} from '../shared/frames.ts'

/** Where a tab's bytes come from. */
export type Endpoint =
  | { kind: 'unix'; path: string }
  | { kind: 'ws'; url: string; token: () => Promise<string> }

export interface TerminalConnOptions {
  endpoint: Endpoint
  cols: number
  rows: number
  role?: Role
  name?: string
  /** Called when the connection needs a token refreshed after a 401-ish close. */
  onAuthFailure?: () => void
}

/** Backoff schedule from spec 31: 0.5s, 1s, 2s, 4s, then 8s forever. */
const RECONNECT_DELAYS = [500, 1_000, 2_000, 4_000, 8_000]

const PING_INTERVAL = 10_000
const PONG_TIMEOUT = 5_000
const DIAL_TIMEOUT = 5_000

/**
 * One viewer connection to a session's terminal host.
 *
 * Both transports are byte streams carrying the same frames — the daemon's
 * WebSocket handler is an io.Copy relay, not a translator — so everything
 * above the socket is shared and only `dial` differs.
 *
 * Events: `output` (Uint8Array), `snapshot` (Uint8Array), `status` (Status),
 * `state` (connecting | live | reconnecting), `exit` (code), `close` (reason).
 */
export class TerminalConn extends EventEmitter {
  private socket: net.Socket | WebSocket | null = null
  private parser = new FrameParser()
  private closed = false
  private attempt = 0

  /**
   * Bytes consumed, tracked exactly as internal/terminal/client.go does it: add
   * every Output payload's length, adopt a Snapshot's sequence wholesale. It is
   * what lets a reconnect resume instead of resync.
   */
  private seq = 0

  private cols: number
  private rows: number
  /** Last size actually sent, so a redundant resize does not disturb negotiation. */
  private sentCols = -1
  private sentRows = -1

  private pingTimer: NodeJS.Timeout | null = null
  private pongTimer: NodeJS.Timeout | null = null
  private reconnectTimer: NodeJS.Timeout | null = null

  // Assigned rather than declared as a parameter property: `node --test`
  // strips types without compiling them, and a parameter property is syntax it
  // refuses, which would put this whole file out of reach of the test suite.
  private readonly opts: TerminalConnOptions

  constructor(opts: TerminalConnOptions) {
    super()
    this.opts = opts
    this.cols = opts.cols
    this.rows = opts.rows
  }

  async start(): Promise<void> {
    await this.connect()
  }

  private async connect(): Promise<void> {
    if (this.closed) return
    this.emit('state', this.attempt === 0 ? 'connecting' : 'reconnecting')
    this.parser.reset()
    this.sentCols = -1
    this.sentRows = -1

    try {
      const socket = await this.dial()
      if (this.closed) {
        destroy(socket)
        return
      }
      this.socket = socket
      this.attempt = 0
      this.emit('state', 'live')
      this.sendHello()
      this.startHeartbeat()
    } catch (err) {
      this.scheduleReconnect(err instanceof Error ? err.message : String(err))
    }
  }

  private async dial(): Promise<net.Socket | WebSocket> {
    if (this.opts.endpoint.kind === 'unix') {
      return this.dialUnix(this.opts.endpoint.path)
    }
    return this.dialWebSocket(this.opts.endpoint)
  }

  /**
   * Dialling again cannot help: the host is gone, not slow.
   *
   * The daemon can name a socket whose host has died — it forgets one only when
   * its reaper next runs — and without this the tab retries that path every
   * eight seconds for as long as the window is open.
   */
  private static readonly GONE = new Set(['ENOENT', 'ECONNREFUSED'])

  private dialUnix(path: string): Promise<net.Socket> {
    return new Promise((resolve, reject) => {
      const socket = net.connect(path)
      const timer = setTimeout(() => {
        socket.destroy()
        reject(new Error(`terminal host did not accept within ${DIAL_TIMEOUT}ms`))
      }, DIAL_TIMEOUT)

      socket.once('connect', () => {
        clearTimeout(timer)
        socket.on('data', (chunk) => this.consume(new Uint8Array(chunk)))
        socket.on('error', (err) => this.onTransportError(err.message))
        socket.on('close', () => this.onTransportClose('terminal host closed the socket'))
        resolve(socket)
      })
      socket.once('error', (err: NodeJS.ErrnoException) => {
        clearTimeout(timer)
        if (err.code && TerminalConn.GONE.has(err.code)) {
          this.fail('session has no live terminal')
        }
        reject(err)
      })
    })
  }

  private async dialWebSocket(endpoint: { url: string; token: () => Promise<string> }): Promise<WebSocket> {
    // The header is the reason all network I/O lives in the main process: a
    // browser WebSocket cannot set Authorization, and the renderer must never
    // hold the device key to begin with.
    const token = await endpoint.token()
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(endpoint.url, {
        headers: { Authorization: `Bearer ${token}` },
        handshakeTimeout: DIAL_TIMEOUT,
        // The stream is long-lived and entirely client-paced; a redraw storm is
        // not a reason to tear it down.
        maxPayload: 64 << 20,
      })

      ws.binaryType = 'nodebuffer'
      ws.once('open', () => {
        ws.on('message', (data) => this.consume(toBytes(data)))
        ws.on('error', (err: Error) => this.onTransportError(err.message))
        ws.on('close', (code: number, reason: Buffer) =>
          this.onTransportClose(reason.length ? reason.toString() : `websocket closed (${code})`),
        )
        resolve(ws)
      })
      ws.once('unexpected-response', (_req: unknown, res: { statusCode?: number }) => {
        const status = res.statusCode ?? 0
        if (status === 401 || status === 403) this.opts.onAuthFailure?.()
        // 409 is the daemon telling us the session is cold. Reconnecting will
        // not fix that, and waking on a retry is how a closed laptop lid turns
        // into a fleet of resurrected agents.
        if (status === 409) {
          this.fail('session has no live terminal')
          reject(new Error('cold'))
          return
        }
        reject(new Error(`terminal endpoint returned ${status}`))
      })
      ws.once('error', (err: Error) => reject(err))
    })
  }

  private sendHello(): void {
    this.write(
      encodeJSONFrame(FrameType.Hello, {
        role: this.opts.role ?? 'interactive',
        cols: this.cols,
        rows: this.rows,
        since: this.seq,
        name: this.opts.name ?? 'desktop',
      }),
    )
    this.sentCols = this.cols
    this.sentRows = this.rows
  }

  private consume(chunk: Uint8Array): void {
    let frames
    try {
      frames = this.parser.push(chunk)
    } catch (err) {
      // A framing error means the stream is no longer trustworthy; resync.
      this.onTransportError(err instanceof Error ? err.message : String(err))
      return
    }

    for (const frame of frames) {
      switch (frame.type) {
        case FrameType.Output:
          this.seq += frame.payload.length
          this.emit('output', frame.payload)
          break
        case FrameType.Snapshot: {
          const { seq, ansi } = decodeSnapshot(frame.payload)
          this.seq = seq
          this.emit('snapshot', ansi)
          break
        }
        case FrameType.Status:
          this.emit('status', decodeStatus(frame.payload) satisfies Status)
          break
        case FrameType.Exit:
          this.emit('exit', decodeExit(frame.payload))
          this.fail('session process exited')
          break
        case FrameType.Pong:
          this.clearPongTimer()
          break
        default:
          // Hello, Input and Resize are client-to-host; a host that sent one
          // is a host we do not understand, and ignoring it is safe.
          break
      }
    }
  }

  // ─── Outbound ──────────────────────────────────────────────────────────

  input(bytes: Uint8Array): void {
    this.write(encodeFrame(FrameType.Input, bytes))
  }

  /**
   * Declares this viewer's size, or withdraws from negotiation with 0×0.
   *
   * The host adopts the smallest size any interactive viewer declares, so a
   * background tab that kept voting would shrink the PTY for a full-screen
   * `helios attach`. negotiateSize skips viewers with cols <= 0 and Host.Resize
   * early-returns on a non-positive size, so zero parks the vote without ever
   * reaching the PTY.
   */
  resize(cols: number, rows: number): void {
    if (cols === this.sentCols && rows === this.sentRows) return
    this.sentCols = cols
    this.sentRows = rows
    if (cols > 0 && rows > 0) {
      // Remembered so a reconnect's Hello carries the real geometry.
      this.cols = cols
      this.rows = rows
    }
    this.write(encodeFrame(FrameType.Resize, encodeResize(Math.max(0, cols), Math.max(0, rows))))
  }

  private write(frame: Uint8Array): void {
    const socket = this.socket
    if (!socket) return
    try {
      if (socket instanceof net.Socket) socket.write(frame)
      else if (socket.readyState === WebSocket.OPEN) socket.send(frame)
    } catch (err) {
      this.onTransportError(err instanceof Error ? err.message : String(err))
    }
  }

  // ─── Heartbeat ─────────────────────────────────────────────────────────

  /**
   * Detects the zombie described in spec 31: a viewer dropped for overrunning
   * its 64-frame queue keeps a socket that reads fine and writes nowhere, so
   * connection state is useless as a signal. It answers no pings, and that is
   * the only thing that distinguishes it.
   */
  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.pingTimer = setInterval(() => {
      if (this.pongTimer) return // a ping is already outstanding
      this.write(encodeFrame(FrameType.Ping))
      this.pongTimer = setTimeout(() => {
        this.pongTimer = null
        this.onTransportError('terminal host stopped answering pings')
      }, PONG_TIMEOUT)
    }, PING_INTERVAL)
  }

  private stopHeartbeat(): void {
    if (this.pingTimer) clearInterval(this.pingTimer)
    this.pingTimer = null
    this.clearPongTimer()
  }

  private clearPongTimer(): void {
    if (this.pongTimer) clearTimeout(this.pongTimer)
    this.pongTimer = null
  }

  // ─── Teardown and retry ────────────────────────────────────────────────

  private onTransportError(reason: string): void {
    if (this.closed) return
    this.teardownSocket()
    this.scheduleReconnect(reason)
  }

  private onTransportClose(reason: string): void {
    if (this.closed || !this.socket) return
    this.teardownSocket()
    this.scheduleReconnect(reason)
  }

  private teardownSocket(): void {
    this.stopHeartbeat()
    const socket = this.socket
    this.socket = null
    if (socket) destroy(socket)
  }

  private scheduleReconnect(reason: string): void {
    if (this.closed || this.reconnectTimer) return
    const delay = RECONNECT_DELAYS[Math.min(this.attempt, RECONNECT_DELAYS.length - 1)]!
    this.attempt++
    this.emit('state', 'reconnecting')
    this.emit('detail', reason)
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      void this.connect()
    }, delay)
  }

  /** Gives up permanently — the session is gone, not merely unreachable. */
  private fail(reason: string): void {
    if (this.closed) return
    this.closed = true
    this.teardownSocket()
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.reconnectTimer = null
    this.emit('state', 'closed')
    this.emit('close', reason)
  }

  /**
   * Detaches this viewer. It never kills the host: closing a tab is closing a
   * window, and `helios ptyhost` is spawned detached precisely so it survives.
   */
  close(): void {
    if (this.closed) return
    this.closed = true
    this.teardownSocket()
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.reconnectTimer = null
    this.emit('state', 'closed')
    this.emit('close', 'detached')
  }

  get sequence(): number {
    return this.seq
  }
}

function destroy(socket: net.Socket | WebSocket): void {
  if (socket instanceof net.Socket) socket.destroy()
  else socket.terminate()
}

function toBytes(data: WebSocket.RawData): Uint8Array {
  if (Buffer.isBuffer(data)) return new Uint8Array(data)
  if (Array.isArray(data)) return new Uint8Array(Buffer.concat(data))
  return new Uint8Array(data as ArrayBuffer)
}
