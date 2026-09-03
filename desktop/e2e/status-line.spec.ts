// The bar at the foot of a session, and the menu that replaced the header.
//
// A 67px header used to carry the session's directory, status and permission
// mode. It is gone; this checks that what it carried is still reachable — from
// an 18px bar, and from the row's own context menu. Those rules live in
// components, and there is no component test framework here.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID } from './daemon.ts'
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
  // Level with the sidebar's own head, not merely short: the two sit side by
  // side at the same y and read as one row, and they drifted apart the first
  // time this strip was trimmed. It was the sessions/schedules switch that sat
  // there until the rail took the switching over.
  const tabs = await window.locator('.panel-tabs').boundingBox()
  const head = await window.locator('.sidebar-head').boundingBox()
  expect(tabs?.height).toBe(34)
  expect(head?.y).toBe(tabs?.y)
})

test('the bar grows with the text size rather than clipping it', async ({ window }) => {
  await open(window, ALPHA)
  expect((await bar(window).boundingBox())?.height).toBe(18)

  await openSettings(window)
  // Scoped to the group: "Text size" is also the label of the markdown size in
  // the theme group above it.
  const field = window
    .locator('.settings-group', { hasText: 'Status line' })
    .locator('.setting-row', { hasText: 'Text size' })
    .locator('input')
  await field.fill('16')
  await field.blur()
  await closeSettings(window)

  // Height is derived from the size, not set beside it: 16 + 7.
  await expect
    .poll(async () => (await bar(window).boundingBox())?.height)
    .toBe(23)
})

test('the permission mode is on the row menu, ticked on the one in force', async ({ window }) => {
  await window.locator('.session-row', { hasText: ALPHA }).click({ button: 'right' })

  const menu = window.locator('.line-menu').first()
  await expect(menu).toBeVisible()
  // No ellipsis on it any more: the item opens a field on the row itself
  // rather than the prompt it used to, which Electron never supported.
  await expect(menu.getByText('Rename', { exact: true })).toBeVisible()

  // Hovering the parent row opens the child beside it. The parent is one row
  // tall whether or not the provider list has arrived, which is why the modes
  // live in a child rather than being appended to the menu.
  await menu.getByText('Permission mode').hover()
  const submenu = window.locator('.line-sub-menu')
  await expect(submenu).toBeVisible()
  await expect(submenu.getByText('✓ default')).toBeVisible()
  await expect(submenu.getByText('bypassPermissions')).toBeVisible()
})

/** Settings is a mode now, opened from the rail and left the same way. */
async function openSettings(window: Page): Promise<void> {
  await window.locator('.rail-item[aria-label="Settings"]').click()
  await window.locator('.settings-nav button', { hasText: 'Appearance' }).click()
  await expect(window.locator('.seg-list')).toBeVisible()
}

/** Back to the sessions, where the session under test is still selected. */
async function closeSettings(window: Page): Promise<void> {
  await window.locator('.rail-item[aria-label="Sessions"]').click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

function segment(window: Page, label: string) {
  return window.locator('.seg-row', { has: window.getByText(label, { exact: true }) })
}

// The modes grey out mid-turn, and a disabled button fires no pointer events —
// so a tooltip on them is unreadable by construction. The way out has to be a
// row of its own.
test('a busy session says what to do before the modes will take', async ({ window, daemon }) => {
  await open(window, ALPHA)

  // Both halves: the event is what the client hears, and the list behind it has
  // to agree or the refetch puts "idle" straight back.
  daemon.setStatus(ALPHA_ID, 'active')
  daemon.emit('session_status', { session_id: ALPHA_ID, status: 'active' })
  await expect(bar(window)).toContainText('Active')

  await window.locator('.tab-menu').click()
  await window.locator('.line-menu').first().getByText('Permission mode').hover()

  const submenu = window.locator('.line-sub-menu')
  await expect(submenu.getByText('Stop the agent to change this')).toBeVisible()
  await expect(submenu.getByRole('button', { name: 'bypassPermissions' })).toBeDisabled()
})

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

  await closeSettings(window)
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
  await closeSettings(window)

  // The preference went to the main process and came back over theme:changed —
  // no reload anywhere, which is the part worth asserting.
  await expect(bar(window)).toHaveCount(0)
})
