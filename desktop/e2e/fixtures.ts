// Launching the packaged renderer against the stub daemon, once per test.
//
// The app is given a throwaway user data directory with a host already in it.
// Pairing is not what these tests are about, and the registry reads a host from
// hosts.json exactly as it would after a real pairing — a plaintext seed is the
// same shape it stores on a machine with no keyring.
import crypto from 'node:crypto'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, test as base, type ElectronApplication, type Page } from '@playwright/test'

import { startDaemon, type StubDaemon } from './daemon.ts'

export const HOST_ID = 'e2e-host'

interface Fixtures {
  daemon: StubDaemon
  app: ElectronApplication
  window: Page
}

export const test = base.extend<Fixtures>({
  daemon: async ({}, use) => {
    const daemon = await startDaemon()
    await use(daemon)
    await daemon.close()
  },

  app: async ({ daemon }, use) => {
    const userData = await fs.mkdtemp(path.join(os.tmpdir(), 'helios-e2e-'))
    await fs.writeFile(
      path.join(userData, 'hosts.json'),
      JSON.stringify([
        {
          id: HOST_ID,
          name: 'stub',
          url: daemon.url,
          device_id: HOST_ID,
          local: false,
          secret: crypto.randomBytes(32).toString('base64url'),
          encrypted: false,
        },
      ]),
    )

    // '.' is the desktop package: the suite is run by `npm run e2e`, which puts
    // the working directory there and has already built dist/.
    const app = await electron.launch({ args: ['.', `--user-data-dir=${userData}`] })
    await use(app)
    await app.close()
    await fs.rm(userData, { recursive: true, force: true })
  },

  window: async ({ app }, use) => {
    const window = await app.firstWindow()
    await window.waitForLoadState('domcontentloaded')
    await use(window)
  },
})

export { expect } from '@playwright/test'
