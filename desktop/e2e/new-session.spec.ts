import type { Page } from '@playwright/test'

import { CREATED } from './daemon.ts'
import { expect, test } from './fixtures.ts'

async function openComposer(window: Page): Promise<void> {
  await window.locator('button[aria-label="New session"]').first().click()
  await expect(window.locator('.composer')).toBeVisible()
  await expect(window.locator('.composer-place')).toContainText('repo')
  await expect(window.locator('.composer-foot')).toContainText('Default permissions')
}

async function openChip(window: Page, name: RegExp): Promise<void> {
  await window.locator('.composer-foot .picker > summary').filter({ hasText: name }).click()
  await expect(window.locator('.picker[open] .picker-body')).toBeVisible()
}

async function openPlace(window: Page): Promise<void> {
  await window.locator('.composer-place > summary').click()
  await expect(window.locator('.composer-place .picker-body')).toBeVisible()
}

async function backdrop(window: Page): Promise<{ x: number; y: number }> {
  const box = await window.locator('.composer').boundingBox()
  if (!box) throw new Error('composer has no box')
  return { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height + 120) }
}

test('every chip opens on its own value', async ({ window }) => {
  await openComposer(window)

  await expect(window.locator('.composer-place')).toContainText('repo')
  await expect(window.locator('.composer-host')).toContainText('stub')
  await expect(window.locator('.composer-foot')).toContainText('Claude')
  await expect(window.locator('.composer-foot')).toContainText('Default model')
})

test('picking a model puts it on the chip', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  await window.locator('.composer-option', { hasText: 'Sonnet' }).click()

  await expect(window.locator('.picker[open]')).toHaveCount(0)
  await expect(window.locator('.composer-foot')).toContainText('Sonnet')
})

test('dismissing a chip does not take the dialog with it', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  const at = await backdrop(window)
  await window.mouse.click(at.x, at.y)

  await expect(window.locator('.picker[open]')).toHaveCount(0)
  await expect(window.locator('.composer')).toBeVisible()
})

test('the backdrop still closes an untouched dialog', async ({ window }) => {
  await openComposer(window)

  const at = await backdrop(window)
  await window.mouse.click(at.x, at.y)

  await expect(window.locator('.composer')).toHaveCount(0)
})

test('a written prompt survives a stray click on the backdrop', async ({ window }) => {
  await openComposer(window)
  await window.locator('.composer-prompt').fill('something worth keeping')

  const at = await backdrop(window)
  await window.mouse.click(at.x, at.y)

  await expect(window.locator('.composer-prompt')).toHaveValue('something worth keeping')
})

test('escape closes the chip first and the dialog second', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  await window.keyboard.press('Escape')
  await expect(window.locator('.picker[open]')).toHaveCount(0)
  await expect(window.locator('.composer')).toBeVisible()

  await window.keyboard.press('Escape')
  await expect(window.locator('.composer')).toHaveCount(0)
})

test('arrows walk the options and wrap', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  const options = window.locator('.picker[open] .composer-option')
  await expect(options).toHaveCount(3)

  await window.keyboard.press('ArrowDown')
  await expect(options.nth(0)).toBeFocused()

  await window.keyboard.press('ArrowDown')
  await expect(options.nth(1)).toBeFocused()

  await window.keyboard.press('End')
  await expect(options.nth(2)).toBeFocused()

  await window.keyboard.press('ArrowDown')
  await expect(options.nth(0)).toBeFocused()

  await window.keyboard.press('ArrowUp')
  await expect(options.nth(2)).toBeFocused()
})

test('escape hands focus back to the chip', async ({ window }) => {
  await openComposer(window)
  await openChip(window, /model/i)

  await window.keyboard.press('Escape')

  const summary = window.locator('.composer-foot .picker > summary').filter({ hasText: /model/i })
  await expect(summary).toBeFocused()
})

