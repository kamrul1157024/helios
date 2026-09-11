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

/**
 * Sets the width and comes back to the transcript.
 *
 * The measurements below are all relative to the panel: the window CI runs in
 * is narrower than the default measure, so asserting "prose is under 1100px"
 * there passes whatever the setting does — and asserting it exceeds 1100 with
 * the cap off cannot pass at all.
 */
async function setWidth(window: Page, px: string): Promise<void> {
  await window.locator('.rail-item[aria-label="Settings"]').click()
  await window.locator('.settings-nav button', { hasText: 'Appearance' }).click()
  const box = window.locator('.setting-row', { hasText: 'Reading width' }).locator('input')
  await box.fill(px)
  await box.blur()
  await window.locator('.rail-item[aria-label="Sessions"]').click()
}

test('the measure starts at 1100px', async ({ window }) => {
  await open(window)

  expect(await window.evaluate(() => document.documentElement.style.getPropertyValue('--prose-width'))).toBe(
    '1100px',
  )
})

test('prose is held to the measure, and stops short of the panel', async ({ window }) => {
  appendTranscript(ALPHA_ID, LONG)
  await open(window)
  await setWidth(window, '400')

  await expect.poll(() => widthOf(window, '.msg.assistant .msg-body')).toBeLessThanOrEqual(400)
  expect(await widthOf(window, '.msg.assistant .msg-body')).toBeLessThan(
    await widthOf(window, '.chat-scroll'),
  )
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

  await setWidth(window, '400')

  await expect.poll(() => widthOf(window, '.msg.assistant .msg-body')).toBeLessThanOrEqual(400)
  // The patch keeps the panel: it is scanned, not read.
  expect(await widthOf(window, '.tool-diff')).toBeGreaterThan(
    await widthOf(window, '.msg.assistant .msg-body'),
  )
})

test('the setting moves it, and zero takes the limit off', async ({ window }) => {
  appendTranscript(ALPHA_ID, LONG)
  await open(window)

  await setWidth(window, '400')
  await expect.poll(() => widthOf(window, '.msg.assistant .msg-body')).toBeLessThanOrEqual(400)

  await setWidth(window, '0')
  await expect
    .poll(() => window.evaluate(() => document.documentElement.style.getPropertyValue('--prose-width')))
    .toBe('none')
  // No limit means the panel's width, whatever that is on this machine.
  const prose = await widthOf(window, '.msg.assistant .msg-body')
  const panel = await widthOf(window, '.chat-scroll')
  expect(prose).toBeGreaterThan(400)
  expect(panel - prose).toBeLessThan(48)
})
