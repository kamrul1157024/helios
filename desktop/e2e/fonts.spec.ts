// The three font pickers, and the variables they write.
//
// A picker that stores a preference nobody reads looks identical to one that
// works until you restart, so these assert the document itself: what <html>
// carries is what the app draws with.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

async function openAppearance(window: Page): Promise<void> {
  await window.locator('.rail-item[aria-label="Settings"]').click()
  await window.locator('.settings-nav button', { hasText: 'Appearance' }).click()
}

function picker(window: Page, label: string) {
  return window.locator('.setting-row', { hasText: label }).locator('select')
}

/** What applyFonts() wrote, read back off the root element. */
function cssVar(window: Page, name: string): Promise<string> {
  return window.evaluate((prop) => document.documentElement.style.getPropertyValue(prop), name)
}

test('the app boots on the bundled code font', async ({ window }) => {
  await expect(window.locator('.session-row').first()).toBeVisible()

  expect(await cssVar(window, '--mono')).toContain('Fira Code')
  expect(await cssVar(window, '--font-term')).toContain('Fira Code')
  // The shipped face, not a hope that the machine has it.
  expect(await window.evaluate(() => document.fonts.check('12px "Fira Code"'))).toBe(true)
})

test('picking a code font moves the variable the diffs and code blocks read', async ({ window }) => {
  await openAppearance(window)

  await picker(window, 'Code font').selectOption('jetbrains-mono')

  await expect
    .poll(() => cssVar(window, '--mono'))
    .toContain('JetBrains Mono')
  // One slot at a time: the terminal keeps what it was set to.
  expect(await cssVar(window, '--font-term')).toContain('Fira Code')
})

test('the interface font is a slot of its own', async ({ window }) => {
  await openAppearance(window)

  await picker(window, 'Interface font').selectOption('fira-code-ui')

  await expect
    .poll(() => cssVar(window, '--font-ui'))
    .toContain('Fira Code')
  expect(await cssVar(window, '--mono')).toContain('Fira Code')
})

test('code size moves the variable the code blocks and diffs are set in', async ({ window }) => {
  await openAppearance(window)

  const box = window.locator('.setting-row', { hasText: 'Code size' }).locator('input')
  await box.fill('17')
  await box.blur()

  await expect.poll(() => cssVar(window, '--code-size')).toBe('17px')
})

test('interface size scales the window rather than one font', async ({ window, app }) => {
  await openAppearance(window)

  const box = window.locator('.setting-row', { hasText: 'Interface size' }).locator('input')
  await box.fill('120')
  await box.blur()

  // The window's zoom, read from the main process: nothing in the page's own
  // styles is what carries this.
  await expect
    .poll(() =>
      app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0]?.webContents.getZoomFactor()),
    )
    .toBeCloseTo(1.2, 2)
})

test('a font the machine does not have is still offered, and says so', async ({ window }) => {
  await openAppearance(window)

  const options = await picker(window, 'Code font').locator('option').allTextContents()
  expect(options.some((text) => text.startsWith('Fira Code'))).toBe(true)
  // Bundled, so it can never be reported as missing.
  expect(options).not.toContain('Fira Code — not installed')
})
