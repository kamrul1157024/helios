// A half-written prompt is work. These check it survives the three ways it
// used to be lost: switching session, closing the dialog, and the panel being
// unmounted after five minutes off screen.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'
const BETA = 'Beta'

async function open(window: Page, title: string): Promise<void> {
  await window.locator('.session-row', { hasText: title }).click()
  await expect(window.locator('.composer textarea')).toBeVisible()
}

function box(window: Page) {
  return window.locator('.composer textarea')
}

test('a draft survives switching to another session and back', async ({ window }) => {
  await open(window, ALPHA)
  await box(window).fill('rebase onto main and re-run the gates')

  await open(window, BETA)
  // Another session is another draft, not the same one.
  await expect(box(window)).toHaveValue('')

  await open(window, ALPHA)
  await expect(box(window)).toHaveValue('rebase onto main and re-run the gates')
})

test('each session keeps its own', async ({ window }) => {
  await open(window, ALPHA)
  await box(window).fill('for alpha')
  await open(window, BETA)
  await box(window).fill('for beta')

  await open(window, ALPHA)
  await expect(box(window)).toHaveValue('for alpha')
  await open(window, BETA)
  await expect(box(window)).toHaveValue('for beta')
})

test('sending clears it, so it does not come back next time', async ({ window }) => {
  await open(window, ALPHA)
  await box(window).fill('send me')
  await box(window).press('Enter')

  await expect(box(window)).toHaveValue('')
  await open(window, BETA)
  await open(window, ALPHA)
  await expect(box(window)).toHaveValue('')
})

test('a draft outlives the window itself', async ({ window }) => {
  await open(window, ALPHA)
  await box(window).fill('still here tomorrow')

  await window.reload()
  await open(window, ALPHA)
  await expect(box(window)).toHaveValue('still here tomorrow')
})

test('what was typed into the new-session dialog is there when it reopens', async ({ window }) => {
  await window.locator('.tool.primary').click()
  const prompt = window.locator('.modal-backdrop textarea').first()
  await expect(prompt).toBeVisible()
  await prompt.fill('look into the flake in the hooks test')

  // Closed the way somebody closes it to go and check something.
  await window.keyboard.press('Escape')
  await expect(window.locator('.modal-backdrop')).toHaveCount(0)

  await window.locator('.tool.primary').click()
  await expect(window.locator('.modal-backdrop textarea').first()).toHaveValue(
    'look into the flake in the hooks test',
  )
})
