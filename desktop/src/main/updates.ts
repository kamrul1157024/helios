import { app } from 'electron'
import fs from 'node:fs'
import path from 'node:path'

import type { UpdateInfo } from '../shared/models.ts'
import { isNewer } from '../shared/version.ts'

const REPO = 'kamrul1157024/helios'
const LATEST = `https://api.github.com/repos/${REPO}/releases/latest`

/**
 * Whether a newer release exists, and whether the user has already been told.
 *
 * There is no auto-updater here: the app ships as a DMG, an AppImage and a
 * deb, and none of them update themselves. What it can do is notice, once, and
 * point at the download — the mobile app does the same against the same
 * releases.
 */
export class UpdateChecker {
  private readonly file: string
  private dismissed = ''
  private checked: UpdateInfo | null = null

  constructor(userDataDir = app.getPath('userData')) {
    this.file = path.join(userDataDir, 'update-dismissed.json')
    try {
      const parsed = JSON.parse(fs.readFileSync(this.file, 'utf8')) as { version?: string }
      this.dismissed = parsed.version ?? ''
    } catch {
      this.dismissed = ''
    }
  }

  /**
   * Returns the release worth mentioning, or null. Asked once per launch and
   * remembered: this is a courtesy, not a service, and it should not cost a
   * request every time a component mounts.
   */
  async check(): Promise<UpdateInfo | null> {
    if (this.checked) return this.suppressDismissed(this.checked)

    try {
      const response = await fetch(LATEST, {
        headers: { Accept: 'application/vnd.github+json' },
        signal: AbortSignal.timeout(10_000),
      })
      if (!response.ok) return null

      const body = (await response.json()) as { tag_name?: string; html_url?: string }
      const latest = (body.tag_name ?? '').replace(/^v/, '')
      if (!latest || !isNewer(latest, app.getVersion())) return null

      this.checked = {
        version: latest,
        url: body.html_url ?? `https://github.com/${REPO}/releases/latest`,
      }
      return this.suppressDismissed(this.checked)
    } catch {
      // An update notice is not worth a word to the user when the network is
      // the thing that failed.
      return null
    }
  }

  /** Stops this version being mentioned again, on this machine. */
  dismiss(version: string): void {
    this.dismissed = version
    try {
      fs.writeFileSync(this.file, JSON.stringify({ version }), 'utf8')
    } catch {
      // Losing the record costs one more banner, which is not worth failing a
      // dismissal the user has already seen work.
    }
  }

  private suppressDismissed(info: UpdateInfo): UpdateInfo | null {
    return info.version === this.dismissed ? null : info
  }
}
