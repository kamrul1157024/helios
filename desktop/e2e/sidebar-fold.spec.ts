// Folding the list column away.
//
// The trap this guards is a rail button that looks broken: asking for a mode
// while the column is folded has to bring the column back, or the click does
// nothing visible and the app looks dead.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

function sidebar(window: Page) {
  return window.locator('.sidebar')
}

function fold(window: Page) {
  return window.locator('.rail-fold')
}

test('the fold button hides the list and brings it back', async ({ window }) => {
  await expect(sidebar(window)).toBeVisible()

  await fold(window).click()
  await expect(sidebar(window)).toHaveCount(0)
  // The rail stays: what is open is still reachable, and so is the way back.
  await expect(window.locator('.rail')).toBeVisible()

  await fold(window).click()
  await expect(sidebar(window)).toBeVisible()
})

test('⌘B folds it too', async ({ window }) => {
  await expect(sidebar(window)).toBeVisible()

  await window.keyboard.press('ControlOrMeta+b')
  await expect(sidebar(window)).toHaveCount(0)

  await window.keyboard.press('ControlOrMeta+b')
  await expect(sidebar(window)).toBeVisible()
})

test('⌘B in the prompt is left to the prompt', async ({ window }) => {
  await window.locator('.session-row').first().click()
  await window.locator('.composer textarea').click()

  await window.keyboard.press('ControlOrMeta+b')

  // Bold belongs to the field that has the keyboard, not to the window.
  await expect(sidebar(window)).toBeVisible()
})

test('asking for a mode while folded brings the list back showing it', async ({ window }) => {
  await fold(window).click()
  await expect(sidebar(window)).toHaveCount(0)

  await window.locator('.rail-item[aria-label="Schedules"]').click()

  await expect(sidebar(window)).toBeVisible()
  await expect(window.locator('.rail-item[aria-label="Schedules"]')).toHaveAttribute('aria-pressed', 'true')
})

test('picking the mode already showing folds it', async ({ window }) => {
  await expect(sidebar(window)).toBeVisible()

  await window.locator('.rail-item[aria-label="Sessions"]').click()

  await expect(sidebar(window)).toHaveCount(0)
  await expect(window.locator('.rail-item[aria-label="Sessions"]')).toHaveAttribute('aria-pressed', 'false')
})
