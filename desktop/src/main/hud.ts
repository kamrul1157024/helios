import { BrowserWindow, app, ipcMain, screen } from 'electron'
import path from 'node:path'

import type { Notification } from '../shared/models.ts'
import type { NotifyTarget } from './notify.ts'

const WIDTH = 400
const INSET = 12
/** Past this the stack scrolls rather than the window growing off the screen. */
const MAX_HEIGHT = 720

export interface HudCard {
  hostId: string
  hostName?: string
  notification: Notification
}

/**
 * The window that answers a request where it lands.
 *
 * A native notification can only announce: Electron's action buttons are macOS
 * only and want a signed app, and this one ships unsigned. So the app draws its
 * own surface, which also buys room for the command being approved and the same
 * controls the phone has.
 *
 * It appears without taking focus — an approval arriving mid-keystroke must not
 * eat the keystroke — which is also why the keyboard shortcuts live behind a
 * global accelerator rather than assuming the window is listening.
 */
export class Hud {
  private window: BrowserWindow | null = null
  private readonly cards = new Map<string, HudCard>()

  constructor(
    private readonly rendererDir: string,
    private readonly preload: string,
    private readonly onActivate: (target: NotifyTarget) => void,
  ) {
    ipcMain.on('hud:resize', (_event, height: number) => this.resize(height))
    ipcMain.on('hud:dismiss', () => this.hide())
    ipcMain.on('hud:activate', (_event, target: NotifyTarget) => {
      this.onActivate(target)
      this.hide()
    })
    ipcMain.on('hud:resolved', (_event, key: string) => this.retract(key))
  }

  present(card: HudCard): void {
    this.cards.set(keyOf(card.hostId, card.notification.id), card)
    const window = this.ensureWindow()
    const send = (): void => window.webContents.send('hud:present', card)
    if (window.webContents.isLoading()) window.webContents.once('did-finish-load', send)
    else send()
    // Shown by resize(), once there is something to look at: showing here puts
    // an empty 10px sliver on screen for a frame while the card renders.
  }

  retract(key: string): void {
    if (!this.cards.delete(key)) return
    this.window?.webContents.send('hud:retract', key)
    if (this.cards.size === 0) this.hide()
  }

  retractHost(hostId: string): void {
    for (const key of [...this.cards.keys()]) {
      if (key.startsWith(`${hostId}:`)) this.retract(key)
    }
  }

  /**
   * Brings the HUD forward and hands it the keyboard, for the global shortcut.
   *
   * `window.focus()` on its own is not enough to pull a window out from under
   * whatever the user is in; the app has to be told to take the front first.
   */
  focus(): void {
    if (this.cards.size === 0) return
    const window = this.ensureWindow()
    if (!window.isVisible()) window.showInactive()
    app.focus({ steal: true })
    window.focus()
  }

  hide(): void {
    this.window?.hide()
  }

  destroy(): void {
    this.window?.destroy()
    this.window = null
  }

  private ensureWindow(): BrowserWindow {
    if (this.window && !this.window.isDestroyed()) return this.window

    const window = new BrowserWindow({
      ...this.anchor(200),
      show: false,
      frame: false,
      transparent: true,
      // The cards draw their own shadow; a window shadow would outline the
      // transparent region instead of the cards.
      hasShadow: false,
      resizable: false,
      movable: false,
      minimizable: false,
      maximizable: false,
      fullscreenable: false,
      skipTaskbar: true,
      // The click that brings the HUD forward should also press the button
      // under the pointer, rather than being spent on focus.
      acceptFirstMouse: true,
      webPreferences: {
        preload: this.preload,
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        spellcheck: false,
      },
    })

    window.setAlwaysOnTop(true, 'screen-saver')
    window.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true })
    window.on('closed', () => {
      this.window = null
    })
    void window.loadFile(path.join(this.rendererDir, 'hud.html'))

    this.window = window
    return window
  }

  private resize(contentHeight: number): void {
    if (!this.window || this.window.isDestroyed()) return
    this.window.setBounds(this.anchor(Math.min(Math.ceil(contentHeight), MAX_HEIGHT)))
    if (this.cards.size > 0 && !this.window.isVisible()) this.window.showInactive()
  }

  /**
   * Top-right of the work area of the display the cursor is on, which is the
   * screen the user is looking at and already clears the menu bar.
   */
  private anchor(height: number): { x: number; y: number; width: number; height: number } {
    const { workArea } = screen.getDisplayNearestPoint(screen.getCursorScreenPoint())
    return {
      x: workArea.x + workArea.width - WIDTH - INSET,
      y: workArea.y + INSET,
      width: WIDTH,
      height,
    }
  }
}

export function keyOf(hostId: string, notificationId: string): string {
  return `${hostId}:${notificationId}`
}
