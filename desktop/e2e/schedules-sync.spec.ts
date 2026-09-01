// The panel must always be showing the session the sidebar says it is.
//
// Two ways that broke. The transcript is fetched per session, so a stale one is
// the wrong conversation under the right title. And the schedules list, when it
// first landed, replaced the session detail rather than covering it — which
// unmounted every terminal pane, disposed its xterm, and left the connection
// counting bytes in the main process, so coming back painted an empty grid.
//
// These drive both: switch between sessions, and switch the sidebar out to
// schedules and back, checking what is on screen each time.
import type { Page } from '@playwright/test'

import { ALPHA, BETA, TRANSCRIPT_TEXT, appendTranscript, resetTranscripts } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA_TITLE = 'Alpha'
const BETA_TITLE = 'Beta'

async function open(window: Page, title: string): Promise<void> {
  await window.locator('.session-row', { hasText: title }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

async function showPanel(window: Page, label: string): Promise<void> {
  await window.locator('.panel-tabs button', { hasText: label }).first().click()
}

/** The transcript the reader can actually see. */
function transcript(window: Page): ReturnType<Page['locator']> {
  return window.locator('.panel-keep:not([hidden]) .msg.assistant')
}

async function switchSidebar(window: Page, mode: 'sessions' | 'schedules'): Promise<void> {
  await window.locator('.sidebar-modes button', { hasText: mode }).click()
}

test.beforeEach(async ({ window }) => {
  resetTranscripts()
  await expect(window.locator('.session-row', { hasText: ALPHA_TITLE })).toBeVisible()
})

// Leaving a session and coming back asks the daemon for everything after the
// last message the panel holds. It used to take that mark from a ref written
// one effect too late and shared across sessions, so on a switch it asked from
// the *other* conversation's position and appended what this one already had.
// On screen that reads as the old transcript coming back.
test('coming back to a session does not print its transcript twice', async ({ window }) => {
  await open(window, ALPHA_TITLE)
  await showPanel(window, 'agent')
  await expect(transcript(window)).toHaveCount(1)

  await open(window, BETA_TITLE)
  await expect(transcript(window)).toHaveCount(1)

  await open(window, ALPHA_TITLE)
  await expect(transcript(window)).toHaveCount(1)
  await expect(transcript(window)).toContainText(TRANSCRIPT_TEXT[ALPHA] as string)
})

test('what was written while you were away is there when you come back', async ({ window }) => {
  await open(window, ALPHA_TITLE)
  await showPanel(window, 'agent')
  await expect(transcript(window)).toHaveCount(1)

  await open(window, BETA_TITLE)
  appendTranscript(ALPHA, 'written while you were elsewhere')

  await open(window, ALPHA_TITLE)
  await expect(transcript(window)).toHaveCount(2)
  await expect(transcript(window).nth(1)).toContainText('written while you were elsewhere')
})

test('the transcript belongs to the session the sidebar has selected', async ({ window }) => {
  await open(window, ALPHA_TITLE)
  await showPanel(window, 'agent')
  await expect(transcript(window)).toContainText(TRANSCRIPT_TEXT[ALPHA] as string)

  await open(window, BETA_TITLE)
  await showPanel(window, 'agent')
  await expect(transcript(window)).toContainText(TRANSCRIPT_TEXT[BETA] as string)
  // The one that matters: no trace of the session that was on screen before.
  await expect(transcript(window)).not.toContainText(TRANSCRIPT_TEXT[ALPHA] as string)

  await open(window, ALPHA_TITLE)
  await expect(transcript(window)).toContainText(TRANSCRIPT_TEXT[ALPHA] as string)
  await expect(transcript(window)).not.toContainText(TRANSCRIPT_TEXT[BETA] as string)
})

test('switching the sidebar to schedules and back keeps the same terminal', async ({ window }) => {
  await open(window, ALPHA_TITLE)
  await showPanel(window, 'terminal')

  const pane = window.locator('.panel-keep:not([hidden]) .terminal-pane, .panel-keep:not([hidden])')
  await expect(pane.first()).toBeVisible()

  // Marked in the DOM. If the pane is unmounted and rebuilt, the mark goes with
  // it — which is exactly the failure this test exists for, and is invisible to
  // any assertion about what is merely on screen.
  await window.evaluate(() => {
    const node = document.querySelector('.panel-keep:not([hidden])')
    node?.setAttribute('data-e2e-mark', 'kept')
  })

  await switchSidebar(window, 'schedules')
  await expect(window.locator('.sched-row', { hasText: 'nightly-sweep' })).toBeVisible()

  await switchSidebar(window, 'sessions')
  await expect(window.locator('.panel-tabs')).toBeVisible()

  await expect(window.locator('[data-e2e-mark="kept"]')).toHaveCount(1)
})

test('the schedules list does not carry the session panels away with it', async ({ window }) => {
  await open(window, ALPHA_TITLE)
  await showPanel(window, 'agent')
  await expect(transcript(window)).toContainText(TRANSCRIPT_TEXT[ALPHA] as string)

  await switchSidebar(window, 'schedules')
  // Covered, not gone: the detail is still in the tree with no layout.
  await expect(window.locator('.detail')).toHaveCount(2)

  await switchSidebar(window, 'sessions')
  await expect(window.locator('.detail')).toHaveCount(1)
  // Still the session it was, and still its own transcript.
  await expect(window.locator('.session-row.active, .session-row.selected')).toBeVisible()
  await expect(transcript(window)).toContainText(TRANSCRIPT_TEXT[ALPHA] as string)
})
