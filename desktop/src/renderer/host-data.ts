import { useQueries } from '@tanstack/react-query'

import { sortModeOf, type SessionListPage, type SettingsDocument, type SortMode } from './keys.ts'
import {
  groupsQuery,
  jobSessionsQuery,
  notificationsQuery,
  sessionsQuery,
  settingsQuery,
} from './queries.ts'
import { statusOf } from './errors.ts'
import { useStore } from './store.ts'
import type { HostStats, Notification, Session, SessionGroup } from '../shared/models.ts'

/**
 * Every host's lists, in the shape the sidebar has always read them.
 *
 * The store used to hold these and refetch them by hand. They are queries now,
 * but the views still want "all hosts at once" rather than one hook per host —
 * the sidebar draws them as one list — so this is where the fan-out lives.
 *
 * Keyed by host id throughout, because a session id is only unique within the
 * daemon that issued it.
 */

export interface HostSessions {
  sessions: Record<string, Session[]>
  /** Each host's warm pool and machine load, from the session list envelope. */
  stats: Record<string, HostStats>
  /**
   * Hosts that have not answered yet, which is not the same as having nothing.
   * Without the distinction a daemon that is slow to answer looks like one with
   * no sessions on it, and the sidebar draws the wrong empty state.
   */
  pending: Record<string, boolean>
}

export function useHostSessions(): HostSessions {
  const hosts = useStore((s) => s.hosts)
  // Asking the daemon to resolve groups costs a lookup it should not do for a
  // client that will not render them, so the flag rides on the fetch.
  const grouped = useStore((s) => s.grouping === 'manual')
  const results = useQueries({ queries: hosts.map((host) => sessionsQuery(host.id, grouped)) })

  const sessions: Record<string, Session[]> = {}
  const stats: Record<string, HostStats> = {}
  const pending: Record<string, boolean> = {}
  hosts.forEach((host, index) => {
    const result = results[index]
    if (!result) return
    pending[host.id] = result.isPending
    const page: SessionListPage | undefined = result.data
    if (!page) return
    sessions[host.id] = page.sessions
    if (page.host) stats[host.id] = page.host
  })
  return { sessions, stats, pending }
}

/**
 * The sessions a schedule started, per host.
 *
 * Kept out of `useHostSessions` rather than filtered from it: the daemon
 * answers the two lists separately, and the sidebar draws them as two sections.
 */
export function useHostJobSessions(): Record<string, Session[]> {
  const hosts = useStore((s) => s.hosts)
  const results = useQueries({ queries: hosts.map((host) => jobSessionsQuery(host.id)) })

  const byHost: Record<string, Session[]> = {}
  hosts.forEach((host, index) => {
    const data = results[index]?.data
    if (data) byHost[host.id] = data
  })
  return byHost
}

export function useHostNotifications(): Record<string, Notification[]> {
  const hosts = useStore((s) => s.hosts)
  const results = useQueries({ queries: hosts.map((host) => notificationsQuery(host.id)) })

  const byHost: Record<string, Notification[]> = {}
  hosts.forEach((host, index) => {
    const data = results[index]?.data
    if (data) byHost[host.id] = data
  })
  return byHost
}

export interface HostGroups {
  groups: Record<string, SessionGroup[]>
  /**
   * Hosts whose daemon has no grouping routes.
   *
   * A daemon older than the feature answers 404, and offering to make a group
   * on a machine that cannot hold one is worse than not offering: the button
   * looks broken rather than absent. Read off the error rather than stored,
   * because the retry rule never retries a 4xx — the 404 stands as the answer.
   */
  unsupported: Record<string, boolean>
}

export function useHostGroups(): HostGroups {
  const hosts = useStore((s) => s.hosts)
  const results = useQueries({ queries: hosts.map((host) => groupsQuery(host.id)) })

  const groups: Record<string, SessionGroup[]> = {}
  const unsupported: Record<string, boolean> = {}
  hosts.forEach((host, index) => {
    const result = results[index]
    if (!result) return
    if (statusOf(result.error) === 404) {
      unsupported[host.id] = true
      groups[host.id] = []
      return
    }
    if (result.data) groups[host.id] = result.data
  })
  return { groups, unsupported }
}

/**
 * How each host's list is ordered.
 *
 * There is no sort-mode route: it is one field of the settings document, so
 * this reads the same query the settings dialog does.
 */
export function useHostSortModes(): Record<string, SortMode> {
  const hosts = useStore((s) => s.hosts)
  const results = useQueries({ queries: hosts.map((host) => settingsQuery(host.id)) })

  const byHost: Record<string, SortMode> = {}
  hosts.forEach((host, index) => {
    const data: SettingsDocument | undefined = results[index]?.data
    // Offline falls back to sorting by activity, which needs nothing from the
    // daemon.
    byHost[host.id] = sortModeOf(data)
  })
  return byHost
}
