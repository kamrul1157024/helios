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

test('the measure starts at 70% of the panel', async ({ window }) => {
  await open(window)

  expect(await window.evaluate(() => document.documentElement.style.getPropertyValue('--prose-width'))).toBe(
    '70%',
  )
})

test('prose is held to the measure, and stops short of the panel', async ({ window }) => {
  appendTranscript(ALPHA_ID, LONG)
  await open(window)
  await setWidth(window, '40')

  // Two fifths of the panel, whatever the panel happens to be here.
  await expect
    .poll(async () =>
      Math.round(
        (await widthOf(window, '.msg.assistant .msg-body')) / (await widthOf(window, '.chat-scroll')) * 100,
      ),
    )
    .toBeLessThanOrEqual(42)
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

  await setWidth(window, '40')

  // The patch keeps the panel: it is scanned, not read.
  await expect
    .poll(async () =>
      (await widthOf(window, '.tool-diff')) > (await widthOf(window, '.msg.assistant .msg-body')),
    )
    .toBe(true)
})

test('the setting moves it, and a hundred takes the limit off', async ({ window }) => {
  appendTranscript(ALPHA_ID, LONG)
  await open(window)

  await setWidth(window, '40')
  const narrow = await widthOf(window, '.msg.assistant .msg-body')

  await setWidth(window, '100')
  await expect
    .poll(() => window.evaluate(() => document.documentElement.style.getPropertyValue('--prose-width')))
    .toBe('none')

  // A hundred percent means the panel, whatever that is on this machine.
  const prose = await widthOf(window, '.msg.assistant .msg-body')
  const panel = await widthOf(window, '.chat-scroll')
  expect(prose).toBeGreaterThan(narrow)
  expect(panel - prose).toBeLessThan(48)
})
