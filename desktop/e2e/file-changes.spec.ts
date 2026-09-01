// A file moving under an open tab.
//
// The rule these drive is the one with no unit test behind it, because it lives
// in a component and there is no component test framework here: a clean tab
// takes what is on disk, a dirty tab keeps every character and says so, and an
// event carrying the bytes already on screen does nothing at all.
//
// See docs/specs/53-file-change-events.md.
import type { Locator, Page } from '@playwright/test'

import { REPO } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'
const MAIN = `${REPO}/main.go`

function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

/** Opens Alpha's files panel with main.go in front, in the editor. */
async function openFile(window: Page): Promise<Locator> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: 'files' }).first().click()
  await expect(shown(window)).toBeVisible()
  await shown(window).locator('.tree-row', { hasText: 'main.go' }).click()
  const editor = shown(window).locator('.cm-content')
  await expect(editor).toContainText('package main')
  return editor
}

/** What the daemon broadcasts once its sweep has found the file changed. */
function moved(path: string): [string, unknown] {
  return ['file_changed', { paths: [{ path, kind: 'file', mod_time: '2026-01-01T00:00:09Z' }] }]
}

test.beforeEach(async ({ window }) => {
  await expect(window.locator('.session-row', { hasText: ALPHA })).toBeVisible()
})

test('an agent edit lands in an open tab with nobody clicking anything', async ({ window, daemon }) => {
  const editor = await openFile(window)

  daemon.setFile(MAIN, 'package main // edited by the agent\n')
  daemon.emit(...moved(MAIN))

  await expect(editor).toContainText('edited by the agent')
  // A clean tab is replaced outright: no bar, nothing to answer.
  await expect(shown(window).locator('.ws-stale-bar')).toHaveCount(0)
})

test('an unsaved buffer survives an edit, and the bar says so', async ({ window, daemon }) => {
  const editor = await openFile(window)

  await editor.click()
  await window.keyboard.type('// mine')
  await expect(shown(window).locator('.ws-tab-close')).toHaveText('●')

  daemon.setFile(MAIN, 'package main // theirs\n')
  daemon.emit(...moved(MAIN))

  await expect(shown(window).locator('.ws-stale-bar')).toBeVisible()
  // Every character still there, and not a word of theirs in the buffer.
  await expect(editor).toContainText('// mine')
  await expect(editor).not.toContainText('// theirs')
  await expect(shown(window).locator('.ws-tab-close')).toHaveText('●')
})

test('reload takes what is on disk, and only when asked', async ({ window, daemon }) => {
  const editor = await openFile(window)

  await editor.click()
  await window.keyboard.type('// mine')
  daemon.setFile(MAIN, 'package main // theirs\n')
  daemon.emit(...moved(MAIN))
  await expect(shown(window).locator('.ws-stale-bar')).toBeVisible()

  await shown(window).locator('.ws-stale-bar button', { hasText: 'Reload' }).click()

  await expect(editor).toContainText('// theirs')
  await expect(editor).not.toContainText('// mine')
  await expect(shown(window).locator('.ws-stale-bar')).toHaveCount(0)
  await expect(shown(window).locator('.ws-tab-close')).toHaveText('✕')
})

// The daemon compares digests before it speaks, but the client compares bytes
// too: this is the echo of a save, which the broadcaster cannot skip because it
// has no addressing. Acting on it would remount the editor under whoever typed.
//
// Waits on the re-read rather than on the absence of a bar. A bar that has not
// appeared yet and a bar that is never going to appear look identical, so
// asserting the absence on its own passes before the event has crossed the
// socket — which it did, until this test was checked against a mutation.
test('an event carrying the bytes already on screen changes nothing', async ({ window, daemon }) => {
  const editor = await openFile(window)

  await editor.click()
  await window.keyboard.type('// mine')
  await expect(shown(window).locator('.ws-tab-close')).toHaveText('●')

  // Content untouched: the same answer the tab already holds.
  const before = daemon.reads(MAIN)
  daemon.emit(...moved(MAIN))
  await expect.poll(() => daemon.reads(MAIN)).toBeGreaterThan(before)

  // Re-read, compared, and nothing done: no bar over a file that did not move.
  await expect(shown(window).locator('.ws-stale-bar')).toHaveCount(0)
  await expect(editor).toContainText('// mine')
})

test('an event about another file leaves this tab alone', async ({ window, daemon }) => {
  const editor = await openFile(window)

  daemon.setFile(`${REPO}/README.md`, '# elsewhere\n')
  daemon.emit(...moved(`${REPO}/README.md`))

  // A second event, for a file that really is open, is what proves the first
  // has been dealt with: the stream is ordered, so this cannot land first.
  daemon.setFile(MAIN, 'package main // later\n')
  daemon.emit(...moved(MAIN))
  await expect(editor).toContainText('// later')

  await expect(editor).not.toContainText('elsewhere')
  await expect(shown(window).locator('.ws-stale-bar')).toHaveCount(0)
})
