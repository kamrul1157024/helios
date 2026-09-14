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

/*
A message body is rendered, not printed, and anything can post one.

Markdown is what agents are told to write, but raw HTML goes through the same
renderer, so the sanitiser is the only thing between a channel and a script tag
posted by whatever could reach the daemon. Worth a test: nobody would notice
this regressing until it mattered.
*/
test('a message renders its markup and cannot run anything', async ({ window }) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID],
    messages: [
      {
        author: `session:${ALPHA_ID}`,
        from: 'Alpha',
        body: '<table><tr><th>field</th><th>now</th></tr><tr><td>page</td><td><b>gone</b></td></tr></table>',
      },
      {
        author: `session:${ALPHA_ID}`,
        from: 'Alpha',
        body: 'markdown too:\n\n| field | now |\n| --- | --- |\n| items | array |',
      },
      {
        author: 'user',
        from: 'user',
        body: '<img src=x onerror="window.__xss=1"><script>window.__xss=2</script>after',
      },
    ],
  })
  await openChannel(window)

  // Both kinds render: the raw table and the markdown one.
  await expect(window.locator('.channel-msg table')).toHaveCount(2)
  await expect(window.locator('.channel-msg b')).toHaveText('gone')

  // And nothing ran.
  expect(await window.evaluate(() => (window as never as Record<string, unknown>).__xss)).toBeUndefined()
  await expect(window.locator('.channel-msg script')).toHaveCount(0)
  await expect(window.locator('.channel-msg').last()).toContainText('after')
})

/*
Colour an agent can use without seeing the theme.

Inline styles survive the sanitiser, so an agent can pick its own hex — chosen
blind, against a surface it cannot see. These classes map onto the status
palette instead, which is derived from the active theme and contrast-checked
against what it is drawn on, so the same markup is legible on every theme.
*/
test('the status classes take their colour from the theme', async ({ window }) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID],
    messages: [
      {
        author: `session:${ALPHA_ID}`,
        from: 'Alpha',
        body: '<span class="ok">ok</span> <span class="warn">slow</span> <span class="bad">failed</span> <span class="muted">aside</span>',
      },
    ],
  })
  await openChannel(window)

  const colourOf = (cls: string): Promise<string> =>
    window
      .locator(`.channel-msg-body .${cls}`)
      .evaluate((node) => getComputedStyle(node).color)

  const [ok, warn, bad, muted] = [
    await colourOf('ok'),
    await colourOf('warn'),
    await colourOf('bad'),
    await colourOf('muted'),
  ]

  // Four distinct colours, none of them the inherited body colour.
  expect(new Set([ok, warn, bad, muted]).size).toBe(4)
  const plain = await window
    .locator('.channel-msg-body')
    .evaluate((node) => getComputedStyle(node).color)
  expect(ok).not.toBe(plain)
  expect(bad).not.toBe(plain)
})

// The browser paints mark black-on-yellow, which is built for a white page and
// shouts on a dark one.
test('mark is toned to the surface rather than left browser-yellow', async ({ window }) => {
  seedChannel({
    id: 'ch_1',
    name: 'api-redesign',
    members: [ALPHA_ID],
    messages: [{ author: `session:${ALPHA_ID}`, from: 'Alpha', body: 'the <mark>page</mark> field' }],
  })
  await openChannel(window)

  const background = await window
    .locator('.channel-msg-body mark')
    .evaluate((node) => getComputedStyle(node).backgroundColor)
  expect(background).not.toBe('rgb(255, 255, 0)')
})
