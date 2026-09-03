import { app } from 'electron'
import fs from 'node:fs'
import path from 'node:path'

import type { ReleaseNote, UpdateInfo } from '../shared/models.ts'
import { releasesSince } from '../shared/version.ts'

const REPO = 'kamrul1157024/helios'
/* The list rather than /releases/latest: a reader several versions behind is
   owed the notes for each release they skipped, and the newest is the first
   entry of this anyway. Thirty is a cap rather than a page to walk — twenty
   releases of history is not something anyone reads in a dialog. */
/* Overridable because the e2e suite launches a real app against a stub daemon:
   left pointing at GitHub, every run raises the release dialog over the window
   and every click in the suite lands on its backdrop. */
const RELEASES =
  process.env.HELIOS_RELEASES_URL ?? `https://api.github.com/repos/${REPO}/releases?per_page=30`

/** The fields of a GitHub release this reads. The rest of the payload is large
 *  and none of it is wanted. */
interface GitHubRelease {
  tag_name?: string
  html_url?: string
  body?: string
  published_at?: string
  draft?: boolean
  prerelease?: boolean
}

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
      const response = await fetch(RELEASES, {
        headers: { Accept: 'application/vnd.github+json' },
        signal: AbortSignal.timeout(10_000),
      })
      if (!response.ok) return null

      const payload = (await response.json()) as GitHubRelease[]
      // A draft is not published and a prerelease is not for everyone; neither
      // is news the reader can act on.
      const published = payload
        .filter((release) => !release.draft && !release.prerelease)
        .map(toNote)
        .filter((note) => note.version !== '')

      const notes = releasesSince(published, app.getVersion())
      const newest = notes[0]
      if (!newest) return null

      this.checked = { version: newest.version, url: newest.url, notes }
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
      // Losing the record costs one more notice, which is not worth failing a
      // dismissal the user has already seen work.
    }
  }

  private suppressDismissed(info: UpdateInfo): UpdateInfo | null {
    return info.version === this.dismissed ? null : info
  }
}

function toNote(release: GitHubRelease): ReleaseNote {
  return {
    version: (release.tag_name ?? '').replace(/^v/, ''),
    body: release.body?.trim() ?? '',
    url: release.html_url ?? `https://github.com/${REPO}/releases`,
    publishedAt: release.published_at ?? '',
  }
}
