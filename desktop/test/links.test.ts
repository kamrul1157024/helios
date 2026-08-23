// Terminal links open in the browser by handing the real URL to window.open,
// which the main process routes to shell.openExternal. Regression guard for the
// "OK does nothing" bug where xterm called window.open() with no URL.
import assert from 'node:assert/strict'
import { afterEach, beforeEach, test } from 'node:test'

import { linkHandler, openLink, webLinkActivate } from '../src/renderer/components/links.ts'

type OpenCall = [url?: string | URL, target?: string, features?: string]

let calls: OpenCall[]
const original = (globalThis as { window?: unknown }).window

beforeEach(() => {
  calls = []
  ;(globalThis as { window?: unknown }).window = {
    open: (...args: OpenCall) => {
      calls.push(args)
      return null // Electron denies the popup; the return is unused.
    },
  }
})

afterEach(() => {
  ;(globalThis as { window?: unknown }).window = original
})

const URL_ = 'https://github.com/newscred/opal-app/pull/7053'

test('openLink hands the real URL to window.open (not an empty call)', () => {
  openLink(URL_)
  assert.equal(calls.length, 1)
  assert.deepEqual(calls[0], [URL_, '_blank', 'noopener,noreferrer'])
  // The bug was calling window.open() with no URL — assert we never do that.
  assert.notEqual(calls[0][0], undefined)
  assert.notEqual(calls[0][0], '')
})

test('OSC 8 linkHandler.activate opens the link', () => {
  linkHandler.activate(new Event('click') as MouseEvent, URL_, { start: { x: 1, y: 1 }, end: { x: 1, y: 1 } })
  assert.deepEqual(calls[0], [URL_, '_blank', 'noopener,noreferrer'])
})

test('WebLinksAddon handler opens the link', () => {
  webLinkActivate(new Event('click') as MouseEvent, URL_)
  assert.deepEqual(calls[0], [URL_, '_blank', 'noopener,noreferrer'])
})
