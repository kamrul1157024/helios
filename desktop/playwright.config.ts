import { defineConfig } from '@playwright/test'

// One worker: each test launches its own Electron app, and several at once on
// a CI runner is how an end-to-end suite becomes a flaky one.
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['github']] : [['list']],
  // A failure here is a failure of a window nobody was watching, so it has to
  // leave enough behind to be read afterwards.
  use: { trace: 'retain-on-failure', screenshot: 'only-on-failure' },
})
