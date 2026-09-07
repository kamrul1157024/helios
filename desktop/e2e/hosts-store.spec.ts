// What a write to hosts.json is allowed to do to the entries it did not write.
//
// Seeds were once put through the OS key store. The Keychain binds an item to
// the code signature that wrote it and these builds are ad-hoc signed, so an
// upgrade left the app unable to read its own seeds — and load() dropped what
// it could not read, which the next persist() then erased from the file. The
// hosts looked like they had vanished on install. Nothing is encrypted any
// more, and an entry that still is has to survive being written around.
import crypto from 'node:crypto'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test, type ElectronApplication } from '@playwright/test'

import { startDaemon, type StubDaemon } from './daemon.ts'

const READABLE = 'readable-host'
const LEGACY = 'legacy-host'

interface Stored {
  id: string
  name: string
  secret: string
  encrypted?: boolean
}

test('a seed left over from the encrypting builds is not deleted by the next write', async () => {
  test.setTimeout(120000)
  let daemon: StubDaemon | null = null
  let app: ElectronApplication | null = null
  const userData = await fs.mkdtemp(path.join(os.tmpdir(), 'helios-hosts-'))

  try {
    daemon = await startDaemon()
    const file = path.join(userData, 'hosts.json')
    await fs.writeFile(
      file,
      JSON.stringify([
        {
          id: READABLE,
          name: 'readable',
          url: daemon.url,
          device_id: READABLE,
          local: false,
          secret: crypto.randomBytes(32).toString('base64url'),
        },
        // Ciphertext this build has no key for, exactly as an upgraded machine
        // finds it.
        {
          id: LEGACY,
          name: 'legacy',
          url: 'http://127.0.0.1:7655',
          device_id: LEGACY,
          local: true,
          secret: crypto.randomBytes(64).toString('base64'),
          encrypted: true,
        },
      ]),
    )

    app = await electron.launch({
      args: ['.', `--user-data-dir=${userData}`],
      env: { ...process.env, HELIOS_RELEASES_URL: 'data:application/json,[]' },
    })
    const window = await app.firstWindow()
    await window.waitForLoadState('domcontentloaded')

    // A rename is the cheapest thing that makes the app rewrite the file.
    await window.locator('.sidebar-foot').getByRole('button', { name: 'Add host' }).click()
    const name = window.locator('.host-name-input').first()
    await expect(name).toBeVisible()
    await name.fill('renamed')
    await name.blur()

    await expect
      .poll(async () => {
        const stored = JSON.parse(await fs.readFile(file, 'utf8')) as Stored[]
        return stored.find((h) => h.id === READABLE)?.name
      })
      .toBe('renamed')

    const stored = JSON.parse(await fs.readFile(file, 'utf8')) as Stored[]
    const legacy = stored.find((h) => h.id === LEGACY)
    expect(legacy).toBeTruthy()
    expect(legacy!.encrypted).toBe(true)

    // The one it can read is written back in the clear, with no flag claiming
    // a key store holds it.
    const readable = stored.find((h) => h.id === READABLE)
    expect(readable!.encrypted).toBeUndefined()
  } finally {
    await app?.close()
    await daemon?.close()
    await fs.rm(userData, { recursive: true, force: true })
  }
})
