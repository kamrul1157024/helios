// The host's remote desktop (spec 66, D7 and D9), against the stub daemon's
// desktop routes and its one-frame guacd. See e2e/desktop-stub.ts.
import type { Page } from '@playwright/test'

import { BACKGROUND, PICTURE, PICTURE_AT } from './desktop-stub.ts'
import { expect, test } from './fixtures.ts'
import { snap } from './screenshots.ts'

/** The Desktop link under the host, which is always on screen. */
async function openDesktop(window: Page): Promise<void> {
  await window.locator('.host-meta button', { hasText: 'Desktop' }).click()
  await expect(window.locator('.desktop-view')).toBeVisible()
}

/** One pixel of the remote screen, read from the canvas Guacamole draws into. */
async function pixel(window: Page, x: number, y: number): Promise<number[] | null> {
  return window.evaluate(
    ({ px, py }) => {
      const canvases = [...document.querySelectorAll<HTMLCanvasElement>('.desktop-stage canvas')]
      const screen = canvases.sort((a, b) => b.width - a.width)[0]
      if (!screen || screen.width <= px || screen.height <= py) return null
      const data = screen.getContext('2d')?.getImageData(px, py, 1, 1).data
      return data ? [data[0] ?? 0, data[1] ?? 0, data[2] ?? 0] : null
    },
    { px: x, py: y },
  )
}

async function expectDrawn(window: Page): Promise<void> {
  await expect(window.locator('.desktop-viewport')).toHaveAttribute('data-link', 'connected')
  await expect.poll(() => pixel(window, PICTURE_AT + 8, PICTURE_AT + 8)).toEqual([...PICTURE])
  await expect.poll(() => pixel(window, 300, 300)).toEqual([...BACKGROUND])
}

test('the desktop opens from the host menu and from the session header', async ({ window }) => {
  await window.locator('.host-head').first().click({ button: 'right' })
  await window.locator('.line-menu button', { hasText: /^Open desktop$/ }).click()
  await expect(window.locator('.desktop-view')).toBeVisible()
  await expect(window.locator('.desktop-title')).toHaveText('Desktop')

  await window.getByRole('button', { name: 'Close the desktop' }).click()
  await expect(window.locator('.desktop-view')).toHaveCount(0)

  await window.locator('.session-row', { hasText: 'Alpha' }).click()
  await window.getByRole('button', { name: 'Open the desktop' }).click()
  await expect(window.locator('.desktop-view')).toBeVisible()

  // Picking a session puts the desktop away again.
  await window.locator('.session-row', { hasText: 'Beta' }).click()
  await expect(window.locator('.desktop-view')).toHaveCount(0)
})

test('installing shows each step as it runs, then the screen', async ({ window, daemon }) => {
  daemon.desktop.setMissing()
  await openDesktop(window)

  await expect(window.getByText('This host has no desktop yet')).toBeVisible()
  await window.getByRole('button', { name: 'Install the desktop' }).click()

  await expect(window.getByText('Installing the desktop')).toBeVisible()
  const first = window.locator('.desktop-step[data-step="packages"]')
  await expect(first).toHaveClass(/running/)
  await expect(first).toContainText('Unpacking xorgxrdp')
  await expect(window.locator('.desktop-step.pending')).toHaveCount(5)
  expect(daemon.desktop.installs()).toBe(1)
  await snap(window, 'desktop-install')

  daemon.desktop.finishInstall()
  await expectDrawn(window)
})

test('without sudo the view shows the command to run', async ({ window, daemon }) => {
  daemon.desktop.setMissing()
  daemon.desktop.set({ can_install: false })
  await openDesktop(window)
  await expect(window.getByText('Helios needs root once to install the desktop')).toBeVisible()
  await expect(window.locator('.desktop-command code')).toHaveText('helios setup desktop')
})

test('a failed desktop says why and offers Retry', async ({ window, daemon }) => {
  daemon.desktop.set({ state: 'failed', error: 'Could not get the desktop service' })
  await openDesktop(window)
  await expect(window.getByText('Could not get the desktop service')).toBeVisible()
  await window.getByRole('button', { name: 'Retry' }).click()
  await expect(window.getByText('Installing the desktop')).toBeVisible()
})

test('a ready desktop draws the remote screen', async ({ window, daemon }) => {
  await openDesktop(window)
  await expectDrawn(window)

  // Fit is the default, so the first dial asks for the view's own size.
  const viewport = await window.locator('.desktop-viewport').boundingBox()
  const dialled = daemon.desktop.tunnels()[0]
  expect(dialled?.get('dpi')).toBe('96')
  expect(Math.abs(Number(dialled?.get('width')) - (viewport?.width ?? 0))).toBeLessThanOrEqual(2)
  expect(Math.abs(Number(dialled?.get('height')) - (viewport?.height ?? 0))).toBeLessThanOrEqual(1)
  await snap(window, 'desktop-ready')
})

