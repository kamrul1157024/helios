// The prompt takes the keyboard when the transcript comes up.
//
// Coming back to a session to say something is the usual reason for coming
// back to it — and for anyone dictating, a click on the box first is the whole
// cost of the feature.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

test('opening a session puts the caret in the prompt', async ({ window }) => {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.composer textarea')).toBeVisible()

  await expect(window.locator('.composer textarea')).toBeFocused()
  // And it is ready to type into, with no click anywhere.
  await window.keyboard.type('rebase onto main')
  await expect(window.locator('.composer textarea')).toHaveValue('rebase onto main')
})

test('coming back from the terminal gives the prompt the keyboard again', async ({ window }) => {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.composer textarea')).toBeVisible()

  await window.locator('.panel-tabs button', { hasText: 'terminal' }).first().click()
  await window.locator('.panel-tabs button', { hasText: 'transcript' }).first().click()

  await expect(window.locator('.composer textarea')).toBeFocused()
})
