// The session row in the sidebar, and the one control that ends the app.
//
// The row grew a line for the directory it runs in — mobile has always carried
// it — and the parity that matters is the order: title, then path, then status,
// the same three lines the phone shows. These rules live in a component, and
// there is no component test framework here, so they are checked from the
// running app.
import type { Page } from '@playwright/test'

import { REPO } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

function row(window: Page) {
  return window.locator('.session-row', { hasText: ALPHA })
}

test.beforeEach(async ({ window }) => {
  await expect(row(window)).toBeVisible()
})

test('a session row shows the directory it runs in, full path in the title', async ({ window }) => {
  const path = row(window).locator('.row-cwd')
  await expect(path).toBeVisible()
  await expect(path).toHaveText(REPO)
  // The visible text is shortened elsewhere; the title is always the whole path,
  // which is what a hover is for.
  await expect(path).toHaveAttribute('title', REPO)
})

test('the row reads title, then path, then status — the mobile order', async ({ window }) => {
  const alpha = row(window)
  const title = await alpha.locator('.row-title').boundingBox()
  const path = await alpha.locator('.row-cwd').boundingBox()
  const status = await alpha.locator('.row-status').boundingBox()

  // By vertical position rather than DOM order, because the order the user reads
  // is the one worth pinning: a later flex tweak could reorder them on screen
  // without touching the markup.
  expect(title && path && status).toBeTruthy()
  expect(title!.y).toBeLessThan(path!.y)
  expect(path!.y).toBeLessThan(status!.y)
})

test('Quit Helios asks before it quits, and Cancel backs out', async ({ window }) => {
  // The footer's button, not the modal's — the modal has one of the same name,
  // and it does not exist yet.
  await window.locator('.sidebar-foot').getByRole('button', { name: 'Quit Helios' }).click()

  const modal = window.locator('.modal')
  await expect(modal).toBeVisible()
  await expect(modal.locator('.modal-head h2')).toHaveText('Quit Helios?')

  // Cancel, not confirm: confirming would call app.quit() and end the run.
  await modal.getByRole('button', { name: 'Cancel' }).click()
  await expect(modal).toHaveCount(0)

  // The window is still here — nothing quit behind the dialog.
  await expect(row(window)).toBeVisible()
})
