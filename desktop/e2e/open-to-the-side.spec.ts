// Opening a file the transcript names, without losing the transcript.
//
// The rules here live in components, so there is nowhere else in this repo to
// drive them: the chip's two buttons, the split one of them asks for, and the
// file tree that gets out of the way while the panel is a pane on the side.
// That last one is the point of the feature — a tree beside a file in half a
// window leaves the file a quarter of it.
import type { Locator, Page } from '@playwright/test'

import { ALPHA, REPO, appendTranscript, pushEvent, resetTranscripts } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ROW = 'Alpha'
const MAIN = `${REPO}/main.go`
const README = `${REPO}/README.md`

/**
 * The panels on screen.
 *
 * Everything below is scoped to these, as the other specs are: a panel behind
 * another stays mounted and keeps its markup, so an unscoped locator finds the
 * transcript twice and cannot tell which copy the reader is looking at.
 */
function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

/** The Files panel, wherever it currently is. */
function files(window: Page): Locator {
  return shown(window).locator('.workspace')
}

/** A chip in the transcript, by the file name it shows. */
function chip(window: Page, name: string): Locator {
  return shown(window).locator('.file-chip').filter({ hasText: name })
}

/** The chip's ◨ — open the file beside the transcript. */
function sideButton(window: Page, name: string): Locator {
  return chip(window, name).locator('.file-chip-side')
}

/** The chip's ↗ — open the file in the Files tab, in front of the transcript. */
function tabButton(window: Page, name: string): Locator {
  return chip(window, name).locator('.file-chip-tab')
}

/**
 * Alpha open, with a line naming two files at the end of its transcript.
 *
 * One line for both, which is what an agent writes when it has touched both.
 * It arrives on the live edge — appended once the first read has landed, then
 * announced — rather than being in the log before the app starts: a line
 * waiting at launch is read by the initial fetch and by the refetch behind it,
 * and every chip comes out with a twin.
 */
async function openAlpha(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ROW }).click()
  await expect(shown(window).locator('.msg.assistant')).toHaveCount(1)

  appendTranscript(ALPHA, `Edited ${MAIN} and ${README}.`)
  pushEvent('session_status', { session_id: ALPHA, status: 'idle' })

  // Both chips, so a test that clicks the second one is not racing them in.
  await expect(shown(window).locator('.file-chip')).toHaveCount(2)
}

/** One click on ◨, leaving the file open beside the transcript. */
async function openToTheSide(window: Page, name: string): Promise<void> {
  await sideButton(window, name).click()
  await expect(window.locator('.panel-tabs')).toHaveCount(2)
}

// The transcripts are module state shared by every test in the worker.
test.afterEach(() => {
  resetTranscripts()
})

test('a chip opens its file beside the transcript, with the tree out of the way', async ({ window }) => {
  await openAlpha(window)

  await openToTheSide(window, 'main.go')

  // Two strips, two bodies: the transcript did not give up its half.
  await expect(shown(window)).toHaveCount(2)
  await expect(chip(window, 'main.go')).toBeVisible()
  await expect(files(window).locator('.cm-content')).toContainText('package main')
  // The half the pane has goes to the file, not to a tree of the directory it
  // is in — which is the whole reason for opening it here rather than in front.
  await expect(files(window).locator('.ws-side')).toBeHidden()
})

test('with the pane already there, the tab button drops out', async ({ window, daemon }) => {
  // Distinct from main.go, so "the file opened" cannot pass on the file that
  // was already in the pane.
  daemon.setFile(README, '# the readme\n')
  await openAlpha(window)
  // Both buttons while the panel is still in front of the transcript.
  await expect(tabButton(window, 'main.go')).toBeVisible()
  await openToTheSide(window, 'main.go')

  // Every chip loses it, not only the one that was clicked: the button is
  // about where the panel is, and the panel is one panel.
  await expect(tabButton(window, 'main.go')).toHaveCount(0)
  await expect(tabButton(window, 'README.md')).toHaveCount(0)

  await sideButton(window, 'README.md').click()

  // The panel, not `.cm-content`: a Markdown file opens in the preview, and
  // the heading is what the reader ends up looking at.
  await expect(files(window)).toContainText('the readme')
  await expect(window.locator('.panel-tabs')).toHaveCount(2)
})

test('the other button puts the panel in front of the transcript, tree and all', async ({ window }) => {
  await openAlpha(window)

  await tabButton(window, 'main.go').click()

  await expect(window.locator('.panel-tabs')).toHaveCount(1)
  await expect(files(window).locator('.cm-content')).toContainText('package main')
  // A panel with the whole window has room for both, and the transcript is
  // behind it rather than beside it.
  await expect(files(window).locator('.ws-side')).toBeVisible()
  await expect(chip(window, 'main.go')).toBeHidden()
})

test('the tree comes back when the pane stops being a pane', async ({ window }) => {
  await openAlpha(window)
  await openToTheSide(window, 'main.go')
  await expect(files(window).locator('.ws-side')).toBeHidden()

  // The only way back: the tab dragged into the transcript's strip, which
  // empties the group it came from and collapses it.
  const strips = window.locator('.panel-tabs')
  await strips.nth(1).locator('[data-item="panel:files"]').dragTo(strips.first())

  await expect(window.locator('.panel-tabs')).toHaveCount(1)
  await expect(files(window).locator('.ws-side')).toBeVisible()
})

// The rule is about where the panel is, not about who put it there — but the
// reader still outranks it. A derived rule would put the tree away again on the
// next render, which is why the effect behind this watches for the change.
test('a tree the reader opens in the pane stays open', async ({ window, daemon }) => {
  daemon.setFile(README, '# the readme\n')
  await openAlpha(window)
  await openToTheSide(window, 'main.go')

  await files(window).locator('.ws-side-toggle').click()
  await expect(files(window).locator('.ws-side')).toBeVisible()

  await sideButton(window, 'README.md').click()

  // The panel, not `.cm-content`: a Markdown file opens in the preview, and
  // the heading is what the reader ends up looking at.
  await expect(files(window)).toContainText('the readme')
  await expect(files(window).locator('.ws-side')).toBeVisible()
})