test('the display menu resizes over the open tunnel and sets the DPI live', async ({ window, daemon }) => {
  await openDesktop(window)
  await expectDrawn(window)

  await window.getByRole('button', { name: 'Display settings' }).click()
  const menu = window.getByRole('dialog', { name: 'Display settings' })
  await menu.getByRole('button', { name: 'Fixed aspect' }).click()
  await menu.getByRole('button', { name: '16:9' }).click()
  await menu.getByRole('button', { name: '720', exact: true }).click()
  await expect
    .poll(() => daemon.desktop.received().filter(([opcode]) => opcode === 'size').at(-1))
    .toEqual(['size', '1280', '720'])
  await expect(window.locator('.desktop-viewport')).toHaveAttribute('data-remote', '1280x720')

  await menu.getByRole('button', { name: '150 %' }).click()
  await expect.poll(() => daemon.desktop.dpiWrites()).toEqual([144])
  await expect(menu.getByRole('button', { name: '150 %' })).toHaveAttribute('aria-pressed', 'true')

  // Out of range is clamped before it is sent.
  await menu.getByLabel('Custom DPI').fill('400')
  await menu.getByRole('button', { name: 'Set' }).click()
  await expect.poll(() => daemon.desktop.dpiWrites()).toEqual([144, 288])

  // One tunnel throughout: nothing here reconnects.
  expect(daemon.desktop.tunnels()).toHaveLength(1)
  await snap(window, 'desktop-display-menu')
})

test('a fixed aspect is letterboxed in the middle of the view', async ({ window }) => {
  await openDesktop(window)
  await expectDrawn(window)

  await window.getByRole('button', { name: 'Display settings' }).click()
  const menu = window.getByRole('dialog', { name: 'Display settings' })
  await menu.getByRole('button', { name: 'Fixed aspect' }).click()
  await menu.getByRole('button', { name: '4:3' }).click()
  await menu.getByRole('button', { name: '720', exact: true }).click()
  await window.keyboard.press('Escape')
  await expect(window.locator('.desktop-viewport')).toHaveAttribute('data-remote', '960x720')

  const view = await window.locator('.desktop-viewport').evaluate((el) => ({ width: el.clientWidth, height: el.clientHeight }))
  const scale = Math.min(view.width / 960, view.height / 720)
  const width = Math.round(960 * scale)
  const height = Math.round(720 * scale)
  const stage = await window.locator('.desktop-stage').evaluate((el) => ({
    left: (el as HTMLElement).offsetLeft,
    top: (el as HTMLElement).offsetTop,
    width: (el as HTMLElement).offsetWidth,
    height: (el as HTMLElement).offsetHeight,
  }))
  expect(stage).toEqual({
    left: Math.floor((view.width - width) / 2),
    top: Math.floor((view.height - height) / 2),
    width,
    height,
  })
  // Bars on one axis, whichever the window's shape leaves room on.
  expect(stage.left + stage.top).toBeGreaterThan(0)
  expect(Math.min(stage.left, stage.top)).toBe(0)
})

test('a refused ticket is replaced once, without the reader seeing it', async ({ window, daemon }) => {
  daemon.desktop.refuseNext(4401)
  await openDesktop(window)
  await expectDrawn(window)
  expect(daemon.desktop.tickets()).toBe(2)
  expect(daemon.desktop.tunnels()).toHaveLength(2)
})

test('too many viewers is said in words', async ({ window, daemon }) => {
  daemon.desktop.refuseNext(4429)
  await openDesktop(window)
  await expect(window.locator('.desktop-overlay')).toContainText('Too many viewers')
  await window.locator('.desktop-overlay').getByRole('button', { name: 'Retry' }).click()
  await expectDrawn(window)
})

test('the keyboard stays the app’s until the desktop is focused', async ({ window, daemon }) => {
  await openDesktop(window)
  await expectDrawn(window)
  const keys = (): string[][] => daemon.desktop.received().filter(([opcode]) => opcode === 'key')

  // Unfocused: typing goes nowhere near the desktop, and ⌘B is still the app's.
  await window.locator('.rail-fold').focus()
  await window.keyboard.press('a')
  await window.keyboard.press('ControlOrMeta+b')
  await expect(window.locator('.rail-fold')).toHaveAttribute('aria-expanded', 'false')
  expect(keys()).toEqual([])

  // Focused: the same key is sent as a keysym, down then up.
  await window.locator('.desktop-viewport').click()
  await window.keyboard.press('a')
  await expect.poll(keys).toEqual([
    ['key', '97', '1'],
    ['key', '97', '0'],
  ])
})

test('the desktop pops out into a window of its own', async ({ app, window }) => {
  await openDesktop(window)
  const [popped] = await Promise.all([app.waitForEvent('window'), window.getByRole('button', { name: 'Pop out' }).click()])
  await popped.waitForLoadState('domcontentloaded')

  await expectDrawn(popped)
  await expect(popped).toHaveTitle('Desktop — stub')
  // It moved rather than copied: the main window goes back to the session.
  await expect(window.locator('.desktop-view')).toHaveCount(0)
})

test('a dropped line says so and comes back by itself', async ({ window, daemon }) => {
  await openDesktop(window)
  await expectDrawn(window)

  daemon.desktop.drop()
  await expect(window.locator('.desktop-overlay')).toContainText('reconnecting')
  await expectDrawn(window)
  expect(daemon.desktop.tunnels()).toHaveLength(2)
})

test('clipboard text crosses both ways while the desktop has focus', async ({ app, window, daemon }) => {
  await openDesktop(window)
  await expectDrawn(window)

  // Local to remote, when the desktop takes focus.
  await app.evaluate(({ clipboard }) => clipboard.writeText('from the laptop'))
  await window.locator('.desktop-viewport').click()
  const sent = (): string[][] => daemon.desktop.received().filter(([opcode]) => opcode === 'clipboard' || opcode === 'blob')
  await expect.poll(sent).toEqual([
    ['clipboard', expect.any(String), 'text/plain'],
    ['blob', expect.any(String), Buffer.from('from the laptop').toString('base64')],
  ])

  // Remote to local, while it still has it.
  daemon.desktop.copyRemote('from the desktop')
  await expect.poll(() => app.evaluate(({ clipboard }) => clipboard.readText())).toBe('from the desktop')
})
