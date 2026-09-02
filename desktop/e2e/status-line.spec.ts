// The bar at the foot of a session, and the menu that replaced the header.
//
// A 67px header used to carry the session's directory, status and permission
// mode. It is gone; this checks that what it carried is still reachable — from
// an 18px bar, and from the row's own context menu. Those rules live in
// components, and there is no component test framework here.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

function bar(window: Page) {
  return window.locator('.status-line')
}

async function open(window: Page, title: string): Promise<void> {
  await window.locator('.session-row', { hasText: title }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

test.beforeEach(async ({ window }) => {
  await expect(window.locator('.session-row', { hasText: ALPHA })).toBeVisible()
})

test('the header is gone and the status line carries what it held', async ({ window }) => {
  await open(window, ALPHA)

  await expect(window.locator('.detail-head')).toHaveCount(0)

  // The five defaults: directory, branch, model, mode, status. The branch is
  // the one that was not on the header at all — reading it meant opening the
  // Git panel.
  await expect(bar(window)).toContainText('/repo')
  await expect(bar(window)).toContainText('main')
  await expect(bar(window)).toContainText('opus')
  await expect(bar(window)).toContainText('default')
  await expect(bar(window)).toContainText('Idle')
})

// The whole point of the change. Measured rather than eyeballed, because a
// later padding tweak would put the height back without anyone noticing.
test('the bar is no taller than the text it holds', async ({ window }) => {
  await open(window, ALPHA)
  const box = await bar(window).boundingBox()
  expect(box?.height).toBe(18)
})

test('the permission mode is on the row menu, ticked on the one in force', async ({ window }) => {
  await window.locator('.session-row', { hasText: ALPHA }).click({ button: 'right' })

  const menu = window.locator('.line-menu').first()
  await expect(menu).toBeVisible()
  await expect(menu.getByText('Rename…')).toBeVisible()

  // Hovering the parent row opens the child beside it. The parent is one row
  // tall whether or not the provider list has arrived, which is why the modes
  // live in a child rather than being appended to the menu.
  await menu.getByText('Permission mode').hover()
  const submenu = window.locator('.line-sub-menu')
  await expect(submenu).toBeVisible()
  await expect(submenu.getByText('✓ default')).toBeVisible()
  await expect(submenu.getByText('bypassPermissions')).toBeVisible()
})

async function openSettings(window: Page): Promise<void> {
  await window.locator('.sidebar-foot .menu summary').click()
  await window.getByRole('button', { name: 'Settings' }).click()
  await expect(window.locator('.seg-list')).toBeVisible()
}

function segment(window: Page, label: string) {
  return window.locator('.seg-row', { has: window.getByText(label, { exact: true }) })
}

test('a segment dragged up the list moves left along the bar', async ({ window }) => {
  await open(window, ALPHA)
  // Directory first, status last: the default order, and what the drag changes.
  await expect(bar(window).locator('> *').first()).toContainText('/repo')

  await openSettings(window)
  await segment(window, 'Status').dragTo(segment(window, 'Working directory'))

  await expect(window.locator('.seg-row:not(.off) .seg-label').first()).toHaveText('Status')

  await window.keyboard.press('Escape')
  await expect(bar(window).locator('> *').first()).toContainText('Idle')
})

test('turning every segment off hides the bar', async ({ window }) => {
  await open(window, ALPHA)
  await expect(bar(window)).toBeVisible()

  await openSettings(window)
  await expect(window.locator('.seg-list .seg-row:not(.off)')).toHaveCount(5)

  // By name rather than by position. Unticking one drops it below the divider
  // and the next takes its place, so a locator that keeps asking for the first
  // row is asking about a different segment each time.
  for (const label of ['Working directory', 'Git branch', 'Model', 'Permission mode', 'Status']) {
    await segment(window, label).locator('input').click()
  }

  await expect(window.locator('.seg-list .seg-row:not(.off)')).toHaveCount(0)
  await window.keyboard.press('Escape')

  // The preference went to the main process and came back over theme:changed —
  // no reload anywhere, which is the part worth asserting.
  await expect(bar(window)).toHaveCount(0)
})
