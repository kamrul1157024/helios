// Two sessions, one directory.
//
// Everything a panel holds is per session, and the case that used to break it
// is two sessions in the same checkout: the panels reset themselves on a change
// of directory, which never came, so the second session opened on the first
// one's search, file and worktree. These drive that switch and check what is on
// screen afterwards.
import type { Locator, Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'
const BETA = 'Beta'

/**
 * The panel on screen.
 *
 * Panels the user has visited stay mounted behind `hidden` so a tab switch does
 * not throw away where they were, which means an unscoped selector matches the
 * panel nobody is looking at as readily as the one they are.
 */
function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

async function open(window: Page, title: string): Promise<void> {
  await window.locator('.session-row', { hasText: title }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

async function showPanel(window: Page, label: string): Promise<void> {
  await window.locator('.panel-tabs button', { hasText: label }).first().click()
  await expect(shown(window)).toBeVisible()
}

/**
 * Leaves both sessions on the same panel, which is the state the leak needs.
 *
 * Which panel a session is on is remembered per session, so a session that has
 * never been shown one opens on the agent transcript — and a panel that is not
 * on screen for either session is torn down for reasons that have nothing to do
 * with what is under test. Two sessions sat on the same panel is also the
 * ordinary way to work: read the same file on two branches.
 */
async function bothOn(window: Page, label: string): Promise<void> {
  await open(window, BETA)
  await showPanel(window, label)
  await open(window, ALPHA)
  await showPanel(window, label)
}

test.beforeEach(async ({ window }) => {
  await expect(window.locator('.session-row', { hasText: ALPHA })).toBeVisible()
})

test('a search typed in one session does not follow into the next', async ({ window }) => {
  await bothOn(window, 'files')

  await shown(window).locator('.ws-view', { hasText: 'Search' }).click()
  await shown(window).locator('.find-input').fill('needle')
  await shown(window).locator('.find-input').press('Enter')
  await expect(shown(window).locator('.find-input')).toHaveValue('needle')

  await open(window, BETA)

  // Beta opens on the explorer, which is where a session with no history of
  // its own starts. The search field belongs to a view nobody asked for here.
  await expect(shown(window).locator('.find-input')).toHaveCount(0)
  await expect(shown(window).locator('.ws-view.on')).toHaveText('Explorer')
})

test('an open file does not follow into the next session', async ({ window }) => {
  await bothOn(window, 'files')

  await shown(window).locator('.tree-row', { hasText: 'main.go' }).click()
  await expect(shown(window).locator('.ws-tab')).toHaveCount(1)

  await open(window, BETA)

  await expect(shown(window).locator('.ws-tab')).toHaveCount(0)
  await expect(shown(window).locator('.ws-blank')).toBeVisible()
})

test('a worktree picked in one session does not follow into the next', async ({ window }) => {
  await bothOn(window, 'git')

  await shown(window).locator('.ws-view', { hasText: 'Worktrees' }).click()
  await shown(window).locator('.worktree', { hasText: 'hotfix' }).click()
  // The pill is the panel saying it is pointed somewhere other than the
  // session's own checkout.
  await expect(shown(window).locator('.git-head .pill')).toHaveText(/repo-hotfix/)

  await open(window, BETA)

  await expect(shown(window).locator('.git-head .pill')).toHaveCount(0)
  await expect(shown(window).locator('.git-head .branch')).toHaveText('main')
})
