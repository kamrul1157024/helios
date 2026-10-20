// The remote desktop routes of the stub daemon (spec 66), and a guacd that
// draws one frame.
//
// The real tunnel is the daemon relaying guacd byte for byte, so a stand-in
// that speaks the same instructions is all the app can tell apart. It sends
// the tunnel UUID, a `size`, a filled rectangle, a small PNG and a `sync` —
// enough for guacamole-common-js to draw a screen worth checking pixels on —
// and answers a `size` from the client the way guacd does, by resizing.
import type http from 'node:http'
import type { Duplex } from 'node:stream'
import zlib from 'node:zlib'

import { WebSocketServer, type WebSocket } from 'ws'

import type { DesktopState, DesktopStep } from '../src/shared/models.ts'

/** The fill behind everything, and the PNG drawn over its corner. */
export const BACKGROUND = [40, 90, 200] as const
export const PICTURE = [0, 200, 120] as const
/** Where the PNG lands, and how big it is. */
export const PICTURE_AT = 16
export const PICTURE_SIZE = 64

const STEPS: Pick<DesktopStep, 'id' | 'label'>[] = [
  { id: 'packages', label: 'Install xrdp and XFCE' },
  { id: 'xrdp-config', label: 'Configure xrdp' },
  { id: 'pam', label: 'Let Helios sign in' },
  { id: 'session', label: 'Set up the XFCE session' },
  { id: 'guacd', label: 'Get the desktop service' },
  { id: 'start', label: 'Start the desktop' },
]

function steps(state: DesktopStep['state']): DesktopStep[] {
  return STEPS.map((step) => ({ ...step, state, line: '' }))
}

function readyState(): DesktopState {
  return {
    enabled: true,
    state: 'ready',
    missing: [],
    can_install: true,
    install_command: 'helios setup desktop',
    display: ':10',
    dpi: 96,
    viewers: 0,
    error: '',
    steps: steps('done'),
  }
}

/** What a test can see and steer. */
export interface DesktopStub {
  /** Replaces part of the state, and says so on the event stream. */
  set(patch: Partial<DesktopState>): void
  /** Starts from a host with nothing installed and working sudo. */
  setMissing(): void
  /** Ends a running install: every step done, the desktop ready. */
  finishInstall(): void
  /**
   * Turns the desktop off without an event: a view that already read the
   * state still holds "on", and install and ticket answer 409 "disabled".
   */
  turnOffQuietly(): void
  /** Ends the next tunnel with this close code as soon as it opens. */
  refuseNext(code: number): void
  /** Cuts every open tunnel without a close frame, as a dropped network does. */
  drop(): void
  /** Puts text on the remote clipboard, as copying inside the desktop does. */
  copyRemote(text: string): void
  tickets(): number
  installs(): number
  /** The query of every tunnel dialled, in order. */
  tunnels(): URLSearchParams[]
  /** Every instruction the client sent, as [opcode, ...args]. */
  received(): string[][]
  /** Every DPI the app asked for. */
  dpiWrites(): number[]
}

