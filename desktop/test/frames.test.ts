// Conformance tests against the fixtures Go writes in
// internal/terminal/golden_test.go. If either codec drifts, one of these fails.
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  FrameParser,
  FrameType,
  MAX_FRAME_SIZE,
  decodeExit,
  decodeResize,
  decodeSnapshot,
  decodeStatus,
  encodeFrame,
  encodeJSONFrame,
  encodeResize,
  encodeSnapshot,
} from '../src/shared/frames.ts'

const fixtures = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../internal/terminal/testdata/frames',
)

function fixture(name: string): Uint8Array {
  return new Uint8Array(readFileSync(resolve(fixtures, `${name}.bin`)))
}

/** Parses a fixture that holds exactly one frame. */
function parseOne(name: string) {
  const frames = new FrameParser().push(fixture(name))
  assert.equal(frames.length, 1, `${name} should hold exactly one frame`)
  return frames[0]!
}

test('hello fixture decodes to the Go struct', () => {
  const frame = parseOne('hello')
  assert.equal(frame.type, FrameType.Hello)
  assert.deepEqual(JSON.parse(new TextDecoder().decode(frame.payload)), {
    role: 'interactive',
    cols: 120,
    rows: 34,
    since: 4096,
    name: 'desktop',
  })
})

test('hello re-encodes byte-identically', () => {
  // Key order matters for a byte comparison, and this is the order Go's struct
  // field order produces.
  const encoded = encodeJSONFrame(FrameType.Hello, {
    role: 'interactive',
    cols: 120,
    rows: 34,
    since: 4096,
    name: 'desktop',
  })
  assert.deepEqual(encoded, fixture('hello'))
})

test('snapshot carries a 64-bit sequence', () => {
  const frame = parseOne('snapshot')
  assert.equal(frame.type, FrameType.Snapshot)
  const { seq, ansi } = decodeSnapshot(frame.payload)
  // 1<<40 — past 32 bits, which is the point of the fixture.
  assert.equal(seq, 1099511627776)
  assert.equal(new TextDecoder().decode(ansi), '\x1b[2J\x1b[H\x1b[32mready\x1b[0m')
  assert.deepEqual(encodeFrame(FrameType.Snapshot, encodeSnapshot(seq, ansi)), fixture('snapshot'))
})

test('output survives multi-byte utf-8 unchanged', () => {
  const frame = parseOne('output')
  assert.equal(frame.type, FrameType.Output)
  // Emoji and CJK are where a codec that treated payloads as strings breaks.
  assert.equal(new TextDecoder().decode(frame.payload), '\x1b[1mhello\x1b[0m 🎉 中文\r\n')
  assert.deepEqual(encodeFrame(FrameType.Output, frame.payload), fixture('output'))
})

test('input fixture is a shift-tab keystroke', () => {
  const frame = parseOne('input')
  assert.equal(frame.type, FrameType.Input)
  assert.deepEqual(Array.from(frame.payload), [0x1b, 0x5b, 0x5a])
})

test('resize round-trips', () => {
  const frame = parseOne('resize')
  assert.equal(frame.type, FrameType.Resize)
  assert.deepEqual(decodeResize(frame.payload), { cols: 120, rows: 34 })
  assert.deepEqual(encodeFrame(FrameType.Resize, encodeResize(120, 34)), fixture('resize'))
})

test('status decodes the advisory fields', () => {
  const frame = parseOne('status')
  assert.equal(frame.type, FrameType.Status)
  assert.deepEqual(decodeStatus(frame.payload), {
    state: 'ready',
    writer: 'phone',
    viewers: 2,
    cols: 120,
    rows: 34,
  })
})

test('exit decodes a negative code', () => {
  const frame = parseOne('exit')
  assert.equal(frame.type, FrameType.Exit)
  // Signed: a process killed by a signal reports -1, which reads as 4294967295
  // if the codec forgets the sign.
  assert.equal(decodeExit(frame.payload), -1)
})

test('ping and pong are empty frames', () => {
  for (const name of ['ping', 'pong'] as const) {
    const frame = parseOne(name)
    assert.equal(frame.type, name === 'ping' ? FrameType.Ping : FrameType.Pong)
    assert.equal(frame.payload.length, 0)
    assert.deepEqual(encodeFrame(frame.type), fixture(name))
  }
})

test('a chunk holding several frames yields all of them', () => {
  const frames = new FrameParser().push(fixture('stream'))
  assert.equal(frames.length, 3)
  assert.deepEqual(frames.map((f) => f.type), [FrameType.Output, FrameType.Ping, FrameType.Output])
  assert.equal(new TextDecoder().decode(frames[0]!.payload), 'first')
  assert.equal(new TextDecoder().decode(frames[2]!.payload), 'second')
})

test('a frame split across chunks is reassembled', () => {
  // The case that matters on the wire: neither transport preserves the
  // boundaries the sender wrote, so every split must parse the same.
  const whole = fixture('stream')
  for (let cut = 1; cut < whole.length; cut++) {
    const parser = new FrameParser()
    const frames = [
      ...parser.push(whole.subarray(0, cut)),
      ...parser.push(whole.subarray(cut)),
    ]
    assert.equal(frames.length, 3, `split at ${cut} lost a frame`)
    assert.equal(new TextDecoder().decode(frames[2]!.payload), 'second')
  }
})

test('byte-at-a-time delivery parses identically', () => {
  const whole = fixture('stream')
  const parser = new FrameParser()
  const frames = []
  for (const byte of whole) frames.push(...parser.push(new Uint8Array([byte])))
  assert.equal(frames.length, 3)
})

test('a 4 MiB output frame matches the digest Go recorded', () => {
  const payload = new Uint8Array(4 << 20)
  for (let i = 0; i < payload.length; i++) payload[i] = i % 251
  const digest = createHash('sha256').update(encodeFrame(FrameType.Output, payload)).digest('hex')
  const want = readFileSync(resolve(fixtures, 'large-output.sha256'), 'utf8').trim()
  assert.equal(digest, want)
})

test('a 4 MiB frame parses back out of a chunked stream', () => {
  const payload = new Uint8Array(4 << 20)
  for (let i = 0; i < payload.length; i++) payload[i] = i % 251
  const encoded = encodeFrame(FrameType.Output, payload)

  const parser = new FrameParser()
  const frames = []
  // 64 KiB reads, which is what a socket actually hands over.
  for (let i = 0; i < encoded.length; i += 65536) {
    frames.push(...parser.push(encoded.subarray(i, i + 65536)))
  }
  assert.equal(frames.length, 1)
  assert.deepEqual(frames[0]!.payload, payload)
})

test('an oversized length prefix is rejected without allocating', () => {
  const header = new Uint8Array(5)
  new DataView(header.buffer).setUint32(0, MAX_FRAME_SIZE + 1, false)
  header[4] = FrameType.Output
  assert.throws(() => new FrameParser().push(header), /exceeds/)
})

test('a zero-length frame is rejected', () => {
  const header = new Uint8Array(5)
  assert.throws(() => new FrameParser().push(header), /zero-length/)
})

test('encoding a payload past the maximum throws', () => {
  assert.throws(() => encodeFrame(FrameType.Output, new Uint8Array(MAX_FRAME_SIZE)), /exceeds/)
})
