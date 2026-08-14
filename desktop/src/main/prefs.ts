import { app } from 'electron'
import fs from 'node:fs'
import path from 'node:path'

import { DEFAULT_PREFS, shouldSound } from '../shared/notifications.ts'
import type { NotificationPrefs } from '../shared/models.ts'

/**
 * Notification preferences for this machine.
 *
 * Device-local rather than read from the daemon: whether this laptop makes a
 * noise is a property of this laptop, and a user with three hosts should not
 * have to answer the question three times. The phone stores its copy the same
 * way.
 */
export class PrefsStore {
  private readonly file: string
  private prefs: NotificationPrefs = clone(DEFAULT_PREFS)

  constructor(userDataDir = app.getPath('userData')) {
    this.file = path.join(userDataDir, 'notification-prefs.json')
  }

  load(): void {
    try {
      const parsed = JSON.parse(fs.readFileSync(this.file, 'utf8')) as Partial<NotificationPrefs>
      this.prefs = {
        sound: parsed.sound ?? DEFAULT_PREFS.sound,
        // Merged over the defaults so a type added in a later version arrives
        // switched on rather than missing.
        alerts: { ...DEFAULT_PREFS.alerts, ...(parsed.alerts ?? {}) },
      }
    } catch {
      this.prefs = clone(DEFAULT_PREFS)
    }
  }

  get(): NotificationPrefs {
    return clone(this.prefs)
  }

  shouldSound(type: string): boolean {
    return shouldSound(this.prefs, type)
  }

  setSound(enabled: boolean): NotificationPrefs {
    this.prefs.sound = enabled
    this.persist()
    return this.get()
  }

  setAlert(type: string, enabled: boolean): NotificationPrefs {
    this.prefs.alerts[type] = enabled
    this.persist()
    return this.get()
  }

  reset(): NotificationPrefs {
    this.prefs = clone(DEFAULT_PREFS)
    this.persist()
    return this.get()
  }

  private persist(): void {
    fs.mkdirSync(path.dirname(this.file), { recursive: true })
    fs.writeFileSync(this.file, JSON.stringify(this.prefs, null, 2))
  }
}

function clone(prefs: NotificationPrefs): NotificationPrefs {
  return { sound: prefs.sound, alerts: { ...prefs.alerts } }
}
