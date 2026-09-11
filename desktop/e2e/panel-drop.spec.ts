// A file dragged at a conversation is meant for the conversation.
//
// The drop target used to be the composer alone — a 40px strip at the foot of
// the panel. These check the whole panel takes it, which is a thing that breaks
// silently: a missed drop looks like a file that simply did not attach.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

async function open(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

/** Drags a file onto whatever the selector names, and lets go of it. */
async function dropOn(window: Page, selector: string, name: string): Promise<void> {
  await window.evaluate(
    ([target, filename]) => {
      const carried = new DataTransfer()
      carried.items.add(new File(['a stack trace'], filename as string, { type: 'text/plain' }))
      const el = document.querySelector(target as string)
      if (!el) throw new Error(`nothing at ${target} to drop on`)
      el.dispatchEvent(new DragEvent('dragover', { dataTransfer: carried, bubbles: true }))
      el.dispatchEvent(new DragEvent('drop', { dataTransfer: carried, bubbles: true }))
    },
    [selector, name],
  )
}

test('a file dropped on the transcript attaches to the prompt', async ({ window }) => {
  await open(window)

  // The scroller, which is the part of the panel a reader is looking at — and
  // nowhere near the composer that used to be the only target.
  await dropOn(window, '.chat-scroll', 'trace.txt')

  await expect(window.locator('.attachment-name')).toHaveText('trace.txt')
})

test('dragging over the transcript says it will take the file', async ({ window }) => {
  await open(window)

  await window.evaluate(() => {
    const carried = new DataTransfer()
    carried.items.add(new File(['x'], 'x.txt', { type: 'text/plain' }))
    document
      .querySelector('.chat-scroll')
      ?.dispatchEvent(new DragEvent('dragover', { dataTransfer: carried, bubbles: true }))
  })

  await expect(window.locator('.chat.dropping')).toHaveCount(1)
})

test('a session dragged from the sidebar is not a file, and is left alone', async ({ window }) => {
  await open(window)

  await window.evaluate(() => {
    const carried = new DataTransfer()
    carried.setData('text/plain', 'not a file')
    document
      .querySelector('.chat-scroll')
      ?.dispatchEvent(new DragEvent('dragover', { dataTransfer: carried, bubbles: true }))
  })

  await expect(window.locator('.chat.dropping')).toHaveCount(0)
})