test('the directory filter narrows the list and takes a typed path', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  const search = window.locator('.picker-search')
  await expect(search).toBeFocused()

  await search.fill('nothing-matches-this')
  await expect(window.locator('.picker-empty')).toBeVisible()

  await search.fill('/tmp/elsewhere')
  await window.locator('.composer-option', { hasText: 'Use' }).click()

  await expect(window.locator('.composer-place')).toContainText('elsewhere')
})

// The picker's whole job, and the reason #154 was reverted: a directory that
// has never had a session in it is not in the recents, so the only way to it is
// the one the list cannot offer.
//
// It reaches the daemon absolute. `resolveCWD` rejects anything else with a
// 400 (internal/server/cwd.go), so committing the typed text as it stands would
// be an escape hatch that only works for the paths that did not need one.
test('a relative path is committable, and lands under the directory in the chip', async ({
  window,
  daemon,
}) => {
  await openComposer(window)
  await openPlace(window)

  await window.locator('.picker-search').fill('workspace/acme-mobile')
  await window.locator('.composer-option', { hasText: 'Use' }).click()
  await expect(window.locator('.composer-place')).toContainText('acme-mobile')

  await window.locator('.composer-prompt').fill('start here')
  await window.locator('.composer .filled').click()

  await expect.poll(() => daemon.writes().map((write) => write.kind)).toEqual(['create'])
  const [created] = daemon.writes()
  if (created?.kind !== 'create') throw new Error('the daemon recorded something other than a create')
  expect(created.spec.cwd).toBe('/repo/workspace/acme-mobile')
})

// The complaint the fix is for: the chip says one directory and the typing used
// to be answered from another. A bare segment is relative to what the chip
// names, the way it would be in a shell sitting there.
test('a bare name completes inside the directory the chip names', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  await window.locator('.picker-search').fill('desk')
  await expect(window.locator('.picker-section', { hasText: 'In /repo' })).toBeVisible()

  const offered = window.locator('.picker-list .composer-option', { hasText: 'desktop' })
  await expect(offered).toHaveCount(1)
  await expect(offered).toContainText('/repo/desktop')

  // And the escape hatch above it says where it would land, rather than leaving
  // the reader to guess which directory three letters are relative to.
  await expect(window.locator('.composer-option.use-typed')).toContainText('/repo/desk')

  await offered.click()
  await expect(window.locator('.composer-place')).toContainText('desktop')
})

test('enter commits what is typed, with nothing under it agreeing', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  await window.locator('.picker-search').fill('srv/deploys/tonight')
  await window.keyboard.press('Enter')

  await expect(window.locator('.picker[open]')).toHaveCount(0)
  await expect(window.locator('.composer-place')).toContainText('tonight')
})

test('typing completes against the filesystem, not against the recents', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  // `work` is in no recent — the stub daemon has one session directory, /repo —
  // so every row here came from a directory listing. Spelled from home, because
  // a bare segment is relative to /repo, which is the chip's directory.
  await window.locator('.picker-search').fill('~/work')
  const completions = window.locator('.picker-list .composer-option', { hasText: /workspace|worktrees/ })
  await expect(completions).toHaveCount(2)
  await expect(window.locator('.picker-section', { hasText: 'In /home/dev' })).toBeVisible()

  await completions.first().click()
  await expect(window.locator('.composer-place')).toContainText('workspace')
})

test('completion walks down into a directory it just offered', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  await window.locator('.picker-search').fill('/home/dev/workspace/acme-')
  await expect(window.locator('.picker-list .composer-option', { hasText: 'acme-api' })).toBeVisible()
  await expect(window.locator('.picker-list .composer-option', { hasText: 'acme-web' })).toBeVisible()
})

test('tab fills in the top completion', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  const search = window.locator('.picker-search')
  await search.fill('~/worksp')
  await expect(window.locator('.picker-list .composer-option', { hasText: 'workspace' })).toBeVisible()
  await search.press('Tab')

  await expect(search).toHaveValue('/home/dev/workspace/')
  await expect(window.locator('.picker-list .composer-option', { hasText: 'acme-api' })).toBeVisible()
})

