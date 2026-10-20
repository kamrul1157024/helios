/**
 * guacamole-common-js 1.6.0, loaded on demand, and the one tunnel it needs.
 *
 * The library is vendored from the guacamole-client release (spec 66, D7) and
 * copied beside the bundle unchanged. It is a classic script that defines a
 * global, so it arrives the way mermaid does: a sibling <script> under the
 * window's `script-src 'self'`, fetched the first time a desktop is opened
 * rather than parsed by every window at startup.
 *
 * Only the surface this app touches is typed here.
 */

export interface GuacStatus {
  code: number
  message?: string
}

export interface GuacTunnel {
  state: number
  uuid: string | null
  connect(data?: string): void
  disconnect(): void
  sendMessage(...elements: unknown[]): void
  setState(state: number): void
  setUUID(uuid: string): void
  isConnected(): boolean
  oninstruction: ((opcode: string, args: string[]) => void) | null
  onerror: ((status: GuacStatus) => void) | null
}

export interface GuacDisplay {
  getElement(): HTMLElement
  getWidth(): number
  getHeight(): number
  scale(scale: number): void
  showCursor(shown: boolean): void
  onresize: ((width: number, height: number) => void) | null
  oncursor: ((canvas: HTMLCanvasElement, x: number, y: number) => void) | null
}

export interface GuacMouseState {
  x: number
  y: number
  left: boolean
  middle: boolean
  right: boolean
  up: boolean
  down: boolean
}

export interface GuacInputStream {
  index: number
}

export interface GuacOutputStream {
  index: number
}

export interface GuacClient {
  connect(data?: string): void
  disconnect(): void
  getDisplay(): GuacDisplay
  sendSize(width: number, height: number): void
  sendKeyEvent(pressed: 0 | 1, keysym: number): void
  sendMouseState(state: GuacMouseState, applyDisplayScale?: boolean): void
  createClipboardStream(mimetype: string): GuacOutputStream
  onstatechange: ((state: number) => void) | null
  onerror: ((status: GuacStatus) => void) | null
  onclipboard: ((stream: GuacInputStream, mimetype: string) => void) | null
}

export interface GuacMouse {
  onEach(types: string[], listener: (event: { state: GuacMouseState }) => void): void
}

export interface GuacKeyboard {
  onkeydown: ((keysym: number) => boolean | void) | null
  onkeyup: ((keysym: number) => void) | null
  reset(): void
}

interface GuacParser {
  receive(packet: string): void
  oninstruction: ((opcode: string, args: string[]) => void) | null
}

export interface Guacamole {
  Client: { new (tunnel: GuacTunnel): GuacClient; State: { CONNECTED: number; DISCONNECTED: number } }
  Tunnel: {
    new (): GuacTunnel
    State: { CONNECTING: number; OPEN: number; CLOSED: number; UNSTABLE: number }
    INTERNAL_DATA_OPCODE: string
  }
  Parser: { new (): GuacParser; toInstruction(elements: ArrayLike<unknown>): string }
  Mouse: new (element: Element) => GuacMouse
  Keyboard: new (element: Element) => GuacKeyboard
  StringReader: new (stream: GuacInputStream) => { ontext: ((text: string) => void) | null; onend: (() => void) | null }
  StringWriter: new (stream: GuacOutputStream) => { sendText(text: string): void; sendEnd(): void }
}

declare global {
  interface Window {
    Guacamole?: Guacamole
  }
}

/** Where build.mjs copies the vendored file, beside the bundle. */
const SCRIPT = 'vendor/guacamole-common-js-1.6.0/guacamole-common.min.js'

let loading: Promise<Guacamole> | null = null

export function loadGuacamole(): Promise<Guacamole> {
  loading ??= new Promise<Guacamole>((resolve, reject) => {
    const script = document.createElement('script')
    script.src = SCRIPT
    script.onload = () => {
      if (window.Guacamole) resolve(window.Guacamole)
      else reject(new Error('the desktop client loaded but defined nothing'))
    }
    script.onerror = () => {
      // Not cached as a failure: a second open should try again.
      loading = null
      reject(new Error('the desktop client could not be loaded'))
    }
    document.head.appendChild(script)
  })
  return loading
}

/** What the tunnel asks of whoever carries its bytes. */
export interface TunnelCarrier {
  open(): void
  send(data: string): void
  close(): void
}

/**
 * A Guacamole tunnel whose socket lives in the main process.
 *
 * The library's own WebSocketTunnel dials from the page, which this window's
 * `connect-src 'none'` forbids — and it would need the ticket in the page
 * too. This keeps the library's half of the bargain instead: parse the frames,
 * take the first instruction as the tunnel's UUID (the daemon sends it as the
 * internal opcode, exactly as WebSocketTunnel expects), and encode what the
 * client sends. Liveness is the main process's job, by WebSocket ping, so the
 * internal pings and receive timeout of WebSocketTunnel are not repeated here.
 */
export function ipcTunnel(G: Guacamole, carrier: TunnelCarrier): { tunnel: GuacTunnel; receive(data: string): void } {
  const tunnel = new G.Tunnel()
  const { CONNECTING, OPEN, CLOSED } = G.Tunnel.State
  let parser: GuacParser | null = null

  tunnel.connect = () => {
    tunnel.setState(CONNECTING)
    parser = new G.Parser()
    parser.oninstruction = (opcode, args) => {
      if (tunnel.uuid === null) {
        if (opcode === G.Tunnel.INTERNAL_DATA_OPCODE && args.length === 1) tunnel.setUUID(args[0] as string)
        tunnel.setState(OPEN)
      }
      if (opcode !== G.Tunnel.INTERNAL_DATA_OPCODE) tunnel.oninstruction?.(opcode, args)
    }
    carrier.open()
  }

  tunnel.sendMessage = (...elements: unknown[]) => {
    if (!tunnel.isConnected() || elements.length === 0) return
    carrier.send(G.Parser.toInstruction(elements))
  }

  tunnel.disconnect = () => {
    if (tunnel.state === CLOSED) return
    tunnel.setState(CLOSED)
    carrier.close()
  }

  const receive = (data: string): void => {
    try {
      parser?.receive(data)
    } catch (err) {
      tunnel.onerror?.({ code: 0x0200, message: err instanceof Error ? err.message : String(err) })
      tunnel.disconnect()
    }
  }

  return { tunnel, receive }
}
