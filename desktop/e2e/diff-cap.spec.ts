// A long patch inline in a conversation.
//
// The transcript shows enough to see what changed and offers the rest: a
// four-hundred-line rewrite is one message among many, and scrolling past it
// to reach the next sentence is most of what reading that transcript becomes.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, withToolCalls } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'
const LINES = 60

async function open(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.msg.tool-call').first()).toBeVisible()
}

function bigEdit(lines: number) {
  return {
    tool: 'Edit',
    summary: 'big.go',
    input: {
      file_path: '/repo/big.go',
      old_string: 'one line',
      new_string: Array.from({ length: lines }, (_, at) => `line ${at}`).join('\n'),
    },
  }
}

test('a long patch is cut short, and says how much it is holding', async ({ window }) => {
  withToolCalls(ALPHA_ID, [bigEdit(LINES)])
  await open(window)

  const rows = window.locator('.diff-line')
  expect(await rows.count()).toBeLessThanOrEqual(24)
  await expect(window.locator('.diff-more')).toContainText('more lines')
})

test('the rest is one click away, and can be put back', async ({ window }) => {
  withToolCalls(ALPHA_ID, [bigEdit(LINES)])
  await open(window)
  const before = await window.locator('.diff-line').count()

  await window.locator('.diff-more').click()
  expect(await window.locator('.diff-line').count()).toBeGreaterThan(before)
  await expect(window.locator('.diff-more')).toHaveText('Show less')

  await window.locator('.diff-more').click()
  expect(await window.locator('.diff-line').count()).toBe(before)
})

test('a short patch is drawn whole, with nothing to press', async ({ window }) => {
  withToolCalls(ALPHA_ID, [bigEdit(3)])
  await open(window)

  await expect(window.locator('.diff-more')).toHaveCount(0)
})