test('a half-typed path is still there when the chip is reopened', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  await window.locator('.picker-search').fill('/home/dev/works')
  await window.keyboard.press('Escape')
  await expect(window.locator('.picker[open]')).toHaveCount(0)

  await openPlace(window)
  await expect(window.locator('.picker-search')).toHaveValue('/home/dev/works')
})

test('home does not disappear while it is being typed', async ({ window }) => {
  await openComposer(window)
  await openPlace(window)

  await window.locator('.picker-search').fill('hom')
  await expect(window.locator('.composer-option', { hasText: 'Home' })).toBeVisible()
})

// The one part of the composer that is not the chat composer with different
// chips. An upload needs a session to belong to, and the session does not
// exist until Create is pressed — so the first turn cannot be the one the
// agent launches with, and the order below is the whole of the feature.
test('files go up once the session exists, and the first prompt names them', async ({ window, daemon }) => {
  await openComposer(window)

  await window.locator('.composer input[type="file"]').setInputFiles({
    name: 'shot.png',
    mimeType: 'image/png',
    buffer: Buffer.from('not really a png'),
  })
  await expect(window.locator('.attachment-name')).toHaveText('shot.png')

  await window.locator('.composer-prompt').fill('what is wrong with this')
  await window.locator('.composer .filled').click()
  await expect(window.locator('.composer')).toHaveCount(0)

  await expect
    .poll(() => daemon.writes().map((write) => write.kind))
    .toEqual(['create', 'upload', 'send'])

  const [created, uploaded, sent] = daemon.writes()
  if (created?.kind !== 'create' || uploaded?.kind !== 'upload' || sent?.kind !== 'send') {
    throw new Error('the daemon recorded something other than a create, an upload and a send')
  }
  // Silent on purpose: launching with the prompt would send the agent looking
  // for paths that are only decided by the upload below it.
  expect(created.spec.prompt).toBeUndefined()
  expect(uploaded.names).toEqual(['shot.png'])
  expect(uploaded.sessionId).toBe(CREATED)
  expect(sent.message).toContain(`/home/dev/.helios/uploads/${CREATED}/shot.png`)
  expect(sent.message).toContain('what is wrong with this')
})

test('a session with nothing attached still launches with its prompt', async ({ window, daemon }) => {
  await openComposer(window)

  await window.locator('.composer-prompt').fill('just get on with it')
  await window.locator('.composer .filled').click()
  await expect(window.locator('.composer')).toHaveCount(0)

  await expect.poll(() => daemon.writes().map((write) => write.kind)).toEqual(['create'])
  const [created] = daemon.writes()
  if (created?.kind !== 'create') throw new Error('the daemon recorded something other than a create')
  expect(created.spec.prompt).toBe('just get on with it')
})

test('a file dropped anywhere on the dialog lands on it', async ({ window }) => {
  await openComposer(window)

  await window.evaluate(() => {
    const carried = new DataTransfer()
    carried.items.add(new File(['a stack trace'], 'trace.txt', { type: 'text/plain' }))
    const composer = document.querySelector('.composer')
    if (!composer) throw new Error('no composer to drop on')
    composer.dispatchEvent(new DragEvent('dragover', { dataTransfer: carried, bubbles: true }))
    composer.dispatchEvent(new DragEvent('drop', { dataTransfer: carried, bubbles: true }))
  })

  await expect(window.locator('.attachment-name')).toHaveText('trace.txt')
})

test('⌘U reaches for a file without touching the paperclip', async ({ window }) => {
  await openComposer(window)

  const chooser = window.waitForEvent('filechooser')
  await window.keyboard.press('Meta+u')
  await (await chooser).setFiles({
    name: 'note.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('a note'),
  })

  await expect(window.locator('.attachment-name')).toHaveText('note.txt')
})
