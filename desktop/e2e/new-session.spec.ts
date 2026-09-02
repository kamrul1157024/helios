import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

async function openComposer(window: Page): Promise<void> {
  await window.locator('button[aria-label="New session"]').first().click()
  await expect(window.locator('.composer')).toBeVisible()
}

async function openChip(window: Page, name: RegExp): Promise<void> {
  await window.locator('.composer-foot .picker > summary').filter({ hasText: name }).click()
  await expect(window.locator('.picker[open] .picker-body')).toBeVisible()
}

async function backdrop(window: Page): Promise<{ x: number; y: number }> {
  const box = await window.locator('.composer').boundingBox()
  if (!box) throw new Error('composer has no box')
  return { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height + 120) }
}

test('every chip opens on its own value', async ({ window }) => {
  await openComposer(window)

  await expect(window.locator('.composer-place')).toContainText('repo')
  await expect(window.locator('.composer-host')).toContainText('stub')
  await expect(window.locator('.composer-foot')).toContainText('Claude')
  await expect(window.locator('.composer-foot')).toContainText('Default model')
})

test('picking a model puts it on the chip', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  await window.locator('.composer-option', { hasText: 'Sonnet' }).click()

  await expect(window.locator('.picker[open]')).toHaveCount(0)
  await expect(window.locator('.composer-foot')).toContainText('Sonnet')
})

test('dismissing a chip does not take the dialog with it', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  const at = await backdrop(window)
  await window.mouse.click(at.x, at.y)

  await expect(window.locator('.picker[open]')).toHaveCount(0)
  await expect(window.locator('.composer')).toBeVisible()
})

test('the backdrop still closes an untouched dialog', async ({ window }) => {
  await openComposer(window)

  const at = await backdrop(window)
  await window.mouse.click(at.x, at.y)

  await expect(window.locator('.composer')).toHaveCount(0)
})

test('a written prompt survives a stray click on the backdrop', async ({ window }) => {
  await openComposer(window)
  await window.locator('.composer-prompt').fill('something worth keeping')

  const at = await backdrop(window)
  await window.mouse.click(at.x, at.y)

  await expect(window.locator('.composer-prompt')).toHaveValue('something worth keeping')
})

test('escape closes the chip first and the dialog second', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  await window.keyboard.press('Escape')
  await expect(window.locator('.picker[open]')).toHaveCount(0)
  await expect(window.locator('.composer')).toBeVisible()

  await window.keyboard.press('Escape')
  await expect(window.locator('.composer')).toHaveCount(0)
})

test('arrows walk the options and wrap', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  const options = window.locator('.picker[open] .composer-option')
  await expect(options).toHaveCount(3)

  await window.keyboard.press('ArrowDown')
  await expect(options.nth(0)).toBeFocused()

  await window.keyboard.press('ArrowDown')
  await expect(options.nth(1)).toBeFocused()

  await window.keyboard.press('End')
  await expect(options.nth(2)).toBeFocused()

  await window.keyboard.press('ArrowDown')
  await expect(options.nth(0)).toBeFocused()

  await window.keyboard.press('ArrowUp')
  await expect(options.nth(2)).toBeFocused()
})

test('escape hands focus back to the chip', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  await window.keyboard.press('Escape')

  const summary = window.locator('.composer-foot .picker > summary').filter({ hasText: /model/i })
  await expect(summary).toBeFocused()
})

test('the directory filter narrows the list and takes a typed path', async ({ window }) => {
  await openComposer(window)
  await window.locator('.composer-place > summary').click()

  const search = window.locator('.picker-search')
  await expect(search).toBeFocused()

  await search.fill('nothing-matches-this')
  await expect(window.locator('.picker-empty')).toBeVisible()

  await search.fill('/tmp/elsewhere')
  await window.locator('.composer-option', { hasText: 'Use' }).click()

  await expect(window.locator('.composer-place')).toContainText('elsewhere')
})
