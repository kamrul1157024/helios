// The session row in the sidebar, and the one control that ends the app.
//
// The row has two shapes. Compact is what an install starts with: one line a
// session, the agent and the status as glyphs at the head of the title. Roomy
// is the whole of it — title, then the directory it runs in, then the status,
// the same three lines the phone shows, in that order. These rules live in a
// component, and there is no component test framework here, so they are
// checked from the running app.
import type { Page } from '@playwright/test'

import { REPO } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

function row(window: Page) {
  return window.locator('.session-row', { hasText: ALPHA })
}

test.beforeEach(async ({ window }) => {
  await expect(row(window)).toBeVisible()
})

test('the row an install starts with is the agent, the status and the title', async ({ window }) => {
  const alpha = row(window)

  // The mark says which agent in a glyph. The stub's sessions are all Claude's;
  // the other branch is a Codex session, which this daemon does not serve.
  await expect(alpha.locator('.provider-mark')).toBeVisible()
  await expect(alpha.locator('.provider-mark')).toHaveAttribute('title', 'Claude Code')
  await expect(alpha.locator('.row-dot')).toBeVisible()

  // Everything the roomy row adds below the title is folded away, and the two
  // glyphs stand in for the status word among it.
  await expect(alpha.locator('.row-cwd')).toBeHidden()
  await expect(alpha.locator('.row-sub')).toBeHidden()
})

// The roomy row, which is a setting away rather than gone.
test.describe('roomy', () => {
  test.use({ density: 'comfortable' })

  test('a session row shows the directory it runs in, full path in the title', async ({ window }) => {
    const path = row(window).locator('.row-cwd')
    await expect(path).toBeVisible()
    await expect(path).toHaveText(REPO)
    // The visible text is shortened elsewhere; the title is always the whole path,
    // which is what a hover is for.
    await expect(path).toHaveAttribute('title', REPO)
  })

  test('the row reads title, then path, then status — the mobile order', async ({ window }) => {
    const alpha = row(window)
    const title = await alpha.locator('.row-title').boundingBox()
    const path = await alpha.locator('.row-cwd').boundingBox()
    const status = await alpha.locator('.row-status').boundingBox()

    // By vertical position rather than DOM order, because the order the user reads
    // is the one worth pinning: a later flex tweak could reorder them on screen
    // without touching the markup.
    expect(title && path && status).toBeTruthy()
    expect(title!.y).toBeLessThan(path!.y)
    expect(path!.y).toBeLessThan(status!.y)
  })

  // The glyphs the compact row leads with are drawn either way and hidden here,
  // because the lines below already say both in words.
  test('the agent mark and the status dot are not drawn twice', async ({ window }) => {
    await expect(row(window).locator('.provider-mark')).toBeHidden()
    await expect(row(window).locator('.row-dot')).toBeHidden()
  })
})

test('Quit Helios asks before it quits, and Cancel backs out', async ({ window }) => {
  // The footer's button, not the modal's — the modal has one of the same name,
  // and it does not exist yet.
  await window.locator('.sidebar-foot').getByRole('button', { name: 'Quit Helios' }).click()

  const modal = window.locator('.modal')
  await expect(modal).toBeVisible()
  await expect(modal.locator('.modal-head h2')).toHaveText('Quit Helios?')

  // Cancel, not confirm: confirming would call app.quit() and end the run.
  await modal.getByRole('button', { name: 'Cancel' }).click()
  await expect(modal).toHaveCount(0)

  // The window is still here — nothing quit behind the dialog.
  await expect(row(window)).toBeVisible()
})
