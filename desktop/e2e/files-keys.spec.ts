// The files panel's keys (spec 65, F2, F3 and B5).
//
// Every panel stays mounted while hidden, so a key handler on the window hears
// presses meant for something else entirely. These pin down who owns which key
// and when: nothing for a hidden panel, ⌘B for the app sidebar alone, and the
// tab keys only while the panel has the keyboard.
import type { Locator, Page } from '@playwright/test'

import { REPO } from './daemon.ts'
import { expect, test } from './fixtures.ts'
import { snap } from './screenshots.ts'

const ALPHA = 'Alpha'

function shown(window: Page): Locator {
  return window.locator('.panel-keep:not([hidden])')
}

async function openPanel(window: Page, tab = 'files'): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await window.locator('.panel-tabs button', { hasText: tab }).first().click()
  await expect(shown(window)).toBeVisible()
}

async function openTree(window: Page): Promise<Locator> {
  await openPanel(window)
  const tree = shown(window).locator('.tree')
  await expect(tree.locator('.tree-row', { hasText: 'main.go' })).toBeVisible()
  return tree
}

test.beforeEach(async ({ window }) => {
  await expect(window.locator('.session-row', { hasText: ALPHA })).toBeVisible()
})

test('a hidden files panel saves nothing and opens nothing', async ({ window, daemon }) => {
  await openTree(window)
  await shown(window).locator('.tree-row', { hasText: 'main.go' }).click()
  const editor = shown(window).locator('.cm-content')
  await expect(editor).toContainText('package main')
  await editor.click()
  await window.keyboard.type('// unsaved')
  await expect(shown(window).locator('.ws-tab-close')).toHaveText('●')

  // Away to the transcript. The files panel is still mounted behind it.
  await window.locator('.panel-tabs button', { hasText: 'transcript' }).first().click()
  await expect(window.locator('.workspace:visible')).toHaveCount(0)
  // Wherever the keyboard ended up — the editor's own keymap included.
  await window.keyboard.press('Control+s')
  await window.keyboard.press('Control+p')

  await expect(window.locator('.quick')).toHaveCount(0)
  expect(daemon.writes().filter((w) => w.kind === 'save')).toHaveLength(0)
})

test('⌘B folds the app sidebar and leaves the explorer alone; ⌘⇧E folds the explorer', async ({ window }) => {
  const tree = await openTree(window)
  await tree.click()

  await window.keyboard.press('Control+b')
  await expect(window.locator('.sidebar')).toHaveCount(0)
  await expect(shown(window).locator('.ws-side')).toBeVisible()
  await window.keyboard.press('Control+b')
  await expect(window.locator('.sidebar')).toBeVisible()

  await window.keyboard.press('Control+Shift+E')
  await expect(shown(window).locator('.ws-side')).toBeHidden()
  await expect(window.locator('.sidebar')).toBeVisible()
  await window.keyboard.press('Control+Shift+E')
  await expect(shown(window).locator('.ws-side')).toBeVisible()
})

test('the tree is one focus stop, walked with the arrow keys', async ({ window }) => {
  const tree = await openTree(window)
  await tree.focus()

  // From nothing, down lands on the first row: build, then desktop.
  await window.keyboard.press('ArrowDown')
  await window.keyboard.press('ArrowDown')
  await expect(tree.locator('.tree-row.active')).toContainText('desktop')

  // Right opens the folder, right again steps into it, left goes back up.
  await window.keyboard.press('ArrowRight')
  await expect(tree.locator('.tree-row', { hasText: 'src' })).toBeVisible()
  await window.keyboard.press('ArrowRight')
  await expect(tree.locator('.tree-row.active')).toContainText('src')
  await window.keyboard.press('ArrowLeft')
  await expect(tree.locator('.tree-row.active')).toContainText('desktop')
  await window.keyboard.press('ArrowLeft')
  await expect(tree.locator('.tree-row', { hasText: 'src' })).toHaveCount(0)

  // Typing jumps to the next row with that name, and Enter opens it.
  await window.keyboard.type('ma')
  await expect(tree.locator('.tree-row.active')).toContainText('main.go')
  await window.keyboard.press('Enter')
  await expect(shown(window).locator('.ws-tab.active')).toContainText('main.go')

  // Ignored output is drawn dim, with the reason on hover.
  await expect(tree.locator('.tree-row.ignored', { hasText: 'build' })).toHaveAttribute('title', /Ignored by \.gitignore/)
  await snap(window, 'tree-keyboard')
})

test('a long folder draws only the rows near the view', async ({ window }) => {
  const tree = await openTree(window)
  await tree.locator('.tree-row', { hasText: 'docs' }).click()
  await tree.locator('.tree-row', { hasText: 'many' }).click()
  await expect(tree.locator('.tree-row', { hasText: 'note-000.md' })).toBeVisible()
  // Four hundred notes, and nowhere near that many rows in the document.
  expect(await tree.locator('.tree-row').count()).toBeLessThan(120)

  // Revealing one far down scrolls it into the middle of the view.
  await window.keyboard.press('Control+p')
  await window.locator('.quick-input').fill('note-350')
  await expect(window.locator('.quick-row').first()).toContainText('note-350.md')
  await window.keyboard.press('Enter')
  const row = tree.locator('.tree-row', { hasText: 'note-350.md' })
  await expect(row).toBeVisible()
  await expect(row).toBeInViewport()
})

