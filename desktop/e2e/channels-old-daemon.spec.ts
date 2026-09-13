/*
A daemon older than channels has no such route and answers 404.

Not hypothetical: a fleet where one paired machine is a release or two behind
is the normal state, and that is exactly what happened the first time this was
run for real — a remote host on an older build 404'd every channel read. The
404 must cost the reader nothing. That host simply has no channels, the way it
already has no session groups.

Its own file because the stub has to be told to refuse *before* the app
launches: the window fixture is built before the first hook runs, and a
successful read cached at boot would still be fresh when the test asked again.
That is what the option is for.
*/
import { resetChannels } from './daemon.ts'
import { expect, test } from './fixtures.ts'

test.use({ channelsSupported: false })

test.beforeEach(() => {
  resetChannels()
})

test('the channels mode is empty rather than broken', async ({ window }) => {
  await window.locator('.rail-item[aria-label="Channels"]').click()

  await expect(window.locator('.channel-row')).toHaveCount(0)
  await expect(window.locator('.channel-closed-head')).toHaveCount(0)
  // The panel says there is nothing to show, not that something failed.
  await expect(window.getByText('Pick a channel, or start one from a few sessions.')).toBeVisible()
})

test('the picker says why it cannot start one here', async ({ window, daemon }) => {
  await window.locator('.session-row').first().click({ button: 'right' })
  await window.getByText('Add to channel…').click()
  await window.keyboard.type('orders-migration')

  // Offering to create would be offering a button that fails.
  await expect(window.locator('.quick-list')).toContainText('without channels')
  await expect(window.locator('.quick-row.new-channel')).toHaveCount(0)

  await window.keyboard.press('Enter')
  expect(daemon.writes().filter((write) => write.kind === 'channel')).toHaveLength(0)
})