export function desktopStub(emit: (type: string, data: unknown) => void) {
  let state = readyState()
  const issued = new Set<string>()
  let ticketCount = 0
  let quietlyOff = false
  let installCount = 0
  const refusals: number[] = []
  const dialled: URLSearchParams[] = []
  const instructions: string[][] = []
  const dpis: number[] = []
  const wss = new WebSocketServer({ noServer: true, handleProtocols: (protocols) => (protocols.has('guacamole') ? 'guacamole' : false) })

  const set = (patch: Partial<DesktopState>): void => {
    state = { ...state, ...patch }
    emit('desktop_state', state)
  }

  const stub: DesktopStub = {
    set,
    setMissing: () => {
      state = { ...readyState(), state: 'missing', missing: ['xrdp', 'xfce', 'guacd'], display: '', dpi: 0, steps: steps('pending') }
    },
    finishInstall: () => set({ state: 'ready', missing: [], display: ':10', dpi: 96, steps: steps('done') }),
    turnOffQuietly: () => {
      quietlyOff = true
    },
    refuseNext: (code) => refusals.push(code),
    drop: () => {
      for (const client of wss.clients) client.terminate()
    },
    copyRemote: (text) => {
      const frame =
        instruction('clipboard', 2, 'text/plain') +
        instruction('blob', 2, Buffer.from(text).toString('base64')) +
        instruction('end', 2)
      for (const client of wss.clients) client.send(frame)
    },
    tickets: () => ticketCount,
    installs: () => installCount,
    tunnels: () => [...dialled],
    received: () => instructions.map((one) => [...one]),
    dpiWrites: () => [...dpis],
  }

  const json = (res: http.ServerResponse, status: number, body: unknown): void => {
    res.writeHead(status, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify(body))
  }

  const off = (): boolean => quietlyOff || state.enabled === false
  const refuseOff = (res: http.ServerResponse): void =>
    json(res, 409, { code: 'disabled', error: 'Remote desktop is turned off on this host.' })

  /** Answers a desktop route. False for anything else. */
  const handle = (req: http.IncomingMessage, res: http.ServerResponse, path: string): boolean => {
    if (path === '/api/desktop' && req.method === 'GET') {
      // A fresh read tells the truth; only the copy the view already holds is stale.
      json(res, 200, { ...state, enabled: !off() })
      return true
    }
    if (path === '/api/desktop/enable' && req.method === 'POST') {
      quietlyOff = false
      state = { ...state, enabled: true }
      json(res, 200, state)
      emit('desktop_state', state)
      return true
    }
    if ((path === '/api/desktop/install' || path === '/api/desktop/ticket') && req.method === 'POST' && off()) {
      refuseOff(res)
      return true
    }
    if (path === '/api/desktop/install' && req.method === 'POST') {
      installCount += 1
      json(res, 202, { started: true })
      // The first step running and the rest waiting, then nothing until the
      // test says so: progress on a timer would race the screenshot.
      const running = steps('pending')
      running[0] = { ...(running[0] as DesktopStep), state: 'running', line: 'Unpacking xorgxrdp (1:0.10.1-1) ...' }
      set({ state: 'installing', steps: running })
      return true
    }
    if (path === '/api/desktop/ticket' && req.method === 'POST') {
      ticketCount += 1
      const ticket = `ticket-${ticketCount}`
      issued.add(ticket)
      json(res, 200, { ticket, expires_at: new Date(Date.now() + 30_000).toISOString() })
      return true
    }
    if (path === '/api/desktop/display' && req.method === 'PUT') {
      const chunks: Buffer[] = []
      req.on('data', (chunk: Buffer) => chunks.push(chunk))
      req.on('end', () => {
        const { dpi } = JSON.parse(Buffer.concat(chunks).toString() || '{}') as { dpi?: number }
        if (typeof dpi !== 'number' || dpi < 72 || dpi > 288) {
          json(res, 400, { error: 'dpi out of range', code: 'bad_dpi' })
          return
        }
        dpis.push(dpi)
        json(res, 200, { dpi })
        set({ dpi })
      })
      return true
    }
    return false
  }

  const upgrade = (req: http.IncomingMessage, socket: Duplex, head: Buffer): void => {
    const url = new URL(req.url ?? '/', 'http://127.0.0.1')
    if (url.pathname !== '/api/desktop/tunnel') {
      socket.destroy()
      return
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      dialled.push(url.searchParams)
      const ticket = url.searchParams.get('ticket') ?? ''
      const refusal = refusals.shift()
      if (refusal !== undefined) {
        ws.close(refusal, 'refused by the test')
        return
      }
      if (!issued.delete(ticket)) {
        ws.close(4401, 'bad ticket')
        return
      }
      if (state.state !== 'ready') {
        ws.close(4503, 'desktop not ready')
        return
      }
      guacd(ws, Number(url.searchParams.get('width')), Number(url.searchParams.get('height')))
    })
  }

  /** The instructions guacd would send for one connection. */
  const guacd = (ws: WebSocket, width: number, height: number): void => {
    let stamp = 1
    const frame = (w: number, h: number): void => {
      ws.send(
        instruction('size', 0, w, h) +
          instruction('rect', 0, 0, 0, w, h) +
          instruction('cfill', 14, 0, ...BACKGROUND, 255) +
          instruction('img', 1, 14, 0, 'image/png', PICTURE_AT, PICTURE_AT) +
          instruction('blob', 1, png(PICTURE_SIZE, PICTURE).toString('base64')) +
          instruction('end', 1) +
          instruction('sync', stamp++),
      )
    }
    ws.send(instruction('', 'e2e00000-0000-4000-8000-000000000000'))
    frame(width, height)
    ws.on('message', (data) => {
      for (const parsed of parse(data.toString())) {
        instructions.push(parsed)
        const [opcode, ...args] = parsed
        if (opcode === 'size') frame(Number(args[0]), Number(args[1]))
        if (opcode === 'disconnect') ws.close(1000)
      }
    })
  }

  return {
    stub,
    handle,
    upgrade,
    close: () => {
      for (const client of wss.clients) client.terminate()
      wss.close()
    },
  }
}

/** One Guacamole instruction: each element as length.value, comma-joined, ended by a semicolon. */
function instruction(...elements: (string | number)[]): string {
  return elements.map((element) => `${String(element).length}.${element}`).join(',') + ';'
}

/** The instructions in one text frame. Lengths count characters, which for this ASCII is bytes. */
function parse(text: string): string[][] {
  const out: string[][] = []
  let current: string[] = []
  let at = 0
  while (at < text.length) {
    const dot = text.indexOf('.', at)
    if (dot === -1) break
    const length = Number(text.slice(at, dot))
    current.push(text.slice(dot + 1, dot + 1 + length))
    const end = text[dot + 1 + length]
    at = dot + 2 + length
    if (end === ';') {
      out.push(current)
      current = []
    }
  }
  return out
}

/** A square PNG of one colour, written by hand: signature, IHDR, one IDAT, IEND. */
function png(size: number, [r, g, b]: readonly [number, number, number]): Buffer {
  const row = Buffer.alloc(1 + size * 3)
  for (let x = 0; x < size; x++) row.set([r, g, b], 1 + x * 3)
  const raw = Buffer.concat(Array.from({ length: size }, () => row))
  const chunk = (type: string, data: Buffer): Buffer => {
    const length = Buffer.alloc(4)
    length.writeUInt32BE(data.length)
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data])
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(zlib.crc32(body))
    return Buffer.concat([length, body, crc])
  }
  const header = Buffer.alloc(13)
  header.writeUInt32BE(size, 0)
  header.writeUInt32BE(size, 4)
  header.set([8, 2, 0, 0, 0], 8)
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header),
    chunk('IDAT', zlib.deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ])
}
