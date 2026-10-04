// The window slides rather than grows: a page loaded at one end drops a page off
// the other, so a channel of any length holds a fixed slice, and every drop is
// reversible by scrolling back the way you came.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  CHANNEL_MAX_HELD,
  CHANNEL_PAGE_SIZE,
  openingWindow,
  overflowOf,
  trimmedNewest,
  withNewer,
  withOlder,
} from '../src/renderer/channel-window.ts'
import type { ChannelMessage } from '../src/shared/models.ts'

function message(n: number): ChannelMessage {
  return { id: `m${n}`, author: 'user', from: 'user', body: `message ${n}`, created_at: '' }
}

function range(from: number, to: number): ChannelMessage[] {
  const out: ChannelMessage[] = []
  for (let n = from; n < to; n++) out.push(message(n))
  return out
}

test('a full opening page has more behind it, a short one is the start', () => {
  assert.equal(openingWindow(range(0, CHANNEL_PAGE_SIZE)).hasOlder, true)
  assert.equal(openingWindow(range(0, CHANNEL_PAGE_SIZE)).hasNewer, false)
  assert.equal(openingWindow(range(0, 5)).hasOlder, false)
})

test('older prepends and keeps oldest-first order', () => {
  const held = openingWindow(range(80, 100))
  const grown = withOlder(held, range(60, 80))
  assert.equal(grown.messages[0]?.body, 'message 60')
  assert.equal(grown.messages[grown.messages.length - 1]?.body, 'message 99')
  assert.equal(grown.messages.length, 40)
  assert.equal(grown.hasOlder, true)
})

test('a short older page means the top has been reached', () => {
  const held = { messages: range(5, 25), hasOlder: true, hasNewer: false }
  assert.equal(withOlder(held, range(0, 5)).hasOlder, false)
})

test('loading older past the cap trims the newest, reversibly', () => {
  let win = { messages: range(0, CHANNEL_MAX_HELD), hasOlder: true, hasNewer: false }
  win = withOlder(win, range(-CHANNEL_PAGE_SIZE, 0))
  assert.equal(win.messages.length, CHANNEL_MAX_HELD + CHANNEL_PAGE_SIZE)

  const over = overflowOf(win)
  assert.equal(over, CHANNEL_PAGE_SIZE)

  win = trimmedNewest(win, over)
  assert.equal(win.messages.length, CHANNEL_MAX_HELD)
  assert.equal(win.messages[0]?.body, `message ${-CHANNEL_PAGE_SIZE}`)
  assert.equal(win.messages[win.messages.length - 1]?.body, `message ${CHANNEL_MAX_HELD - 1 - CHANNEL_PAGE_SIZE}`)
  assert.equal(win.hasNewer, true)
})

test('newer appends and drops the oldest past the cap', () => {
  const held = { messages: range(0, CHANNEL_MAX_HELD), hasOlder: true, hasNewer: true }
  const grown = withNewer(held, range(CHANNEL_MAX_HELD, CHANNEL_MAX_HELD + CHANNEL_PAGE_SIZE))
  assert.equal(grown.messages.length, CHANNEL_MAX_HELD)
  assert.equal(grown.messages[grown.messages.length - 1]?.body, `message ${CHANNEL_MAX_HELD + CHANNEL_PAGE_SIZE - 1}`)
  assert.equal(grown.messages[0]?.body, `message ${CHANNEL_PAGE_SIZE}`)
  assert.equal(grown.hasOlder, true)
  assert.equal(grown.hasNewer, true)
})

test('a short newer page means the live tail is back in view', () => {
  const held = { messages: range(0, 40), hasOlder: false, hasNewer: true }
  assert.equal(withNewer(held, range(40, 45)).hasNewer, false)
})

test('empty pages leave the window untouched', () => {
  const held = openingWindow(range(0, 20))
  assert.equal(withOlder(held, []).messages.length, 20)
  assert.equal(withNewer(held, []).messages.length, 20)
  assert.equal(trimmedNewest(held, 0).messages.length, 20)
})
