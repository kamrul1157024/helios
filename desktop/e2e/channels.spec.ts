// Channels: several sessions and the person, with one conversation running
// through them.
//
// The gesture this was asked for is the first test: select a few sessions,
// press a button, and be in a conversation with them.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, BETA as BETA_ID, resetChannels, seedChannel } from './daemon.ts'
import { expect, test } from './fixtures.ts'

test.beforeEach(() => {
  resetChannels()
})

async function pickSessions(window: Page, count: number): Promise<void> {
  // Creating a channel switches the sidebar to it, so coming back for a second
  // selection starts by going back to the list — but only when we are not
  // already there: the rail item folds the column when it names the mode that
  // is already showing.
  // Hidden, not absent: the sessions list stays mounted behind the other
  // modes, so the question is whether it is on screen.
  if (!(await window.locator('.session-row').first().isVisible())) {
    await window.locator('.rail-item[aria-label="Sessions"]').click()
  }
  await expect(window.locator('.session-row').first()).toBeVisible()
  await window.locator('.select-toggle').click()
  for (let at = 0; at < count; at++) {
    await window.locator('.session-row .row-tick').nth(at).check()
  }
}

test('a few sessions and a button is a conversation', async ({ window, daemon }) => {
  await pickSessions(window, 2)

  await window.locator('.bulk-bar').getByText('New group chat').click()

  // The sidebar switches to the channels it just made one of.
  await expect(window.locator('.channel-row')).toHaveCount(1)
  await expect(window.locator('.channel')).toBeVisible()

  const made = daemon.writes().filter((write) => write.kind === 'channel')
  expect(made).toHaveLength(1)
  if (made[0]?.kind === 'channel') expect(made[0].members).toHaveLength(2)
})

test('the channel is named by who is in it', async ({ window }) => {
  seedChannel({ id: 'ch_1', members: [ALPHA_ID, BETA_ID] })
  await window.locator('.rail-item[aria-label="Channels"]').click()

  // An unnamed channel has no name to show, so it is shown by its members —
  // "ch_1" on a row would say nothing about the conversation.
  await expect(window.locator('.channel-row')).toContainText('Alpha')
  await expect(window.locator('.channel-row')).toContainText('Beta')
  // And no `#`: that mark belongs to a name somebody chose, not to a list of
  // members that happens to stand in for one.
  await expect(window.locator('.channel-name')).not.toContainText('#')
})

test('a named channel wears a hash', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID] })
  await window.locator('.rail-item[aria-label="Channels"]').click()

  await expect(window.locator('.channel-name')).toHaveText('#api-redesign')
})

test('asking twice for the same sessions opens the conversation they have', async ({
  window,
  daemon,
}) => {
  await pickSessions(window, 2)
  await window.locator('.bulk-bar').getByText('New group chat').click()
  await expect(window.locator('.channel-row')).toHaveCount(1)

  await pickSessions(window, 2)
  await window.locator('.bulk-bar').getByText('New group chat').click()

  // Two asks, one channel: an unnamed channel is its members.
  await expect(window.locator('.channel-row')).toHaveCount(1)
  expect(daemon.writes().filter((write) => write.kind === 'channel')).toHaveLength(2)
})

test('what was said is shown, by who said it', async ({ window }) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID, BETA_ID],
    messages: [
      { author: `session:${ALPHA_ID}`, from: 'Alpha', body: 'the response shape changed' },
      { author: 'user', from: 'user', body: 'does the client cope?' },
    ],
  })
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').click()

  await expect(window.locator('.channel-msg')).toHaveCount(2)
  await expect(window.locator('.channel-msg').first()).toContainText('Alpha')
  await expect(window.locator('.channel-msg').first()).toContainText('the response shape changed')
  await expect(window.locator('.channel-msg').nth(1)).toContainText('user')
})

// The session detail stays mounted behind the other modes so that a terminal
// is not disposed on the way past it — but mounted is not shown, and a channel
// sharing the pane with "Select a session." gets half the height it needs.
test('a channel has the pane to itself', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID] })
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').click()

  await expect(window.locator('.panel-empty')).toBeHidden()

  const pane = await window.locator('.channel').boundingBox()
  const app = await window.locator('.detail').first().boundingBox()
  expect(pane?.height).toBeGreaterThan((app?.height ?? 0) * 0.9)
})

// Every session used to render in the same accent, so a busy channel was a
// wall of identical headers and telling who said what meant reading each one.
test('each session is a different colour, and the person is not coloured at all', async ({
  window,
}) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID, BETA_ID],
    messages: [
      { author: `session:${ALPHA_ID}`, from: 'Alpha', body: 'the response shape changed' },
      { author: `session:${BETA_ID}`, from: 'Beta', body: 'porting it now' },
      { author: 'user', from: 'user', body: 'does the client cope?' },
    ],
  })
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').click()

  const colourOf = (at: number): Promise<string> =>
    window
      .locator('.channel-msg')
      .nth(at)
      .locator('.channel-from')
      .evaluate((node) => getComputedStyle(node).color)

  const [alpha, beta, person] = [await colourOf(0), await colourOf(1), await colourOf(2)]
  expect(alpha).not.toBe(beta)
  expect(person).not.toBe(alpha)
  expect(person).not.toBe(beta)

  // The rule down the left edge carries the same colour, so one agent can be
  // followed without reading a name. The person has no rule.
  await expect(window.locator('.channel-msg.from-session')).toHaveCount(2)
  await expect(window.locator('.channel-msg.you')).toHaveCount(1)
})

test('a member chip opens that session', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID] })
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').click()

  await window.locator('.channel-members-toggle').click()
  await window.locator('.member-list .member-chip').first().click()

  // Two seconds after reading a message, the reader wants the session that
  // wrote it.
  await expect(window.locator('.panel-tabs')).toBeVisible()
})

test('typing in the channel posts to it', async ({ window, daemon }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID] })
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').click()

  // The box has the keyboard already: the reason for opening a channel is
  // usually to say something in it.
  await expect(window.locator('.channel textarea')).toBeFocused()
  await window.keyboard.type('align on {items, next}')
  await window.keyboard.press('Enter')

  await expect.poll(() => daemon.writes().filter((w) => w.kind === 'post').length).toBe(1)
  await expect(window.locator('.channel-msg')).toContainText('align on {items, next}')
  await expect(window.locator('.channel textarea')).toHaveValue('')
})

test('a draft in a channel survives leaving it', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID] })
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').click()
  await window.locator('.channel textarea').fill('half a thought')

  await window.locator('.rail-item[aria-label="Sessions"]').click()
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').click()

  await expect(window.locator('.channel textarea')).toHaveValue('half a thought')
})
