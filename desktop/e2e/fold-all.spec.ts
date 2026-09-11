// Folding every tool call in the transcript at once.
//
// A card opened by hand after a Fold all has to be reachable by the next
// press, which is the case a boolean would quietly get wrong — so that is what
// most of this checks.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, addToolCall, pushEvent, setSessionStatus, withToolCalls } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

test.beforeEach(() => {
  // Working, so the calls at the end of the transcript are not grouped into a
  // turn: this suite is about the cards, and a live session is where they are.
  setSessionStatus(ALPHA_ID, 'active')
  withToolCalls(ALPHA_ID, [
    { tool: 'Bash', summary: 'ls -la', input: { command: 'ls -la', description: 'List the directory' } },
    { tool: 'Edit', summary: 'main.go', input: { file_path: '/repo/main.go', old_string: 'a', new_string: 'b' } },
  ])
})

async function open(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
  await expect(window.locator('.msg.tool-call').first()).toBeVisible()
}

function fold(window: Page) {
  return window.locator('.tab-fold')
}

test('the button folds every open card, then opens them all again', async ({ window }) => {
  await open(window)
  // A recent write opens itself, so there is something to fold before anything
  // is pressed.
  await expect(window.locator('.tool-detail')).toHaveCount(1)

  await fold(window).click()
  await expect(window.locator('.tool-detail')).toHaveCount(0)

  await fold(window).click()
  await expect(window.locator('.tool-detail')).toHaveCount(2)
})

test('a card opened by hand is still reached by the next press', async ({ window }) => {
  await open(window)

  await fold(window).click()
  await expect(window.locator('.tool-detail')).toHaveCount(0)

  // Opened by hand while the button says "open every one".
  await window.locator('.tool-head').first().click()
  await expect(window.locator('.tool-detail')).toHaveCount(1)

  // Open all, then fold all. The hand-opened card has to answer both — a flag
  // it already agreed with would leave it behind.
  await fold(window).click()
  await expect(window.locator('.tool-detail')).toHaveCount(2)

  await fold(window).click()
  await expect(window.locator('.tool-detail')).toHaveCount(0)
})

// Folding is a standing instruction for the session, not a press that expires:
// a write the agent makes afterwards arrives folded too.
test('a write that arrives after a fold all arrives folded', async ({ window }) => {
  await open(window)

  await fold(window).click()
  await expect(window.locator('.tool-detail')).toHaveCount(0)

  addToolCall(ALPHA_ID, {
    tool: 'Edit',
    summary: 'later.go',
    input: { file_path: '/repo/later.go', old_string: 'x', new_string: 'y' },
  })
  // The record moving is what tells the panel there is more to read.
  pushEvent('session_status', { session_id: ALPHA_ID, status: 'idle' })

  await expect(window.locator('.msg.tool-call')).toHaveCount(3)
  await expect(window.locator('.tool-detail')).toHaveCount(0)
})

// The panel is unmounted after five minutes out of sight, so without this the
// fold lasts exactly as long as the reader's attention does.
test('the fold outlives the panel, and the window', async ({ window }) => {
  await open(window)
  await fold(window).click()

  const stored = await window.evaluate(() => localStorage.getItem('helios.foldModes'))
  expect(stored).toContain('folded')

  // What a remounted panel reads: the mode, not the press.
  await window.reload()
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.msg.tool-call').first()).toBeVisible()
  await expect(window.locator('.tool-detail')).toHaveCount(0)
  await expect(fold(window)).toHaveAttribute('aria-label', 'Open every tool call')
})

test('opening them all again is remembered too', async ({ window }) => {
  await open(window)
  await fold(window).click()
  await fold(window).click()

  const stored = await window.evaluate(() => localStorage.getItem('helios.foldModes'))
  expect(stored).toContain('open')
})

test('the button says which way it will go', async ({ window }) => {
  await open(window)
  await expect(fold(window)).toHaveAttribute('aria-label', 'Fold every tool call')

  await fold(window).click()
  await expect(fold(window)).toHaveAttribute('aria-label', 'Open every tool call')
})

test('it sits beside the transcript tab, and goes away on another tab', async ({ window }) => {
  await open(window)
  await expect(fold(window)).toHaveCount(1)

  // Next to the tab it acts on, not at the far end of the strip.
  const order = await window
    .locator('.panel-tabs > *')
    .evaluateAll((nodes) => nodes.map((node) => node.className))
  const transcript = order.findIndex((name) => name.includes('active'))
  expect(order[transcript + 1]).toContain('tab-fold')

  await window.locator('.panel-tabs button', { hasText: 'terminal' }).first().click()
  await expect(fold(window)).toHaveCount(0)
})
