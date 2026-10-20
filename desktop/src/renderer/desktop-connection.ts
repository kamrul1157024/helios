import { bridge } from './bridge.ts'
import { closeAction, reconnectDelay } from './desktop-state.ts'
import type { Size } from './desktop-geometry.ts'
import { ipcTunnel, type GuacClient, type GuacDisplay, type GuacKeyboard, type Guacamole } from './guacamole.ts'

/** Where the line to the desktop stands, as the overlay reports it. */
export type LinkState =
  | { kind: 'connecting' }
  | { kind: 'connected' }
  | { kind: 'reconnecting'; attempt: number }
  /** The desktop stopped being ready; the view should read its state again. */
  | { kind: 'not-ready' }
  /** The host has the desktop turned off; the view should offer the switch. */
  | { kind: 'disabled' }
  | { kind: 'failed'; text: string }

export interface ConnectionOptions {
  hostId: string
  /** What to ask for on each dial. Read again on a redial, so a resize while
   *  the line was down is not lost. */
  size: () => Size
  dpi: number
  onLink: (state: LinkState) => void
  /** The remote screen changed size: guacd's answer to a resize, or the first frame. */
  onScreen: (size: Size) => void
}

/**
 * One viewer of a host's desktop: the Guacamole client, the input wired to it,
 * and the redialling when the line drops.
 *
 * A plain class rather than a hook, because its lifetime is a connection's and
 * not a render's: it owns timers, a client that is replaced on every dial, and
 * listeners on elements React never sees.
 *
 * Each dial is a fresh client. A Guacamole.Client that has disconnected does
 * not connect again cleanly, and a fresh one costs nothing that the desktop
 * would not resend anyway.
 */
export class DesktopConnection {
  private client: GuacClient | null = null
  private display: GuacDisplay | null = null
  private receive: ((data: string) => void) | null = null
  private tunnelId = ''
  private attempt = 0
  private reticketed = false
  private timer: number | null = null
  private disposed = false
  private scale = 1
  private readonly keyboard: GuacKeyboard
  private readonly unsubscribe: (() => void)[] = []
  /** The last clipboard text either side sent, so focus does not send it back. */
  private clipboard = ''

  private readonly G: Guacamole
  private readonly host: HTMLElement
  private readonly focusTarget: HTMLElement
  private readonly opts: ConnectionOptions

  /**
   * `host` holds the display; `focusTarget` is the element whose focus decides
   * whether keys and the clipboard reach the desktop at all.
   */
  constructor(G: Guacamole, host: HTMLElement, focusTarget: HTMLElement, opts: ConnectionOptions) {
    this.G = G
    this.host = host
    this.focusTarget = focusTarget
    this.opts = opts

    this.unsubscribe.push(
      bridge.desktop.onMessage(({ tunnelId, data }) => {
        if (tunnelId === this.tunnelId) this.receive?.(data)
      }),
      bridge.desktop.onClosed(({ tunnelId, code }) => {
        if (tunnelId === this.tunnelId) this.onClosed(code)
      }),
    )

    // Listening on the view itself, not the window, is what keeps the keyboard
    // the app's while the desktop is not focused.
    this.keyboard = new G.Keyboard(focusTarget)
    this.keyboard.onkeydown = (keysym) => {
      this.client?.sendKeyEvent(1, keysym)
    }
    this.keyboard.onkeyup = (keysym) => this.client?.sendKeyEvent(0, keysym)

    this.listen(focusTarget, 'keydown', (event) => {
      // While the desktop has the keyboard, ⌘W and the rest are the remote
      // app's. The window's own shortcuts listen above this, on `window`.
      event.stopPropagation()
    })
    // The library cancels mousedown, which would leave focus where it was.
    this.listen(focusTarget, 'pointerdown', () => focusTarget.focus())
    this.listen(focusTarget, 'focus', () => void this.pushClipboard())
    // A key held as focus leaves would otherwise stay down on the remote.
    this.listen(focusTarget, 'blur', () => this.keyboard.reset())
    this.listen(window, 'focus', () => {
      if (document.activeElement === focusTarget) void this.pushClipboard()
    })
  }

  start(): void {
    this.dial()
  }

  /** Asks the remote screen for a new size, over the open tunnel. */
  resize(size: Size): void {
    this.client?.sendSize(size.width, size.height)
  }

  setScale(scale: number): void {
    this.scale = scale
    this.display?.scale(scale)
  }

  /** Dials again from scratch, for Retry. */
  retry(): void {
    this.attempt = 0
    this.reticketed = false
    this.dial()
  }

