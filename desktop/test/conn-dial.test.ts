// The daemon can hand out the socket of a host that has died — it forgets one
// only when its reaper next runs — and a tab that treats that as a slow peer
// retries it every eight seconds for as long as the window is open.
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { TerminalConn } from '../src/main/conn.ts'

test('a unix dial to a socket that is not there gives up', async () => {
  const conn = new TerminalConn({
    endpoint: { kind: 'unix', path: join(tmpdir(), 'helios-no-such-host.sock') },
    cols: 80,
    rows: 24,
  })

  const states: string[] = []
  conn.on('state', (state: string) => states.push(state))

  const reason = await new Promise<string>((resolve) => {
    conn.on('close', resolve)
    void conn.start()
  })

  assert.equal(reason, 'session has no live terminal')
  assert.ok(!states.includes('reconnecting'), `retried instead of giving up: ${states.join(', ')}`)
})