test('⌃Tab walks the tabs, ⌘W closes one, and ⌥⇧T brings it back', async ({ window }) => {
  const tree = await openTree(window)
  for (const name of ['main.go', 'README.md']) await tree.locator('.tree-row', { hasText: name }).click()
  const tabs = shown(window).locator('.ws-tab')
  await expect(tabs).toHaveCount(2)
  await expect(shown(window).locator('.ws-tab.active')).toContainText('README.md')

  await tree.focus()
  await window.keyboard.press('Control+Tab')
  await expect(shown(window).locator('.ws-tab.active')).toContainText('main.go')
  await window.keyboard.press('Control+Shift+Tab')
  await expect(shown(window).locator('.ws-tab.active')).toContainText('README.md')

  // ⌘W from inside the panel closes the tab, not the session's terminal.
  await window.keyboard.press('Control+w')
  await expect(tabs).toHaveCount(1)
  await expect(shown(window).locator('.ws-tab.active')).toContainText('main.go')

  await window.keyboard.press('Alt+Shift+T')
  await expect(tabs).toHaveCount(2)
  await expect(shown(window).locator('.ws-tab.active')).toContainText('README.md')
})

test('two files of one name say which folder each is in', async ({ window }) => {
  const tree = await openTree(window)
  await tree.locator('.tree-row', { hasText: 'desktop' }).click()
  await tree.locator('.tree-row', { hasText: 'src' }).click()
  await tree.locator('.tree-row', { hasText: 'index.ts' }).first().click()
  await tree.locator('.tree-row', { hasText: 'index.ts' }).nth(1).click()
  const suffixes = shown(window).locator('.ws-tab-suffix')
  await expect(suffixes).toHaveCount(2)
  await expect(suffixes.nth(0)).toHaveText('src')
  await expect(suffixes.nth(1)).toHaveText('desktop')
  await expect(shown(window).locator('.ws-tab').first()).toHaveAttribute('title', `${REPO}/desktop/src/index.ts`)

  // Closing an unsaved tab asks in the app's own dialog.
  const editor = shown(window).locator('.cm-content')
  await editor.click()
  await window.keyboard.type('x')
  await shown(window).locator('.ws-tab.active .ws-tab-close').click()
  await expect(window.locator('.modal', { hasText: 'Discard unsaved changes?' })).toBeVisible()
  await window.locator('.modal button', { hasText: 'Cancel' }).click()
  await expect(shown(window).locator('.ws-tab')).toHaveCount(2)
  await snap(window, 'tabs')
})

// The editor has a keymap of its own, and the panel's keys must win inside it:
// ⌘⇧F used to open CodeMirror's find panel under Find in Files, because the
// editor reads it as its own ⌘F. None of these may run an editor command, or
// type into the buffer.
test('panel keys pressed in the editor do nothing to the editor', async ({ window }) => {
  const tree = await openTree(window)
  for (const name of ['README.md', 'main.go']) await tree.locator('.tree-row', { hasText: name }).click()
  const editor = shown(window).locator('.cm-content')
  await expect(editor).toContainText('package main')
  const before = await editor.textContent()

  const inEditor = async (): Promise<void> => {
    if ((await shown(window).locator('.ws-tab.active').textContent())?.includes('main.go') !== true) {
      await shown(window).locator('.ws-tab', { hasText: 'main.go' }).click()
    }
    await editor.click()
  }
  const unchanged = async (): Promise<void> => {
    await expect(shown(window).locator('.cm-panels .cm-search')).toHaveCount(0)
    await expect(shown(window).locator('.cm-panel.cm-search')).toHaveCount(0)
    expect(await editor.textContent()).toBe(before)
  }

  await inEditor()
  await window.keyboard.press('Control+Shift+F')
  await expect(shown(window).locator('.find-input').first()).toBeFocused()
  await unchanged()

  await inEditor()
  await window.keyboard.press('Control+p')
  await expect(window.locator('.quick')).toBeVisible()
  await window.keyboard.press('Escape')
  await unchanged()

  await inEditor()
  await window.keyboard.press('Control+Shift+E')
  await expect(shown(window).locator('.ws-side')).toBeHidden()
  await window.keyboard.press('Control+Shift+E')
  await unchanged()

  await inEditor()
  await window.keyboard.press('Alt+z')
  await unchanged()
  await window.keyboard.press('Alt+z')

  await inEditor()
  await window.keyboard.press('F7')
  await window.keyboard.press('Shift+F7')
  await unchanged()

  await inEditor()
  await window.keyboard.press('Control+Tab')
  await expect(shown(window).locator('.ws-tab.active')).toContainText('README.md')
  await inEditor()
  await window.keyboard.press('Control+Shift+Tab')
  await expect(shown(window).locator('.ws-tab.active')).toContainText('README.md')
  await inEditor()
  await unchanged()

  await window.keyboard.press('Control+w')
  await expect(shown(window).locator('.ws-tab')).toHaveCount(1)
  await window.keyboard.press('Alt+Shift+T')
  await expect(shown(window).locator('.ws-tab')).toHaveCount(2)
  await expect(shown(window).locator('.ws-tab.active')).toContainText('main.go')
  await expect(editor).toContainText('package main')
  await unchanged()
})
