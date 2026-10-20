// One place for files and changes (spec 65, B8): the Git tab is gone, and the
// Files panel's Changes mode and its file tabs do its job.
import type { Locator, Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, addCommit, setGitChanges, setGitDiff } from './daemon.ts'
import { HOST_ID, expect, test } from './fixtures.ts'
import { snap } from './screenshots.ts'

const ALPHA = 'Alpha'

// Real `git diff` output, made by running git in a scratch repository, so the
// view meets the file headers a daemon sends — `diff --git`, `index`, `---`
// and `+++`, the mode lines — and not a tidied patch that never has them.

/** Two separate runs of changes, for F7 to step between. */
const TWO_RUNS = [
  'diff --git a/main.go b/main.go',
  'index 1db1290..05802af 100644',
  '--- a/main.go',
  '+++ b/main.go',
  '@@ -1,5 +1,5 @@',
  ' package main',
  '-func old() {}',
  '+func first() {}',
  ' ',
  ' // between',
  '-var x = 1',
  '+var second = 2',
].join('\n')

const DELETED = [
  'diff --git a/docs/old.md b/docs/old.md',
  'deleted file mode 100644',
  'index 286c5f5..0000000',
  '--- a/docs/old.md',
  '+++ /dev/null',
  '@@ -1 +0,0 @@',
  '-gone',
].join('\n')

const BINARY = [
  'diff --git a/logo.png b/logo.png',
  'index bdc955b..8835708 100644',
  'Binary files a/logo.png and b/logo.png differ',
].join('\n')

function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

async function openChanges(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: 'files' }).first().click()
  await shown(window).locator('.ws-view', { hasText: 'Changes' }).click()
}

test.beforeEach(async ({ window }) => {
  await expect(window.locator('.session-row', { hasText: ALPHA })).toBeVisible()
})

test('the panel bar has no Git tab', async ({ window }) => {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.panel-tabs button', { hasText: 'files' })).toHaveCount(1)
  await expect(window.locator('.panel-tabs button', { hasText: /^git$/ })).toHaveCount(0)
})

test('a layout saved with the Git panel in front opens Files on Changes', async ({ window }) => {
  await window.evaluate(
    ({ key }) =>
      localStorage.setItem(
        key,
        JSON.stringify({
          axis: 'row',
          focused: 'g1',
          groups: [{ id: 'g1', items: ['panel:chat', 'panel:git', 'panel:files'], active: 'panel:git', size: 1 }],
        }),
      ),
    { key: `helios.layout.${HOST_ID}.${ALPHA_ID}` },
  )
  await window.reload()
  await window.locator('.session-row', { hasText: ALPHA }).click()

  await expect(shown(window).locator('.workspace')).toBeVisible()
  await expect(shown(window).locator('.ws-view.on')).toHaveText('Changes')
  await expect(window.locator('.panel-tabs button', { hasText: /^git$/ })).toHaveCount(0)
})

test('the scope picker switches Changes to a commit and lists its files', async ({ window }) => {
  addCommit({
    sha: 'c0ffee1234567',
    subject: 'Teach the parser about tabs',
    files: [
      { path: 'desktop/src/app.tsx', status: 'M', insertions: 4, deletions: 1 },
      { path: 'docs/old.md', status: 'D', insertions: 0, deletions: 9 },
    ],
  })
  await openChanges(window)
  await expect(shown(window).locator('.scope-label')).toHaveText('Uncommitted changes')

  await shown(window).locator('.scope-button').click()
  await window.locator('.commit-row', { hasText: 'Teach the parser about tabs' }).click()

  await expect(shown(window).locator('.scope-label')).toHaveText('Teach the parser about tabs')
  const rows = shown(window).locator('.change-row')
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(0)).toContainText('app.tsx')
  await expect(rows.nth(1)).toHaveClass(/deleted/)
  await snap(window, 'changes-scope')

  // A file the commit deleted opens as its diff, with no Source to switch to.
  setGitDiff('docs/old.md', DELETED)
  await rows.nth(1).click()
  await expect(shown(window).locator('.ws-tab.active')).toContainText('old.md')
  await expect(shown(window).locator('.diff-line.d-del, .diff-cell.d-del').first()).toContainText('gone')
  await expect(shown(window).locator('.seg-toggle[aria-label="View"]')).toHaveCount(0)
  await expect(shown(window).locator('.file-diff')).not.toContainText('deleted file mode')
})

test('a changed file opens on its diff, and the toggle goes to Source and back', async ({ window }) => {
  setGitChanges({ unstaged: [{ path: 'main.go', status: 'M' }] })
  setGitDiff('main.go', TWO_RUNS)
  await openChanges(window)
  await shown(window).locator('.change-row', { hasText: 'main.go' }).click()

  const toggle = shown(window).locator('.seg-toggle[aria-label="View"]')
  await expect(toggle.locator('button', { hasText: 'Diff' })).toHaveAttribute('aria-pressed', 'true')
  await expect(shown(window).locator('.diff-cell.d-add').first()).toContainText('func first')
  await snap(window, 'diff-toggle')

  await toggle.locator('button', { hasText: 'Source' }).click()
  await expect(shown(window).locator('.cm-content')).toContainText('package main')
  await toggle.locator('button', { hasText: 'Diff' }).click()
  await expect(shown(window).locator('.diff-cell.d-add').first()).toContainText('func first')
  // One file, one tab, whichever view it is in.
  await expect(shown(window).locator('.ws-tab')).toHaveCount(1)
})

