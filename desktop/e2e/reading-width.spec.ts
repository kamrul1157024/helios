// How wide the agent's prose is allowed to run.
//
// The rule worth guarding is which half it applies to: prose is read, so it
// gets a measure; code and patches are scanned, so they get the panel.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, appendTranscript, withToolCalls } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'
const LONG = `A paragraph long enough to run the width of a wide window, which is the
whole point of the setting: ${'and on it goes, '.repeat(40)} to the end.`

async function open(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

function widthOf(window: Page, selector: string): Promise<number> {
  return window.locator(selector).first().evaluate((node) => node.getBoundingClientRect().width)
}

test('prose is held to a measure', async ({ window }) => {
  appendTranscript(ALPHA_ID, LONG)
  await open(window)

  expect(await window.evaluate(() => document.documentElement.style.getPropertyValue('--prose-width'))).toBe(
    '900px',
  )
  expect(await widthOf(window, '.msg.assistant .msg-body')).toBeLessThanOrEqual(900)
})

test('a patch is not: it is scanned, not read', async ({ window }) => {
  appendTranscript(ALPHA_ID, LONG)
  withToolCalls(ALPHA_ID, [
    {
      tool: 'Edit',
      summary: 'main.go',
      input: { file_path: '/repo/main.go', old_string: 'a', new_string: 'b' },
    },
  ])
  await open(window)

  const prose = await widthOf(window, '.msg.assistant .msg-body')
  const patch = await widthOf(window, '.tool-diff')
  expect(patch).toBeGreaterThan(prose)
})

test('the setting moves it, and zero takes the limit off', async ({ window }) => {
  appendTranscript(ALPHA_ID, LONG)
  await open(window)

  await window.locator('.rail-item[aria-label="Settings"]').click()
  await window.locator('.settings-nav button', { hasText: 'Appearance' }).click()
  const box = window.locator('.setting-row', { hasText: 'Reading width' }).locator('input')

  await box.fill('600')
  await box.blur()
  await window.locator('.rail-item[aria-label="Sessions"]').click()
  await expect.poll(() => widthOf(window, '.msg.assistant .msg-body')).toBeLessThanOrEqual(600)

  await window.locator('.rail-item[aria-label="Settings"]').click()
  await box.fill('0')
  await box.blur()
  await window.locator('.rail-item[aria-label="Sessions"]').click()
  await expect
    .poll(() => window.evaluate(() => document.documentElement.style.getPropertyValue('--prose-width')))
    .toBe('none')
  expect(await widthOf(window, '.msg.assistant .msg-body')).toBeGreaterThan(900)
})
