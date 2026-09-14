// Escape in the prompt box ends the turn.
//
// The ■ beside the box has always done it, but the hand that wants to stop an
// agent is already on the keys. These rules live in a component and there is no
// component test framework here, so they are checked from the running app.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, type StubDaemon } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

function box(window: Page) {
  return window.locator('.composer textarea')
}

async function open(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

/** Both halves: the event the client hears, and the list behind it agreeing. */
async function runTurn(window: Page, daemon: StubDaemon): Promise<void> {
  daemon.setStatus(ALPHA_ID, 'active')
  daemon.emit('session_status', { session_id: ALPHA_ID, status: 'active' })
  await expect(window.locator('.composer .stop-btn')).toBeVisible()
}

test('Escape stops a running turn', async ({ window, daemon }) => {
  await open(window)
  await runTurn(window, daemon)

  await box(window).click()
  await window.keyboard.press('Escape')

  await expect
    .poll(() => daemon.writes().filter((write) => write.kind === 'stop').length)
    .toBe(1)
})

// Writing the follow-up while the agent works is the normal way to use this
// box. A draft is no reason to withhold the shortcut, and no reason to throw
// the draft away either.
test('a half-typed draft neither blocks the stop nor is lost to it', async ({ window, daemon }) => {
  await open(window)
  await runTurn(window, daemon)

  await box(window).click()
  await window.keyboard.type('and after that, run the tests')
  await window.keyboard.press('Escape')

  await expect.poll(() => daemon.writes().filter((write) => write.kind === 'stop').length).toBe(1)
  await expect(box(window)).toHaveValue('and after that, run the tests')
})

// Idle, Escape is somebody else's key.
test('Escape does nothing when there is no turn to end', async ({ window, daemon }) => {
  await open(window)
  await expect(window.locator('.composer .stop-btn')).toHaveCount(0)

  await box(window).click()
  await window.keyboard.press('Escape')

  // Given a moment to be wrong in: the assertion is that nothing was sent, and
  // an immediate check would pass before a request had had time to go out.
  await window.waitForTimeout(250)
  expect(daemon.writes().filter((write) => write.kind === 'stop')).toHaveLength(0)
})

test('the placeholder offers the shortcut only while it would do something', async ({ window, daemon }) => {
  await open(window)
  await expect(box(window)).toHaveAttribute('placeholder', /↵ to send/)

  await runTurn(window, daemon)
  await expect(box(window)).toHaveAttribute('placeholder', /esc to stop/)
})
