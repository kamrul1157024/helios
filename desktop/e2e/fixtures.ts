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

import { setChannelsUnsupported, startDaemon, type StubDaemon } from './daemon.ts'

export const HOST_ID = 'e2e-host'

interface Options {
  /**
   * Whether this daemon knows what a channel is.
   *
   * An option rather than something a test sets in its own body: the window is
   * built before the first hook runs, so a flag flipped inside a test arrives
   * after the app has already read the channel list and cached the answer.
   * Options are resolved before the fixtures that depend on them, which is the
   * ordering this needs. Use it with `test.use({ channelsSupported: false })`.
   */
  channelsSupported: boolean
}

interface Fixtures {
  daemon: StubDaemon
  app: ElectronApplication
  window: Page
}

export const test = base.extend<Options & Fixtures>({
  channelsSupported: [true, { option: true }],

  daemon: async ({ channelsSupported }, use) => {
    setChannelsUnsupported(!channelsSupported)
    const daemon = await startDaemon()
    await use(daemon)
    await daemon.close()
    setChannelsUnsupported(false)
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
    // An empty release list, so the update dialog never opens. Pointed at
    // GitHub it raises itself over the window on launch — the app under test is
    // several releases behind whatever is published — and every click in the
    // suite lands on its backdrop.
    const app = await electron.launch({
      args: ['.', `--user-data-dir=${userData}`],
      // HELIOS_E2E_HIDDEN is deliberately not set here. Passing it keeps the
      // window off the screen, which is pleasant locally, but a window that is
      // never shown does not repaint after a reload — so the two specs that
      // reload the window ("a draft outlives the window itself" and "the fold
      // outlives the panel, and the window") time out waiting for a click on
      // something Chromium never painted. Opt in per run instead:
      //   HELIOS_E2E_HIDDEN=1 npx playwright test e2e/<spec>.spec.ts
      env: { ...process.env, HELIOS_RELEASES_URL: 'data:application/json,[]' },
    })
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
