// The files panel against a daemon from before spec 65: no raw read, no match
// positions or ranges, no `ignored`, no `touched`. Mobile and desktop releases
// lag the daemon, and the daemon lags them too.
import type { Locator, Page } from '@playwright/test'

import { REPO } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

test.use({ fastFiles: false })

function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

test('files open, search and change without the new routes', async ({ window, daemon }) => {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: 'files' }).first().click()

  // Nothing is dimmed: an old daemon cannot say what git ignores.
  await expect(shown(window).locator('.tree-row', { hasText: 'build' })).not.toHaveClass(/ignored/)

  await shown(window).locator('.tree-row', { hasText: 'main.go' }).click()
  const editor = shown(window).locator('.cm-content')
  await expect(editor).toContainText('package main')

  daemon.setFile(`${REPO}/main.go`, 'package main // later\n')
  daemon.emit('file_changed', { paths: [{ path: `${REPO}/main.go`, kind: 'file' }] })
  await expect(editor).toContainText('// later')

  await window.keyboard.press('Control+p')
  await window.locator('.quick-input').fill('readme')
  await expect(window.locator('.quick-row').first()).toContainText('README.md')
  await expect(window.locator('.quick-row b.match')).toHaveCount(0)
  await window.keyboard.press('Escape')

  await shown(window).locator('.ws-view', { hasText: 'Search' }).click()
  await shown(window).locator('.find-input').first().fill('needle')
  await expect(shown(window).locator('.find-hit')).toHaveCount(2)

  await shown(window).locator('.ws-view', { hasText: 'Changes' }).click()
  await expect(shown(window).locator('.changes-section').first()).toContainText('has not written a file yet')
})
