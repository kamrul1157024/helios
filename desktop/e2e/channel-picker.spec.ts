// Putting a session into a channel that already exists, and closing one that
// is finished. See docs/specs/60-group-chat.md.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, BETA as BETA_ID, resetChannels, seedChannel } from './daemon.ts'
import { expect, test } from './fixtures.ts'

test.beforeEach(() => {
  resetChannels()
})

/** Right-click the first session row, which is where the menu hangs off. */
async function openRowMenu(window: Page): Promise<void> {
  await expect(window.locator('.session-row').first()).toBeVisible()
  await window.locator('.session-row').first().click({ button: 'right' })
}

async function openChannels(window: Page): Promise<void> {
  await window.locator('.rail-item[aria-label="Channels"]').click()
}

test('the row menu asks which channel, and the list is searchable', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [BETA_ID] })
  seedChannel({ id: 'ch_2', name: 'flaky-hooks-test', members: [BETA_ID] })

  await openRowMenu(window)
  await window.getByText('Add to channel…').click()

  // A submenu would be a scroll at thirty channels. The field is the point.
  await expect(window.locator('.quick-input')).toBeFocused()
  await expect(window.locator('.quick-row')).toHaveCount(3) // two, and "New channel"

  await window.keyboard.type('flaky')
  await expect(window.locator('.quick-row')).toHaveCount(2)
  await expect(window.locator('.quick-row').first()).toContainText('flaky-hooks-test')
})

test('a channel is findable by who is in it, not only by its name', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [BETA_ID] })

  await openRowMenu(window)
  await window.getByText('Add to channel…').click()
  // "Beta" is a member's title, not the channel's name. Somebody looking for
  // the conversation a session is in searches for the session.
  await window.keyboard.type('Beta')

  await expect(window.locator('.quick-row').first()).toContainText('api-redesign')
})

test('Enter puts the session in the channel', async ({ window, daemon }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [BETA_ID] })

  await openRowMenu(window)
  await window.getByText('Add to channel…').click()
  await window.keyboard.press('Enter')

  const joined = daemon.writes().filter((write) => write.kind === 'join')
  expect(joined).toHaveLength(1)
  if (joined[0]?.kind === 'join') expect(joined[0].channelId).toBe('ch_1')

  // Having done it, the app shows the conversation it was done to.
  await expect(window.locator('.channel')).toBeVisible()
})

// Searching and creating are one gesture: a name that matched nothing is the
// name of the channel you meant to make.
test('a name that matches nothing offers to create it', async ({ window, daemon }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [BETA_ID] })

  await openRowMenu(window)
  await window.getByText('Add to channel…').click()
  await window.keyboard.type('orders-migration')

  await expect(window.locator('.quick-row')).toHaveCount(1)
  await expect(window.locator('.quick-row.new-channel')).toContainText('orders-migration')
  await window.keyboard.press('Enter')

  const made = daemon.writes().filter((write) => write.kind === 'channel')
  expect(made).toHaveLength(1)
  if (made[0]?.kind === 'channel') expect(made[0].name).toBe('orders-migration')
})

test('a closed channel is not somewhere to put anybody', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [BETA_ID], archived: true })
  seedChannel({ id: 'ch_2', name: 'flaky-hooks-test', members: [BETA_ID] })

  await openRowMenu(window)
  await window.getByText('Add to channel…').click()

  // The daemon refuses the join, so offering it would be offering an error.
  await expect(window.locator('.quick-list')).not.toContainText('api-redesign')
  await expect(window.locator('.quick-list')).toContainText('flaky-hooks-test')
})

test('Escape closes the picker and adds nobody', async ({ window, daemon }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [BETA_ID] })

  await openRowMenu(window)
  await window.getByText('Add to channel…').click()
  await expect(window.locator('.quick')).toBeVisible()
  await window.keyboard.press('Escape')

  await expect(window.locator('.quick')).toBeHidden()
  expect(daemon.writes().filter((write) => write.kind === 'join')).toHaveLength(0)
})

test('closing a channel puts it away, and it can be read back', async ({ window, daemon }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID] })
  await openChannels(window)

  await window.locator('.channel-row').click({ button: 'right' })
  await window.getByText('Close', { exact: true }).click()

  const archived = daemon.writes().filter((write) => write.kind === 'archive')
  expect(archived).toHaveLength(1)
  if (archived[0]?.kind === 'archive') expect(archived[0].archived).toBe(true)

  // Out of the list, but not gone: it is under a disclosure that starts shut.
  await expect(window.locator('.channel-row')).toHaveCount(0)
  await expect(window.locator('.channel-closed-head')).toContainText('Closed (1)')
  await window.locator('.channel-closed-head').click()
  await expect(window.locator('.channel-row')).toHaveCount(1)
})

// Read-only is the promise archiving makes. A composer that took text and then
// had it refused would be worse than none at all.
test('a closed channel has no composer, and says why', async ({ window }) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID],
    archived: true,
    messages: [{ author: 'user', from: 'user', body: 'that is the last of it' }],
  })
  await openChannels(window)
  await window.locator('.channel-closed-head').click()
  await window.locator('.channel-row').click()

  await expect(window.locator('.channel textarea')).toHaveCount(0)
  await expect(window.locator('.channel-closed-note')).toContainText('takes no more messages')
  // What was said is still there to read.
  await expect(window.locator('.channel-msg')).toContainText('that is the last of it')
})

test('Reopen brings the composer back', async ({ window, daemon }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID], archived: true })
  await openChannels(window)
  await window.locator('.channel-closed-head').click()
  await window.locator('.channel-row').click()

  await window.locator('.channel-closed-note button').click()

  const archived = daemon.writes().filter((write) => write.kind === 'archive')
  expect(archived).toHaveLength(1)
  if (archived[0]?.kind === 'archive') expect(archived[0].archived).toBe(false)
  await expect(window.locator('.channel textarea')).toBeVisible()
})
