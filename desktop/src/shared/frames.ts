// Port of internal/terminal/protocol.go. The wire format is shared with the Go
// host and the two are pinned together by golden fixtures — see
// internal/terminal/golden_test.go and test/frames.test.ts.
//
// Length-prefixed: uint32 len(type + payload), uint8 type, payload.

// A const object rather than an enum: enums are not erasable, so they break
// Node's --experimental-strip-types, which is how the conformance tests run.
export const FrameType = {
  Hello: 0x01,
  Snapshot: 0x02,
  Output: 0x03,
  Input: 0x04,
  Resize: 0x05,
  Status: 0x06,
  Exit: 0x07,
  Ping: 0x08,
  Pong: 0x09,
} as const

export type FrameType = (typeof FrameType)[keyof typeof FrameType]

/** Bounds a single frame so a corrupt length prefix cannot exhaust memory. */
export const MAX_FRAME_SIZE = 8 << 20 // 8 MiB

export const HEADER_SIZE = 5

export type Role = 'interactive' | 'observer'

export interface Hello {
  role: Role
  cols: number
  rows: number
  /** Replay position. Zero means "snapshot me". */
  since: number
  name?: string
}

export type HostState = 'warming' | 'ready' | 'busy' | 'exited'

export interface Status {
  state: HostState
  writer?: string
  viewers: number
  cols: number
  rows: number
}

export interface Frame {
  type: FrameType
  payload: Uint8Array
}

export class FrameTooLargeError extends Error {
  constructor(size: number) {
    super(`terminal: frame of ${size} bytes exceeds the ${MAX_FRAME_SIZE} byte maximum`)
    this.name = 'FrameTooLargeError'
  }
}

const EMPTY = new Uint8Array(0)

/** Encodes one frame, header included, ready for the wire. */
export function encodeFrame(type: FrameType, payload: Uint8Array = EMPTY): Uint8Array {
  if (payload.length + 1 > MAX_FRAME_SIZE) throw new FrameTooLargeError(payload.length + 1)
  const out = new Uint8Array(HEADER_SIZE + payload.length)
  new DataView(out.buffer).setUint32(0, payload.length + 1, false)
  out[4] = type
  out.set(payload, HEADER_SIZE)
  return out
}

export function encodeJSONFrame(type: FrameType, value: unknown): Uint8Array {
  return encodeFrame(type, new TextEncoder().encode(JSON.stringify(value)))
}

/** Prefixes ANSI resync bytes with the sequence they correspond to. */
export function encodeSnapshot(seq: number | bigint, ansi: Uint8Array): Uint8Array {
  const out = new Uint8Array(8 + ansi.length)
  new DataView(out.buffer).setBigUint64(0, BigInt(seq), false)
  out.set(ansi, 8)
  return out
}

export function decodeSnapshot(payload: Uint8Array): { seq: number; ansi: Uint8Array } {
  if (payload.length < 8) throw new Error('terminal: snapshot payload too short')
  const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength)
  // Sequence counts bytes streamed. Number is exact to 2^53, which at the
  // 1 MiB ring's throughput is not reachable in any real session.
  return { seq: Number(view.getBigUint64(0, false)), ansi: payload.subarray(8) }
}

export function encodeResize(cols: number, rows: number): Uint8Array {
  const out = new Uint8Array(4)
  const view = new DataView(out.buffer)
  view.setUint16(0, cols, false)
  view.setUint16(2, rows, false)
  return out
}

export function decodeResize(payload: Uint8Array): { cols: number; rows: number } {
  if (payload.length < 4) throw new Error('terminal: resize payload too short')
  const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength)
  return { cols: view.getUint16(0, false), rows: view.getUint16(2, false) }
}

export function decodeExit(payload: Uint8Array): number {
  if (payload.length < 4) return 0
  const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength)
  return view.getInt32(0, false)
}

export function decodeStatus(payload: Uint8Array): Status {
  return JSON.parse(new TextDecoder().decode(payload)) as Status
}

/**
 * Incremental frame parser.
 *
 * Both transports are byte streams — the unix socket obviously, and the
 * WebSocket because the daemon relays with io.Copy over websocket.NetConn, so
 * message boundaries carry no meaning (terminal_ws.go:78). Neither one may be
 * treated as one-frame-per-chunk.
 */
export class FrameParser {
  // Buffered bytes not yet forming a complete frame. Kept as a single
  // Uint8Array rather than a chunk list because frames are small and the
  // common case is a chunk holding several whole frames plus a fragment.
  private buf: Uint8Array = EMPTY

  /** Feeds bytes in and returns every frame that completed. */
  push(chunk: Uint8Array): Frame[] {
    this.buf = this.buf.length === 0 ? chunk : concat(this.buf, chunk)

    const frames: Frame[] = []
    let offset = 0
    while (this.buf.length - offset >= HEADER_SIZE) {
      const view = new DataView(this.buf.buffer, this.buf.byteOffset + offset, HEADER_SIZE)
      const length = view.getUint32(0, false)
      if (length === 0) throw new Error('terminal: zero-length frame')
      if (length > MAX_FRAME_SIZE) throw new FrameTooLargeError(length)
      if (this.buf.length - offset < HEADER_SIZE + length - 1) break

      const type = this.buf[offset + 4] as FrameType
      const start = offset + HEADER_SIZE
      // Copied rather than subarray'd: the payload outlives this.buf, which is
      // about to be sliced away, and a retained view would pin the whole chunk.
      frames.push({ type, payload: this.buf.slice(start, start + length - 1) })
      offset = start + length - 1
    }

    this.buf = offset === 0 ? this.buf : this.buf.subarray(offset)
    return frames
  }

  reset(): void {
    this.buf = EMPTY
  }
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(a.length + b.length)
  out.set(a, 0)
  out.set(b, a.length)
  return out
}