test('F7 and ⇧F7 step between the changes, in either layout', async ({ window }) => {
  setGitChanges({ unstaged: [{ path: 'main.go', status: 'M' }] })
  setGitDiff('main.go', TWO_RUNS)
  await openChanges(window)
  await shown(window).locator('.change-row', { hasText: 'main.go' }).click()
  await expect(shown(window).locator('.diff-cell.d-add').first()).toBeVisible()

  const current = shown(window).locator('.diff-row-current')
  await window.keyboard.press('F7')
  await expect(current).toContainText('func first')
  await window.keyboard.press('F7')
  await expect(current).toContainText('var second')
  await window.keyboard.press('Shift+F7')
  await expect(current).toContainText('func first')

  // The layout choice is kept, and the keys work the same in unified.
  await shown(window).locator('.seg-toggle[aria-label="Diff layout"] button', { hasText: 'Unified' }).click()
  await expect(shown(window).locator('.diff-unified')).toBeVisible()
  // A run is every row of it: in unified, the removed line and the added one.
  await window.keyboard.press('F7')
  await expect(current).toHaveCount(2)
  await expect(current.last()).toContainText('var second')
})

test('a review comment on diff lines goes to the agent', async ({ window, daemon }) => {
  setGitChanges({ unstaged: [{ path: 'main.go', status: 'M' }] })
  setGitDiff('main.go', TWO_RUNS)
  await openChanges(window)
  await shown(window).locator('.change-row', { hasText: 'main.go' }).click()

  await shown(window).locator('.diff-cell.d-add', { hasText: 'func first' }).click()
  await shown(window).locator('.diff-cell.d-add', { hasText: 'var second' }).click({ modifiers: ['Shift'] })
  const box = shown(window).locator('.review-comment textarea')
  await box.fill('Name it for what it does')
  await box.press('Enter')

  await expect.poll(() => daemon.writes().filter((w) => w.kind === 'send').length).toBe(1)
  const [sent] = daemon.writes().filter((w) => w.kind === 'send')
  expect(sent).toMatchObject({ sessionId: ALPHA_ID })
  expect(sent?.kind === 'send' && sent.message).toContain('Review comment on /repo/main.go')
  expect(sent?.kind === 'send' && sent.message).toContain('Name it for what it does')
})

// git's file header names the file and how it changed; none of it is a line of
// the file, and a binary file has nothing but the header.
test('file headers are not drawn as lines, and a binary change says so', async ({ window }) => {
  setGitChanges({ unstaged: [{ path: 'main.go', status: 'M' }, { path: 'logo.png', status: 'M' }] })
  setGitDiff('main.go', TWO_RUNS)
  setGitDiff('logo.png', BINARY)
  await openChanges(window)
  await shown(window).locator('.change-row', { hasText: 'main.go' }).click()
  const body = shown(window).locator('.file-diff')
  await expect(body.locator('.diff-cell.d-add').first()).toContainText('func first')
  for (const header of ['diff --git', 'index 1db1290', '--- a/main.go', '+++ b/main.go']) {
    await expect(body).not.toContainText(header)
  }

  await shown(window).locator('.change-row', { hasText: 'logo.png' }).click()
  await expect(shown(window).locator('.file-diff')).toContainText('Binary file changed')
  await expect(shown(window).locator('.file-diff')).not.toContainText('Binary files a/logo.png')
})

// Split means two columns, however narrow the pane; Unified means one.
test('split draws two columns and unified one', async ({ window }) => {
  setGitChanges({ unstaged: [{ path: 'main.go', status: 'M' }] })
  setGitDiff('main.go', TWO_RUNS)
  await openChanges(window)
  await shown(window).locator('.change-row', { hasText: 'main.go' }).click()

  const layout = shown(window).locator('.seg-toggle[aria-label="Diff layout"]')
  await expect(layout.locator('button.on')).toHaveText('Split')
  const changed = shown(window).locator('.diff-row:not(.diff-row-meta)').nth(1)
  const [left, right] = [changed.locator('.diff-cell').nth(0), changed.locator('.diff-cell').nth(1)]
  await expect(left).toContainText('func old')
  await expect(right).toContainText('func first')
  const [a, b] = [await left.boundingBox(), await right.boundingBox()]
  expect(a && b && b.x > a.x + a.width - 1 && Math.abs(a.y - b.y) < 2).toBe(true)

  await layout.locator('button', { hasText: 'Unified' }).click()
  await expect(layout.locator('button.on')).toHaveText('Unified')
  await expect(shown(window).locator('.file-diff .diff-split')).toHaveCount(0)
  const removed = shown(window).locator('.diff-line.d-del', { hasText: 'func old' })
  const added = shown(window).locator('.diff-line.d-add', { hasText: 'func first' })
  const [r, n] = [await removed.boundingBox(), await added.boundingBox()]
  expect(r && n && n.y > r.y && Math.abs(r.x - n.x) < 2).toBe(true)
})
