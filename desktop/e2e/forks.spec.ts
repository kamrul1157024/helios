// Forks in the sidebar: nested under the session they came from, folded by a
// chevron on that session, and never draggable out of the family.
//
// See docs/specs/64-session-forking.md.
import { ALPHA as ALPHA_ID, BETA as BETA_ID, GAMMA as GAMMA_ID, forkSessionFrom } from './daemon.ts'
import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'
const BETA = 'Beta'
const GAMMA = 'Gamma'

function row(window: import('@playwright/test').Page, title: string) {
  return window.locator('.session-row', { hasText: title })
}

test('a fork renders under its parent, marked and indented', async ({ window }) => {
  forkSessionFrom(BETA_ID, ALPHA_ID)
  await window.reload()

  const beta = row(window, BETA)
  await expect(beta).toHaveClass(/forked/)
  // One column of tree line, and it closes: Beta is Alpha's only fork.
  await expect(beta.locator('.fork-guide')).toHaveCount(1)
  await expect(beta.locator('.fork-guide.elbow.last')).toHaveCount(1)

  // Alpha is the parent, so it carries the count and draws no line of its own.
  const alpha = row(window, ALPHA)
  await expect(alpha.locator('.fork-count')).toHaveText('1 ⑂')
  await expect(alpha.locator('.fork-guide')).toHaveCount(0)

  // Drawn directly after its parent rather than wherever activity put it.
  const titles = await window.locator('.session-row .row-title, .session-row .row-name').allInnerTexts()
  const order = titles.join('|')
  expect(order.indexOf(BETA)).toBeGreaterThan(order.indexOf(ALPHA))
})

test('the chevron folds the family and the count stays behind', async ({ window }) => {
  forkSessionFrom(BETA_ID, ALPHA_ID)
  await window.reload()

  await expect(row(window, BETA)).toBeVisible()
  await row(window, ALPHA).locator('.fork-chevron').click()

  await expect(row(window, BETA)).toHaveCount(0)
  // Folded, the count is the only thing saying the branch is still there.
  await expect(row(window, ALPHA).locator('.fork-count')).toHaveText('1 ⑂')

  await row(window, ALPHA).locator('.fork-chevron').click()
  await expect(row(window, BETA)).toBeVisible()
})

test('folding a branch hides what is under it and leaves its sibling alone', async ({ window }) => {
  forkSessionFrom(BETA_ID, ALPHA_ID, '2026-01-01T01:00:00Z')
  forkSessionFrom(GAMMA_ID, BETA_ID, '2026-01-01T02:00:00Z')
  await window.reload()

  // Gamma is two deep, so it draws two columns.
  await expect(row(window, GAMMA)).toBeVisible()
  await expect(row(window, GAMMA).locator('.fork-guide')).toHaveCount(2)

  await row(window, BETA).locator('.fork-chevron').click()

  await expect(row(window, GAMMA)).toHaveCount(0)
  await expect(row(window, BETA)).toBeVisible()
  await expect(row(window, ALPHA)).toBeVisible()
})

// A fork has no place in the host's hand-sorted order, so it must not offer to
// be dragged into one.
test('a fork row is not a drag handle', async ({ window }) => {
  forkSessionFrom(BETA_ID, ALPHA_ID)
  await window.reload()

  await expect(row(window, BETA)).not.toHaveClass(/movable/)
})

test('the fork dialog sends the branch it shows, and the daemon fills the rest', async ({
  window,
  daemon,
}) => {
  await row(window, ALPHA).click({ button: 'right' })
  await window.locator('.line-menu').first().getByText('Fork…').click()

  const dialog = window.locator('.fork-dialog')
  await expect(dialog).toBeVisible()
  // Prefilled from the session's own name, so the ordinary path is one click.
  await expect(dialog.locator('input').first()).toHaveValue('alpha')

  await dialog.locator('textarea').fill('Try it with a queue instead.')
  await dialog.getByRole('button', { name: 'Fork' }).click()

  await expect(dialog).toHaveCount(0)
  const forks = daemon.writes().filter((write) => write.kind === 'fork')
  expect(forks).toHaveLength(1)
  expect(forks[0]).toMatchObject({
    sessionId: ALPHA_ID,
    body: { workspace: 'worktree', branch: 'alpha', prompt: 'Try it with a queue instead.' },
  })
})

test('sharing the parent folder is behind a disclosure, and drops the branch', async ({
  window,
  daemon,
}) => {
  await row(window, ALPHA).click({ button: 'right' })
  await window.locator('.line-menu').first().getByText('Fork…').click()

  const dialog = window.locator('.fork-dialog')
  // Not on the face: putting it there invites a shared checkout by accident.
  await expect(dialog.locator('.fork-same')).toHaveCount(0)

  await dialog.locator('.fork-more').click()
  await dialog.locator('.fork-same input').check()
  // No worktree, so no branch to name.
  await expect(dialog.locator('input[placeholder="fork"]')).toHaveCount(0)

  await dialog.getByRole('button', { name: 'Fork' }).click()

  const forks = daemon.writes().filter((write) => write.kind === 'fork')
  expect(forks[0]).toMatchObject({ sessionId: ALPHA_ID, body: { workspace: 'same' } })
})
