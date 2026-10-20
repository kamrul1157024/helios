// What the agent changed (spec 65, B2 and B6): the Changes view, the marks in
// the tree, and the arrow on a tool row landing on the edit.
import type { Locator, Page } from '@playwright/test'

import { ALPHA as ALPHA_ID, REPO, setGitChanges, setTouched, withToolCalls } from './daemon.ts'
import { expect, test } from './fixtures.ts'
import { snap } from './screenshots.ts'

const ALPHA = 'Alpha'

function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

test.beforeEach(async ({ window }) => {
  await expect(window.locator('.session-row', { hasText: ALPHA })).toBeVisible()
})

test('the Changes view splits what this session wrote from everything else', async ({ window, daemon }) => {
  const now = new Date(Date.now() - 120_000).toISOString()
  setTouched(ALPHA_ID, [
    { path: `${REPO}/main.go`, last_at: now, tool: 'Edit', count: 2, line: 1 },
    { path: `${REPO}/old.go`, last_at: now, tool: 'Write', count: 1, deleted: true },
  ])
  setGitChanges({
    unstaged: [
      { path: 'main.go', status: 'M' },
      { path: 'desktop/index.ts', status: 'M' },
    ],
    untracked: [{ path: 'notes.txt', status: '??' }],
  })
  daemon.setFile(`${REPO}/main.go`, 'package main\n\nfunc main() {}\n')

  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: 'files' }).first().click()

  // The tree first: a letter on the changed file, a dot on the folder holding
  // one, and the agent's own mark.
  const tree = shown(window).locator('.tree')
  const main = tree.locator('.tree-row', { hasText: 'main.go' })
  await expect(main.locator('.tree-badge')).toHaveText('M')
  await expect(main.locator('.agent-mark')).toHaveAttribute('title', /Edited by this session, 2m ago/)
  await expect(tree.locator('.tree-row', { hasText: 'desktop' }).locator('.tree-dot')).toBeVisible()
  await snap(window, 'tree')

  await shown(window).locator('.ws-view', { hasText: 'Changes' }).click()
  const sections = shown(window).locator('.changes-section')
  await expect(sections.nth(0).locator('.change-row')).toHaveCount(2)
  await expect(sections.nth(0).locator('.change-row.deleted')).toContainText('old.go')
  await expect(sections.nth(1).locator('.change-row')).toHaveCount(2)
  await expect(sections.nth(1)).toContainText('index.ts')
  await expect(sections.nth(1)).toContainText('notes.txt')
  await snap(window, 'changes')

  await sections.nth(0).locator('.change-row', { hasText: 'main.go' }).click()
  await expect(shown(window).locator('.ws-tab.active')).toContainText('main.go')
})

test('a tool row opens its file at the line the edit wrote', async ({ window, daemon }) => {
  const lines = Array.from({ length: 80 }, (_, at) => `line ${at + 1}`)
  lines[59] = 'the agent wrote this'
  daemon.setFile(`${REPO}/big.go`, lines.join('\n'))
  withToolCalls(ALPHA_ID, [
    {
      tool: 'Edit',
      summary: 'big.go',
      input: { file_path: `${REPO}/big.go`, old_string: 'line 60', new_string: 'the agent wrote this' },
    },
  ])

  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.tool-open').first().click()

  await expect(shown(window).locator('.ws-tab.active')).toContainText('big.go')
  await expect(shown(window).locator('.cm-status')).toContainText('Ln 60')
  await expect(shown(window).locator('.cm-activeLine')).toContainText('the agent wrote this')
})

test('an agent write to a clean open tab tints the lines it changed', async ({ window, daemon }) => {
  daemon.setFile(`${REPO}/main.go`, 'package main\n\nfunc a() {}\n')
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: 'files' }).first().click()
  await shown(window).locator('.tree-row', { hasText: 'main.go' }).click()
  await expect(shown(window).locator('.cm-content')).toContainText('func a')

  daemon.setFile(`${REPO}/main.go`, 'package main\n\nfunc b() {}\n')
  daemon.emit('file_changed', { paths: [{ path: `${REPO}/main.go`, kind: 'file' }] })
  await expect(shown(window).locator('.cm-content')).toContainText('func b')
  await expect(shown(window).locator('.cm-flash')).toHaveCount(1)
  await expect(shown(window).locator('.cm-flash')).toContainText('func b')
})

test('the changed-on-disk bar can show what moved', async ({ window, daemon }) => {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: 'files' }).first().click()
  await shown(window).locator('.tree-row', { hasText: 'main.go' }).click()
  const editor = shown(window).locator('.cm-content')
  await editor.click()
  await window.keyboard.type('// mine ')
  daemon.setFile(`${REPO}/main.go`, 'package main // theirs\n')
  daemon.emit('file_changed', { paths: [{ path: `${REPO}/main.go`, kind: 'file' }] })

  await shown(window).locator('.ws-stale-bar button', { hasText: 'Show diff' }).click()
  const dialog = window.locator('.modal')
  await expect(dialog.locator('.diff-line.d-add', { hasText: 'theirs' })).toBeVisible()
  await expect(dialog.locator('.diff-line.d-del', { hasText: 'mine' })).toBeVisible()
})

// Highlighting runs in a worker now (spec 65, 2.6). The colours still arrive,
// and nothing on the way refuses to start the worker.
test('a patch is coloured by the highlighting worker', async ({ window }) => {
  const refused: string[] = []
  window.on('console', (message) => {
    if (message.type() === 'error' && /worker/i.test(message.text())) refused.push(message.text())
  })
  withToolCalls(ALPHA_ID, [
    {
      tool: 'Edit',
      summary: 'calc.go',
      input: { file_path: `${REPO}/calc.go`, old_string: 'x := 1', new_string: 'x := 42' },
    },
  ])
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.diff-line .hljs-number').first()).toBeVisible()
  expect(refused).toEqual([])
})
