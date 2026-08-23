// A viewer must not answer device queries itself; the host is the terminal.
// See internal/terminal — the host's emulator is the single authoritative
// answerer, and a viewer's late/duplicate reply is what leaked `^[[…R` onto
// the prompt.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { IFunctionIdentifier } from '@xterm/xterm'

import { DEVICE_REPORT_QUERIES, silenceDeviceReports } from '../src/renderer/components/deviceReports.ts'

type Registered = { id: IFunctionIdentifier; cb: (params: (number | number[])[]) => boolean | Promise<boolean> }

function fakeParser(): { parser: { registerCsiHandler: (id: IFunctionIdentifier, cb: Registered['cb']) => { dispose(): void } }; registered: Registered[] } {
  const registered: Registered[] = []
  return {
    registered,
    parser: {
      registerCsiHandler: (id, cb) => {
        registered.push({ id, cb })
        return { dispose() {} }
      },
    },
  }
}

test('swallows every device-report query so the built-in reply never fires', () => {
  const { parser, registered } = fakeParser()
  silenceDeviceReports(parser as never)

  assert.equal(registered.length, DEVICE_REPORT_QUERIES.length)
  for (const r of registered) {
    // Returning true tells xterm the sequence was handled, suppressing its reply.
    assert.equal(r.cb([]), true)
  }
})

test('only touches report finals — never display or keyboard sequences', () => {
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
