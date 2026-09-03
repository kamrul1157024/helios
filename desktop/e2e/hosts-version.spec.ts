// What each daemon is running, in Settings ▸ Hosts.
//
// Updating the desktop is half the job: the daemon runs the sessions and is
// updated on its own machine. The pane can only say which ones are behind if
// the version survives the trip from /api/health through the main process and
// onto the host's status, which is what this covers.
import { expect, test } from './fixtures.ts'

test('the hosts pane shows the version each daemon reports', async ({ window }) => {
  await window.locator('.rail-item[aria-label="Settings"]').click()
  await window.locator('.settings-nav button', { hasText: 'Hosts' }).click()

  const row = window.locator('.host-row').first()
  await expect(row).toBeVisible()
  await expect(row).toContainText('v9.9.9')

  // The suite pins the release list to empty, so nothing can be behind — and
  // a pane that marked a host anyway would be marking it on no evidence.
  await expect(window.locator('.host-behind')).toHaveCount(0)
})