  dispose(): void {
    this.disposed = true
    this.clearTimer()
    this.teardown()
    for (const off of this.unsubscribe) off()
    this.keyboard.onkeydown = null
    this.keyboard.onkeyup = null
  }

  private dial(): void {
    if (this.disposed) return
    this.clearTimer()
    this.teardown()
    this.opts.onLink(this.attempt === 0 ? { kind: 'connecting' } : { kind: 'reconnecting', attempt: this.attempt })

    const tunnelId = crypto.randomUUID()
    this.tunnelId = tunnelId
    const size = this.opts.size()
    const { tunnel, receive } = ipcTunnel(this.G, {
      open: () => void bridge.desktop.open({ tunnelId, hostId: this.opts.hostId, ...size, dpi: this.opts.dpi }),
      send: (data) => bridge.desktop.send(tunnelId, data),
      close: () => void bridge.desktop.close(tunnelId),
    })
    this.receive = receive

    const client = new this.G.Client(tunnel)
    this.client = client
    client.onstatechange = (state) => {
      if (state !== this.G.Client.State.CONNECTED) return
      this.attempt = 0
      this.reticketed = false
      this.opts.onLink({ kind: 'connected' })
    }
    // guacd's own `error` instruction: RDP refused, the session would not
    // start. Its message is for the daemon's log, not for the reader.
    client.onerror = () => this.fail('The desktop service reported an error. Try again, or check the host.')
    client.onclipboard = (stream, mimetype) => {
      if (!mimetype.startsWith('text/')) return
      const reader = new this.G.StringReader(stream)
      let text = ''
      reader.ontext = (chunk) => {
        text += chunk
      }
      reader.onend = () => {
        this.clipboard = text
        if (document.activeElement === this.focusTarget) void bridge.desktop.writeClipboard(text)
      }
    }

    const display = client.getDisplay()
    this.display = display
    display.scale(this.scale)
    display.onresize = (width, height) => this.opts.onScreen({ width, height })
    // The remote cursor, drawn by the pointer itself rather than as a layer that
    // trails it by a round trip.
    display.showCursor(false)
    const element = display.getElement()
    display.oncursor = (canvas, x, y) => {
      element.style.cursor = `url(${canvas.toDataURL()}) ${x} ${y}, default`
    }
    this.host.replaceChildren(element)

    const mouse = new this.G.Mouse(element)
    mouse.onEach(['mousedown', 'mouseup', 'mousemove'], (event) => client.sendMouseState(event.state, true))

    client.connect()
  }

  private onClosed(code: number): void {
    this.teardown()
    const action = closeAction(code, this.reticketed)
    switch (action.kind) {
      case 'reticket':
        this.reticketed = true
        this.dial()
        return
      // The view reads the state again on hearing this, and unmounts the
      // canvas if the desktop has gone back to installing. If it has not, the
      // dial after the pause is what finds out it is ready again.
      case 'disabled':
        this.opts.onLink({ kind: 'disabled' })
        return
      case 'refresh':
        this.opts.onLink({ kind: 'not-ready' })
        this.redialLater()
        return
      case 'reconnect':
        this.opts.onLink({ kind: 'reconnecting', attempt: this.attempt + 1 })
        this.redialLater()
        return
      case 'fail':
        this.fail(action.text)
    }
  }

  private redialLater(): void {
    this.timer = window.setTimeout(() => this.dial(), reconnectDelay(this.attempt))
    this.attempt += 1
  }

  private fail(text: string): void {
    this.teardown()
    this.opts.onLink({ kind: 'failed', text })
  }

  /** Drops the current client without telling anyone: the caller says what happens next. */
  private teardown(): void {
    const client = this.client
    this.client = null
    this.display = null
    this.receive = null
    this.tunnelId = ''
    if (!client) return
    client.onstatechange = null
    client.onerror = null
    client.onclipboard = null
    client.disconnect()
  }

  /** Sends the local clipboard to the remote, once per change. */
  private async pushClipboard(): Promise<void> {
    const client = this.client
    if (!client) return
    const text = await bridge.desktop.readClipboard()
    if (!text || text === this.clipboard || client !== this.client) return
    this.clipboard = text
    const writer = new this.G.StringWriter(client.createClipboardStream('text/plain'))
    writer.sendText(text)
    writer.sendEnd()
  }

  private listen<K extends keyof HTMLElementEventMap>(
    target: HTMLElement | Window,
    type: K,
    listener: (event: HTMLElementEventMap[K]) => void,
  ): void {
    target.addEventListener(type, listener as EventListener)
    this.unsubscribe.push(() => target.removeEventListener(type, listener as EventListener))
  }

  private clearTimer(): void {
    if (this.timer !== null) window.clearTimeout(this.timer)
    this.timer = null
  }
}
