import { Notification as ElectronNotification, nativeImage, Tray, Menu, app } from 'electron'
import path from 'node:path'

import type { HostRegistry } from './hosts.ts'
import { isBlocking } from '../shared/notifications.ts'
import type { Hud } from './hud.ts'
import type { PrefsStore } from './prefs.ts'
import type { Notification } from '../shared/models.ts'

const SEEN_LIMIT = 500

export interface NotifyTarget {
  hostId: string
  notificationId: string
  sessionId: string
}

/**
 * Native notifications and the tray badge.
 *
 * The point of the desktop app over the phone is that an approval lands where
 * the user already is, so a pending permission raises an OS notification whose
 * click focuses the session that asked.
 */
export class Notifier {
  private tray: Tray | null = null
  private seen = new Set<string>()
  private pending = new Map<string, NotifyTarget>()
  /** Notifications still on screen, so one answered elsewhere can be taken down. */
  private shown = new Map<string, ElectronNotification>()

  constructor(
    private readonly hosts: HostRegistry,
    private readonly hud: Hud,
    private readonly prefs: PrefsStore,
    private readonly onActivate: (target: NotifyTarget) => void,
    private readonly onQuit: () => void,
  ) {}

  /** Called for every SSE `notification` event, from any host. */
  handleEvent(hostId: string, type: string, data: Record<string, unknown>): void {
    if (type === 'notification') {
      const notif = data as unknown as Notification
      if (notif.status && notif.status !== 'pending') return
      this.present(hostId, notif)
      return
    }
    if (type === 'notification_resolved') {
      const id = typeof data.id === 'string' ? data.id : ''
      if (id) this.resolve(hostId, id)
    }
  }

  /**
   * Called after a poll, so notifications raised while the app was down still
   * show — and, just as importantly, so ones answered while the app was down
   * stop showing. A reconnect can drop the `notification_resolved` event, and
   * without this the tray would keep counting approvals that no longer exist.
   *
   * [notifications] must be the host's full pending set, not a delta.
   */
  seed(hostId: string, notifications: Notification[]): void {
    const stillPending = new Set<string>()
    for (const notif of notifications) {
      if (notif.status !== 'pending') continue
      const key = `${hostId}:${notif.id}`
      stillPending.add(key)
      // Tracked here rather than left to present(): the badge counts what the
      // daemon says is pending, while `seen` only decides whether to raise a
      // banner. present() skips anything seen before, so an approval that has
      // already alerted once would otherwise never come back onto the tray.
      this.pending.set(key, { hostId, notificationId: notif.id, sessionId: notif.source_session })
      this.present(hostId, notif)
    }
    for (const key of [...this.pending.keys()]) {
      if (key.startsWith(`${hostId}:`) && !stillPending.has(key)) this.retract(key)
    }
    this.refreshTray()
  }

  private present(hostId: string, notif: Notification): void {
    const key = `${hostId}:${notif.id}`
    if (this.seen.has(key)) return
    this.remember(key)

    const target: NotifyTarget = {
      hostId,
      notificationId: notif.id,
      sessionId: notif.source_session,
    }
    this.pending.set(key, target)
    this.refreshTray()

    const hostName = this.hosts.get(hostId)?.record.name

    // Anything that blocks an agent goes to the HUD, which can answer it. A
    // banner for the same request would offer nothing the tray does not.
    if (isBlocking(notif.type)) {
      this.hud.present({ hostId, hostName, notification: notif })
      return
    }

    if (!ElectronNotification.isSupported()) return

    const suffix = hostName && !this.isOnlyHost() ? ` · ${hostName}` : ''
    const notification = new ElectronNotification({
      title: (notif.title ?? titleFor(notif.type)) + suffix,
      body: notif.detail ?? projectOf(notif.cwd),
      // Silencing a type buys quiet, not invisibility: the notification still
      // appears, and the tray still counts it.
      silent: !this.prefs.shouldSound(notif.type),
    })
    notification.on('click', () => this.onActivate(target))
    notification.on('close', () => this.shown.delete(key))
    this.shown.set(key, notification)
    notification.show()
  }

  private resolve(hostId: string, notificationId: string): void {
    if (this.retract(`${hostId}:${notificationId}`)) this.refreshTray()
  }

  /**
   * Takes a notification down: off the tray and off the screen. Leaving the
   * banner up offers an Approve button for a request that was already answered
   * on the phone or auto-approved here.
   */
  private retract(key: string): boolean {
    this.shown.get(key)?.close()
    this.shown.delete(key)
    this.hud.retract(key)
    return this.pending.delete(key)
  }

  /** Forgets a host's notifications when it is removed or goes offline for good. */
  clearHost(hostId: string): void {
    for (const key of [...this.pending.keys()]) {
      if (key.startsWith(`${hostId}:`)) this.retract(key)
    }
    this.refreshTray()
  }

  private remember(key: string): void {
    this.seen.add(key)
    if (this.seen.size > SEEN_LIMIT) {
      // Insertion-ordered, so the oldest is first. Bounded rather than exact:
      // the set only exists to suppress duplicates within a session.
      const oldest = this.seen.values().next().value
      if (oldest !== undefined) this.seen.delete(oldest)
    }
  }

  private isOnlyHost(): boolean {
    return this.hosts.list().length <= 1
  }

  // ─── Tray ──────────────────────────────────────────────────────────────

  installTray(iconDir: string): void {
    if (this.tray) return
    const image = nativeImage.createFromPath(path.join(iconDir, 'trayTemplate.png'))
    // A template image lets macOS invert it for dark/light menu bars; the flag
    // is ignored elsewhere.
    image.setTemplateImage(true)
    this.tray = new Tray(image.isEmpty() ? nativeImage.createEmpty() : image)
    this.tray.setToolTip('Helios')
    this.refreshTray()
  }

  private refreshTray(): void {
    const count = this.pending.size
    if (app.dock) app.dock.setBadge(count > 0 ? String(count) : '')
    if (!this.tray) return

    const items = [...this.pending.values()].slice(0, 10).map((target) => ({
      label: `Approval · ${this.hosts.get(target.hostId)?.record.name ?? target.hostId}`,
      click: () => this.onActivate(target),
    }))

    this.tray.setToolTip(count > 0 ? `Helios — ${count} waiting` : 'Helios')
    this.tray.setContextMenu(
      Menu.buildFromTemplate([
        { label: count > 0 ? `${count} waiting for you` : 'Nothing waiting', enabled: false },
        ...(items.length ? [{ type: 'separator' as const }, ...items] : []),
        { type: 'separator' },
        { label: 'Open Helios', click: () => this.onActivate({ hostId: '', notificationId: '', sessionId: '' }) },
        { label: 'Quit', click: () => this.onQuit() },
      ]),
    )
  }

  destroy(): void {
    this.tray?.destroy()
    this.tray = null
  }
}

function titleFor(type: string): string {
  switch (type) {
    case 'claude.permission':
      return 'Permission needed'
    case 'claude.question':
      return 'Claude has a question'
    case 'claude.trust':
      return 'Trust this folder?'
    case 'claude.error':
      return 'Session error'
    default:
      return 'Helios'
  }
}

function projectOf(cwd: string): string {
  if (!cwd) return ''
  const parts = cwd.split('/').filter(Boolean)
  return parts[parts.length - 1] ?? cwd
}
