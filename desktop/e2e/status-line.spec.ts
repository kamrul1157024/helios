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

  // The strip above it is the other half of the saving, and the buttons a
  // terminal tab brings with it used to be what set its height.
  //
  // Level with the sidebar's switch, not merely short: the two sit side by side
  // at the same y and read as one row, and they drifted apart the first time
  // this strip was trimmed.
  const tabs = await window.locator('.panel-tabs').boundingBox()
  const modes = await window.locator('.sidebar-modes').boundingBox()
  expect(tabs?.height).toBe(28)
  expect(modes?.height).toBe(tabs?.height)
  expect(modes?.y).toBe(tabs?.y)
})

test('the bar grows with the text size rather than clipping it', async ({ window }) => {
  await open(window, ALPHA)
  expect((await bar(window).boundingBox())?.height).toBe(18)

  await openSettings(window)
  const field = window.locator('.setting-row', { hasText: 'Status line size' }).locator('input')
  await field.fill('16')
  await field.blur()
  await window.keyboard.press('Escape')

  // Height is derived from the size, not set beside it: 16 + 7.
  await expect
    .poll(async () => (await bar(window).boundingBox())?.height)
    .toBe(23)
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

// Two ways into one list. They are built from the same function, and this is
// what would catch a second list being grown beside it.
test('the tab strip menu offers exactly what the row does', async ({ window }) => {
  await open(window, ALPHA)

  await window.locator('.session-row', { hasText: ALPHA }).click({ button: 'right' })
  const fromRow = await window.locator('.line-menu').first().locator('> button, > .line-sub > button').allInnerTexts()
  await window.keyboard.press('Escape')

  await window.locator('.tab-menu').click()
  const fromTabs = await window.locator('.line-menu').first().locator('> button, > .line-sub > button').allInnerTexts()

  expect(fromTabs).toEqual(fromRow)
  expect(fromRow).toContain('Delete')
})

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
