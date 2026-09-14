// Threads and mentions: answering one message without spraying the channel,
// and addressing one session. See docs/specs/60-group-chat.md.
import type { Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, BETA as BETA_ID, resetChannels, seedChannel } from './daemon.ts'
import { expect, test } from './fixtures.ts'

test.beforeEach(() => {
  resetChannels()
})

async function openChannel(window: Page): Promise<void> {
  await window.locator('.rail-item[aria-label="Channels"]').click()
  await window.locator('.channel-row').first().click()
}

/** A channel whose first message has a thread hanging off it. */
function seedWithThread(): void {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID, BETA_ID],
    messages: [
      { author: `session:${ALPHA_ID}`, from: 'Alpha', body: 'the response shape changed' },
      {
        author: `session:${BETA_ID}`,
        from: 'Beta',
        body: 'which call sites?',
        thread_root: 'm_000000000000',
      },
      { author: 'user', from: 'user', body: 'separately: what about the pager?' },
    ],
  })
}

test('a reply is not on the spine, and the spine says it is there', async ({ window }) => {
  seedWithThread()
  await openChannel(window)

  // Two messages said to the channel; the reply is in the thread, not here.
  await expect(window.locator('.channel-scroll .channel-msg')).toHaveCount(2)
  await expect(window.locator('.channel-scroll')).not.toContainText('which call sites?')
  await expect(window.locator('.channel-replies')).toHaveText('1 reply · Beta')
})

test('the reply line opens the thread beside the conversation', async ({ window }) => {
  seedWithThread()
  await openChannel(window)
  await window.locator('.channel-replies').click()

  const thread = window.locator('.channel-thread')
  await expect(thread).toBeVisible()
  // The message it hangs off, then the reply.
  await expect(thread.locator('.channel-msg')).toHaveCount(2)
  await expect(thread).toContainText('which call sites?')

  // And the conversation is still there beside it, which is the point of a
  // panel rather than a replacement.
  await expect(window.locator('.channel .channel-scroll')).toContainText('the pager')
})

test('a reply posted in the panel goes to the thread, not the channel', async ({
  window,
  daemon,
}) => {
  seedWithThread()
  await openChannel(window)
  await window.locator('.channel-replies').click()

  await window.locator('.channel-thread textarea').fill('orders.ts only')
  await window.locator('.channel-thread textarea').press('Enter')

  await expect
    .poll(() => daemon.writes().filter((write) => write.kind === 'post').length)
    .toBe(1)
  const posted = daemon.writes().filter((write) => write.kind === 'post')
  if (posted[0]?.kind === 'post') expect(posted[0].threadRoot).toBe('m_000000000000')
})

// Two boxes, two drafts. A half-typed reply appearing in the channel's box is
// how somebody says the wrong thing to the wrong people.
test('the thread keeps its own draft', async ({ window }) => {
  seedWithThread()
  await openChannel(window)
  await window.locator('.channel-replies').click()

  await window.locator('.channel-thread textarea').fill('half a reply')
  await expect(window.locator('.channel .composer textarea')).toHaveValue('')

  await window.locator('.channel-thread .channel-head button').click()
  await expect(window.locator('.channel-thread')).toHaveCount(0)
  await window.locator('.channel-replies').click()
  await expect(window.locator('.channel-thread textarea')).toHaveValue('half a reply')
})

test('closing the thread leaves the conversation', async ({ window }) => {
  seedWithThread()
  await openChannel(window)
  await window.locator('.channel-replies').click()
  await window.locator('.channel-thread .channel-head button').click()

  await expect(window.locator('.channel-thread')).toHaveCount(0)
  await expect(window.locator('.channel')).toBeVisible()
})

test('@ offers the members and inserts the handle', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID, BETA_ID] })
  await openChannel(window)

  await window.locator('.channel .composer textarea').fill('@')
  await expect(window.locator('.mention-option')).toHaveCount(2)

  // Narrowed as it is typed, against the handle and the title alike.
  await window.locator('.channel .composer textarea').fill('@be')
  await expect(window.locator('.mention-option')).toHaveCount(1)
  await window.locator('.mention-option').click()

  // The handle is what the daemon resolves, so that is what goes in the box.
  await expect(window.locator('.channel .composer textarea')).toHaveValue('@beta ')
})

test('a mention is a chip, and a mention inside code is not', async ({ window }) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID, BETA_ID],
    messages: [
      {
        author: 'user',
        from: 'user',
        body: 'ping @alpha — but `@alpha` in code is not an address',
      },
    ],
  })
  await openChannel(window)

  // One chip: the prose one. Agents paste code constantly, and decorating an
  // @ inside a snippet would make the chips worth nothing.
  await expect(window.locator('.mention-chip')).toHaveCount(1)
  await expect(window.locator('.mention-chip')).toHaveText('@alpha')
  await expect(window.locator('code')).toContainText('@alpha')
})

test('a message that names you is marked, and the row counts it apart', async ({ window }) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID],
    messages: [
      { author: `session:${ALPHA_ID}`, from: 'Alpha', body: 'general traffic' },
      {
        author: `session:${ALPHA_ID}`,
        from: 'Alpha',
        body: '@user can you look?',
        mentions: ['user'],
      },
    ],
  })
  await window.locator('.rail-item[aria-label="Channels"]').click()

  // The badge says somebody addressed you, which a count of traffic buries.
  await expect(window.locator('.badge.mention')).toHaveText('@1')

  await window.locator('.channel-row').first().click()
  await expect(window.locator('.channel-msg.addressed')).toHaveCount(1)
  await expect(window.locator('.channel-msg.addressed')).toContainText('can you look?')
})

// Four sessions with 48-character titles wrapped the header onto three lines
// and pushed the conversation down the screen, so the header carries a count
// and the names live in a panel.
test('the header counts the members and opens the list', async ({ window }) => {
  seedChannel({ id: 'ch_1', name: 'api-redesign', members: [ALPHA_ID, BETA_ID] })
  await openChannel(window)

  // Two sessions and the person.
  await expect(window.locator('.channel-members-toggle')).toHaveText('3 members')
  await expect(window.locator('.member-list')).toHaveCount(0)

  await window.locator('.channel-members-toggle').click()
  await expect(window.locator('.member-list .member-chip')).toHaveCount(3)
  // The handle belongs beside the name it stands for: it is what you type.
  await expect(window.locator('.member-list')).toContainText('@alpha')
  await expect(window.locator('.member-list')).toContainText('@user')
})

// One slot down the side. Two panels would leave the conversation a column
// wide, and nobody reads a thread and a roster at once.
test('the members list and a thread share the one panel', async ({ window }) => {
  seedWithThread()
  await openChannel(window)

  await window.locator('.channel-members-toggle').click()
  await expect(window.locator('.member-list')).toBeVisible()

  await window.locator('.channel-replies').click()
  await expect(window.locator('.member-list')).toHaveCount(0)
  await expect(window.locator('.channel-thread')).toContainText('which call sites?')
})
