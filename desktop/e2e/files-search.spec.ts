// Find in files and quick open (spec 65, F4, F5, B3 and B4).
import type { Locator, Page } from '@playwright/test'

import { REPO } from './daemon.ts'
import { expect, test } from './fixtures.ts'
import { snap } from './screenshots.ts'

const ALPHA = 'Alpha'

function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

async function openPanel(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: 'files' }).first().click()
  await expect(shown(window).locator('.tree-row', { hasText: 'main.go' })).toBeVisible()
}

async function openSearch(window: Page): Promise<Locator> {
  await openPanel(window)
  await shown(window).locator('.ws-view', { hasText: 'Search' }).click()
  const field = shown(window).locator('.find-input').first()
  await expect(field).toBeFocused()
  return field
}

test.beforeEach(async ({ window }) => {
  await expect(window.locator('.session-row', { hasText: ALPHA })).toBeVisible()
})

test('the search runs as you type, once you pause, and marks what matched', async ({ window, daemon }) => {
  const field = await openSearch(window)

  // One letter is not a search.
  await field.pressSequentially('n')
  await window.waitForTimeout(400)
  expect(daemon.greps()).toBe(0)

  await field.pressSequentially('eedle', { delay: 20 })
  await expect(shown(window).locator('.find-hit')).toHaveCount(2)
  // A pause, not a search per keystroke.
  expect(daemon.greps()).toBeLessThanOrEqual(2)
  await expect(shown(window).locator('.find-summary')).toContainText('2 results in 2 files')
  await expect(shown(window).locator('.find-hit b.match').first()).toHaveText('needle')
  await snap(window, 'search')
})

test('a toggle runs the search again with its flag', async ({ window, daemon }) => {
  const field = await openSearch(window)
  await field.fill('needle')
  await field.press('Enter')
  await expect(shown(window).locator('.find-hit')).toHaveCount(2)
  const before = daemon.grepQueries().length

  await shown(window).locator('.find-toggle[aria-label="Match case"]').click()
  await expect.poll(() => daemon.grepQueries().length).toBeGreaterThan(before)
  expect(daemon.grepQueries().at(-1)?.get('case')).toBe('true')

  await shown(window).locator('.find-toggle[aria-label="Match whole word"]').click()
  await expect.poll(() => daemon.grepQueries().at(-1)?.get('word')).toBe('1')

  // The include and exclude globs ride along as one parameter.
  await shown(window).locator('.find-toggle[aria-label="Files to include or exclude"]').click()
  const globs = shown(window).locator('.find-glob .find-input')
  await globs.fill('*.go, !*_test.go')
  await globs.press('Enter')
  await expect.poll(() => daemon.grepQueries().at(-1)?.get('glob')).toBe('*.go, !*_test.go')
})

test('results are walked with the arrow keys and opened with Enter', async ({ window }) => {
  const field = await openSearch(window)
  await field.fill('needle')
  await field.press('Enter')
  await expect(shown(window).locator('.find-hit')).toHaveCount(2)

  await field.press('ArrowDown')
  await field.press('ArrowDown')
  await expect(shown(window).locator('.find-hit.active')).toContainText('README.md')
  await field.press('ArrowUp')
  await expect(shown(window).locator('.find-hit.active')).toContainText('main.go')
  await field.press('Enter')
  await expect(shown(window).locator('.ws-tab.active')).toContainText('main.go')
  // The line it opened at is tinted for a moment.
  await expect(shown(window).locator('.cm-flash')).toHaveCount(1)
})

test('a group folds away, and the search is still there after the explorer', async ({ window }) => {
  const field = await openSearch(window)
  await field.fill('needle')
  await field.press('Enter')
  await expect(shown(window).locator('.find-hit')).toHaveCount(2)
  await shown(window).locator('.find-file').first().click()
  await expect(shown(window).locator('.find-hit')).toHaveCount(1)

  // Over to the explorer and back: the query, the fold and the results stay.
  await shown(window).locator('.ws-view', { hasText: 'Explorer' }).click()
  await shown(window).locator('.ws-view', { hasText: 'Search' }).click()
  await expect(field).toHaveValue('needle')
  await expect(shown(window).locator('.find-file').first()).toHaveAttribute('aria-expanded', 'false')
  await expect(shown(window).locator('.find-hit')).toHaveCount(1)
})

test('quick open marks the matched letters and lands on file:line', async ({ window, daemon }) => {
  daemon.setFile(`${REPO}/desktop/src/app.tsx`, ['one', 'two', 'three', 'four', 'five'].join('\n'))
  await openPanel(window)
  await window.keyboard.press('Control+p')
  await window.locator('.quick-input').fill('apptsx:3:2')
  const first = window.locator('.quick-row').first()
  await expect(first).toContainText('app.tsx')
  await expect(first.locator('b.match').first()).toBeVisible()
  await snap(window, 'quick-open')
  await window.keyboard.press('Enter')

  await expect(shown(window).locator('.ws-tab.active')).toContainText('app.tsx')
  await expect(shown(window).locator('.cm-status')).toContainText('Ln 3, Col 2')
})

test('an empty quick open offers the open tabs, then what was opened lately', async ({ window }) => {
  await openPanel(window)
  for (const name of ['main.go', 'README.md']) await shown(window).locator('.tree-row', { hasText: name }).click()
  // Close one: it is no longer open, but it was opened lately.
  await shown(window).locator('.ws-tab', { hasText: 'main.go' }).locator('.ws-tab-close').click()
  await expect(shown(window).locator('.ws-tab')).toHaveCount(1)

  await window.keyboard.press('Control+p')
  const rows = window.locator('.quick-row')
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(0)).toContainText('README.md')
  await expect(rows.nth(0).locator('.quick-note')).toHaveText('open')
  await expect(rows.nth(1)).toContainText('main.go')
  await expect(rows.nth(1).locator('.quick-note')).toHaveText('recent')
  await expect(rows.nth(1)).toHaveAttribute('title', `${REPO}/main.go`)
  await snap(window, 'quick-open-empty')
})
