// A viewer must not answer device queries itself; the host is the terminal.
// See internal/terminal — the host's emulator is the single authoritative
// answerer, and a viewer's late/duplicate reply is what leaked `^[[…R` (CSI
// reports) and `^[]11;rgb:…` (OSC colour reports) onto the prompt.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { IFunctionIdentifier } from '@xterm/xterm'

import {
  DEVICE_REPORT_QUERIES,
  OSC_COLOUR_QUERIES,
  isColourQuery,
  silenceDeviceReports,
} from '../src/renderer/components/deviceReports.ts'

type CsiReg = { id: IFunctionIdentifier; cb: (params: (number | number[])[]) => boolean | Promise<boolean> }
type OscReg = { id: number; cb: (data: string) => boolean | Promise<boolean> }

function fakeParser(): {
  parser: {
    registerCsiHandler: (id: IFunctionIdentifier, cb: CsiReg['cb']) => { dispose(): void }
    registerOscHandler: (id: number, cb: OscReg['cb']) => { dispose(): void }
  }
  csi: CsiReg[]
  osc: OscReg[]
} {
  const csi: CsiReg[] = []
  const osc: OscReg[] = []
  return {
    csi,
    osc,
    parser: {
      registerCsiHandler: (id, cb) => {
        csi.push({ id, cb })
        return { dispose() {} }
      },
      registerOscHandler: (id, cb) => {
        osc.push({ id, cb })
        return { dispose() {} }
      },
    },
  }
}

test('swallows every CSI device-report query so the built-in reply never fires', () => {
  const { parser, csi } = fakeParser()
  silenceDeviceReports(parser as never)

  assert.equal(csi.length, DEVICE_REPORT_QUERIES.length)
  for (const r of csi) {
    // Returning true tells xterm the sequence was handled, suppressing its reply.
    assert.equal(r.cb([]), true)
  }
})

test('only touches CSI report finals — never display or keyboard sequences', () => {
  const plainFinals = DEVICE_REPORT_QUERIES.filter((q) => !q.prefix).map((q) => q.final)

  // `R` is the CPR reply's final: leaving it alone means a Shift+F3 keystroke
  // (`^[[1;2R`) is never mistaken for a report. `q` is DECSCUSR (cursor style),
  // `m` is SGR, `A`–`D` are the arrow keys — all must keep working.
  for (const untouched of ['R', 'q', 'm', 'A', 'B', 'C', 'D', 'H', 'F', '~']) {
    assert.ok(!plainFinals.includes(untouched), `must not blanket-handle final ${untouched}`)
  }

  // Report finals are exactly DSR (n) and DA (c).
  assert.deepEqual([...new Set(DEVICE_REPORT_QUERIES.map((q) => q.final))].sort(), ['c', 'n'])
})

test('suppresses OSC colour queries but lets colour sets through', () => {
  const { parser, osc } = fakeParser()
  silenceDeviceReports(parser as never)

  assert.deepEqual(
    osc.map((r) => r.id).sort((a, b) => a - b),
    [...OSC_COLOUR_QUERIES].sort((a, b) => a - b),
  )

  for (const r of osc) {
    // A `?` query is handled here (true) so no reply is emitted…
    assert.equal(r.cb('?'), true)
    // …but a colour set falls through (false) so the built-in still applies it.
    assert.equal(r.cb('#1e1e1e'), false)
    assert.equal(r.cb('rgb:12/34/56'), false)
  }
})

test('isColourQuery detects a `?` in any parameter position', () => {
  assert.equal(isColourQuery('?'), true) // OSC 11;?
  assert.equal(isColourQuery('1;?'), true) // OSC 4;1;?
  assert.equal(isColourQuery('#1e1e1e'), false)
  assert.equal(isColourQuery('rgb:12/34/56'), false)
  assert.equal(isColourQuery('1;rgb:12/34/56'), false)
})
