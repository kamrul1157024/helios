// Acting on several sessions at once.
//
// The risk here is not that a button does nothing — it is that it does the
// right thing to the wrong rows, or half of them. So these assert what reached
// the daemon, not what the bar said.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

function bar(window: Page) {
  return window.locator('.bulk-bar')
}

function ticks(window: Page) {
  return window.locator('.session-row .row-tick')
}

async function startPicking(window: Page): Promise<void> {
  await expect(window.locator('.session-row').first()).toBeVisible()
  await window.locator('.select-toggle').click()
  await expect(bar(window)).toBeVisible()
}

test('Select shows the ticks, and Done takes them away', async ({ window }) => {
  await expect(ticks(window)).toHaveCount(0)

  await startPicking(window)
  expect(await ticks(window).count()).toBeGreaterThan(0)

  await bar(window).getByText('Done').click()
  await expect(ticks(window)).toHaveCount(0)
  await expect(bar(window)).toHaveCount(0)
})

test('the bar counts what is held', async ({ window }) => {
  await startPicking(window)

  await ticks(window).nth(0).check()
  await expect(bar(window)).toContainText('1 selected')

  await ticks(window).nth(1).check()
  await expect(bar(window)).toContainText('2 selected')
})

test('⌘-click picks a row without opening it', async ({ window }) => {
  await expect(window.locator('.session-row').first()).toBeVisible()

  await window.locator('.session-row').first().click({ modifiers: ['ControlOrMeta'] })

  await expect(bar(window)).toContainText('1 selected')
  // Picking is not opening: the panel stays where it was.
  await expect(window.locator('.panel-tabs')).toHaveCount(0)
})

test('deleting the selection asks once, then deletes every one of them', async ({ window, daemon }) => {
  await startPicking(window)
  await ticks(window).nth(0).check()
  await ticks(window).nth(1).check()

  window.on('dialog', (dialog) => void dialog.accept())
  await bar(window).getByText('Delete').click()

  await expect
    .poll(() => daemon.writes().filter((write) => write.kind === 'delete').length)
    .toBe(2)
  // The selection is spent, so a second click cannot delete the same rows.
  await expect(bar(window)).not.toContainText('2 selected')
})

test('a refused confirmation deletes nothing', async ({ window, daemon }) => {
  await startPicking(window)
  await ticks(window).nth(0).check()

  window.on('dialog', (dialog) => void dialog.dismiss())
  await bar(window).getByText('Delete').click()

  await expect(bar(window)).toContainText('1 selected')
  expect(daemon.writes().filter((write) => write.kind === 'delete')).toHaveLength(0)
})

test('Pin pins everything held, in one call each', async ({ window, daemon }) => {
  await startPicking(window)
  await ticks(window).nth(0).check()
  await ticks(window).nth(1).check()

  await bar(window).getByText('Pin', { exact: true }).click()

  await expect.poll(() => daemon.writes().filter((write) => write.kind === 'patch').length).toBe(2)
  for (const write of daemon.writes()) {
    if (write.kind === 'patch') expect(write.patch).toEqual({ pinned: true })
  }
})
