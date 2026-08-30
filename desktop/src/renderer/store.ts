import { useSyncExternalStore } from 'react'

import { api, bridge, statusOf } from './bridge.ts'
import { queryClient } from './query-client.ts'
import {
  effectsFor,
  keys,
  mergeSettings,
  patchSessionInPage,
  type SessionListPage,
  type SettingsDocument,
  type SortMode,
} from './keys.ts'
import type { GroupOrder, PathOrder } from './components/grouping.ts'
import { applyDensity, applyProseSize, applyTheme } from '../shared/theme/apply.ts'
import { hasTerminal } from '../shared/models.ts'
import type {
  Density,
  HostRecord,
  HostStatus,
  Notification,
  Session,
  SessionGroup,
  SSEEvent,
  TabStatus,
  HostStats,
} from '../shared/models.ts'
import type { XtermTheme } from '../shared/theme/resolve.ts'

export interface Tab {
  id: string
  hostId: string
  sessionId: string
  /** The daemon's terminal id: the session id for its agent, `id:shN` for a shell. */
  termId: string
  kind: 'agent' | 'shell'
  title: string
  status: TabStatus
}

/**
 * A tab's identity is the daemon terminal it shows. A session's agent uses the
 * session id, so the key of an agent tab is what it always was.
 */
export function terminalId(hostId: string, termId: string): string {
  return `${hostId}:${termId}`
}

/** `sess-1:sh2` reads as "sh 2", which is what the tab is called. */
function shellLabel(termId: string): string {
  const index = termId.slice(termId.lastIndexOf(':sh') + 3)
  return index ? `sh ${index}` : 'shell'
}

export interface Selection {
  hostId: string
  sessionId: string
}

/**
 * What a session's own state is keyed by. Which panel is in front, and which of
 * its terminals, belong to the session being looked at rather than to the
 * window: coming back to a session should find it as it was left.
 */
export function sessionKey(hostId: string, sessionId: string): string {
  return `${hostId}:${sessionId}`
}

export type RightPanel = 'chat' | 'terminal' | 'approvals' | 'git' | 'files'

export type { SortMode }

/**
 * What splits the list into groups, if anything.
 *
 * 'manual' is the tree the user built and the daemon holds. 'auto' is derived
 * from where each session is running and stored nowhere, so it needs no server
 * data and nothing about it can be edited.
 */
export type GroupMode = 'off' | 'manual' | 'auto'

/** Defined with the tree builders that apply it; re-exported so the picker and
 *  the sidebar can read it from the store like every other setting. */
export type { GroupOrder, PathOrder } from './components/grouping.ts'

/**
 * A file the user asked to see, from a chip in the transcript. The counter is
 * what makes clicking the same chip twice reopen it: the path alone would not
 * change, and the panel would ignore it.
 */
export interface FileTarget {
  hostId: string
  path: string
  seq: number
  /** 'find' searches the panel's root for the name instead of opening the path. */
  mode: 'open' | 'find'
  /** Scroll here once the file is open. Absent means the top. */
  line?: number
}

/** A diff an agent asked the human to look at. No path means the whole change. */
export interface DiffTarget {
  hostId: string
  path?: string
  /** Branch to diff against, for a whole-branch review. */
  base?: string
  /** A single commit to show. Never set alongside base. */
  commit?: string
  /** How to draw the patch. Side by side unless the agent asked otherwise. */
  layout?: 'split' | 'unified'
  /** A line of the new file to scroll to and mark. */
  line?: number
  seq: number
}

/**
 * Why an agent moved the view, shown above it.
 *
 * Without this a panel switches and nothing says who did it or what to look
 * for, which is indistinguishable from the app misbehaving.
 */
export interface ShowNote {
  hostId: string
  sessionId: string
  text: string
  seq: number
}

/**
 * Text on its way to a session's composer from a selection elsewhere in the
 * app. Counted like FileTarget: sending the same lines twice has to arrive
 * twice.
 */
export interface PromptDraft {
  hostId: string
  sessionId: string
  text: string
  seq: number
}

export interface State {
  hosts: HostRecord[]
  hostStatus: Record<string, HostStatus>
  /**
   * Server data lives in the query cache, not here — the session lists, the
   * groups, the notifications and the daemon's own settings. What is left is
   * this window's: what is selected, which panels are open, and what it has
   * connected to.
   */
  /** Open terminal connections, one per session, keyed by `terminalId`. */
  tabs: Tab[]
  selection: Selection | null
  /** The panel each session was last reading, by `sessionKey`. */
  panels: Record<string, RightPanel>
  /**
   * Which terminal a session's panel shows, as a tab id, by `sessionKey`. A
   * session has several once the user opens shells beside its agent, and the
   * strip needs to know which one is in front.
   */
  activeTabs: Record<string, string>
  /**
   * Terminals the user disconnected on purpose, by tab id. Without this the
   * panel would attach again the moment it is shown, and a disconnect would
   * last exactly as long as the user stayed on another tab.
   */
  detached: string[]
  fileTarget: FileTarget | null
  diffTarget: DiffTarget | null
  showNote: ShowNote | null
  promptDraft: PromptDraft | null
  /** Sidebar filter, matched against title, project and cwd. */
  query: string
  loading: boolean
  toast: { text: string; kind: 'info' | 'error' } | null
  pairingLink: string | null
  /** The palette xterm draws with; the CSS side is set on <html>, not here. */
  terminalTheme: XtermTheme
  /**
   * How much of a session the sidebar shows. Kept here as well as on <html>
   * because the control that changes it lives in the sidebar and has to show
   * which way it is set.
   */
  density: Density
  /**
   * How the sidebar splits the list: not at all, by the user's group tree, or
   * by each session's directory.
   *
   * Client-side and off by default: the arrangement belongs to the daemon, but
   * how you look at it is this window's business — a phone and a wide monitor
   * want different answers.
   */
  grouping: GroupMode
  /**
   * What orders the directory groups against each other.
   *
   * Only read in 'auto': a made group sits where it was dragged to, and that
   * position is the daemon's. Client-side and unsent for the same reason
   * `grouping` is — it is a reading of the arrangement, not the arrangement.
   */
  groupOrder: GroupOrder
  /** Where each directory has been dragged to, by path. Only read when
   *  groupOrder is 'manual'. */
  dirOrder: PathOrder
}

const GROUPING_KEY = 'helios.grouping'

/**
 * Reads the saved mode, tolerating the flag this setting used to be.
 *
 * An install from before the modes holds '1' or '0' under the same key. Those
 * mean the only grouping there was, which is the manual tree — reading them as
 * an unknown value would silently turn off a sidebar the user had arranged.
 */
function readGroupMode(): GroupMode {
  try {
    const saved = localStorage.getItem(GROUPING_KEY)
    if (saved === 'manual' || saved === 'auto' || saved === 'off') return saved
    if (saved === '1') return 'manual'
    return 'off'
  } catch {
    return 'off'
  }
}

function writeGroupMode(mode: GroupMode): void {
  try {
    localStorage.setItem(GROUPING_KEY, mode)
  } catch {
    // A full or unavailable store costs the preference, not the setting.
  }
}

const GROUP_ORDER_KEY = 'helios.groupOrder'
const DIR_ORDER_KEY = 'helios.dirOrder'

/** Anything but the one other name means activity, which is the default and is
 *  also what an install from before this setting was showing. */
const GROUP_ORDERS: GroupOrder[] = ['activity', 'name', 'manual']

function readGroupOrder(): GroupOrder {
  try {
    const saved = localStorage.getItem(GROUP_ORDER_KEY)
    // Checked against the list rather than one value at a time: the previous
    // form recognised 'name' and treated everything else as 'activity', so
    // adding a third order silently reverted it on every reload.
    return GROUP_ORDERS.find((order) => order === saved) ?? 'activity'
  } catch {
    return 'activity'
  }
}

function readDirOrder(): PathOrder {
  try {
    const raw = localStorage.getItem(DIR_ORDER_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: PathOrder = {}
    for (const [path, at] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof at === 'number' && Number.isFinite(at)) out[path] = at
    }
    return out
  } catch {
    return {}
  }
}

function writeGroupOrder(order: GroupOrder): void {
  try {
    localStorage.setItem(GROUP_ORDER_KEY, order)
  } catch {
    // A full or unavailable store costs the preference, not the setting.
  }
}

const initial: State = {
  hosts: [],
  hostStatus: {},
  tabs: [],
  selection: null,
  panels: {},
  activeTabs: {},
  detached: [],
  fileTarget: null,
  diffTarget: null,
  showNote: null,
  promptDraft: null,
  query: '',
  loading: true,
  terminalTheme: bridge.theme.boot().terminal,
  density: bridge.theme.boot().density,
  toast: null,
  pairingLink: null,
  grouping: readGroupMode(),
  groupOrder: readGroupOrder(),
  dirOrder: readDirOrder(),
}

type Listener = () => void

/**
 * The order the sidebar shows before it is frozen — approvals first, then
 * pinned, then live, then most recent.
 *
 * Duplicated from the sidebar's own comparator on purpose: that one sorts rows
 * it has already built, and this has to answer the same question from the store
 * before any row exists. They must agree, or turning manual on would rearrange
 * the list at the moment it promised to stop.
 */
function byActivity(a: Session, b: Session, pending: Map<string, number>): number {
  const aPending = pending.get(a.session_id) ?? 0
  const bPending = pending.get(b.session_id) ?? 0
  if (aPending !== bPending) return bPending - aPending
  if (a.pinned !== b.pinned) return a.pinned ? -1 : 1
  const aLive = hasTerminal(a)
  const bLive = hasTerminal(b)
  if (aLive !== bLive) return aLive ? -1 : 1
  return (b.last_event_at ?? b.created_at).localeCompare(a.last_event_at ?? a.created_at)
}

/** Matches the mobile app's coalescing window (daemon_api_service.dart:348). */
const REFRESH_DEBOUNCE = 500

/**
 * How often a focused window re-reports the session it is showing.
 *
 * Two minutes against an eviction pass that runs every twenty: frequent enough
 * that the daemon never thinks a session being read is unattended, rare enough
 * to be invisible.
 */
const TOUCH_INTERVAL = 2 * 60 * 1000

/** Rounded hard: this lands in a toast, not in a memory readout. */
function megabytes(bytes: number): string {
  return bytes >= 1024 ** 3
    ? `${(bytes / 1024 ** 3).toFixed(1)} GB`
    : `${Math.round(bytes / 1024 ** 2)} MB`
}

/**
 * A single mutable store with manual subscription.
 *
 * The app's state is small, mostly server-owned, and updated from three
 * independent sources — user actions, SSE, and terminal events. A reducer per
 * source would be more ceremony than the whole thing is worth.
 */
class Store {
  /** What each session's status was when last seen, so a transition into
   *  'terminated' can be told from a session that was already over. */
  private lastStatus = new Map<string, Map<string, string>>()

  private state: State = initial
  private listeners = new Set<Listener>()
  private toastTimer: ReturnType<typeof setTimeout> | null = null

  getSnapshot = (): State => this.state

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private set(patch: Partial<State> | ((state: State) => Partial<State>)): void {
    const next = typeof patch === 'function' ? patch(this.state) : patch
    this.state = { ...this.state, ...next }
    for (const listener of this.listeners) listener()
  }

  // ─── Lifecycle ─────────────────────────────────────────────────────────

  async init(): Promise<void> {
    this.watchTerminations()

    // The preload painted the boot theme already; this keeps up with changes
    // made afterwards, whether from the settings dialog or the OS switching
    // between light and dark underneath us.
    bridge.theme.onChanged(({ theme, terminal, glass, proseSize, density }) => {
      applyTheme(document.documentElement, theme, glass)
      applyProseSize(document.documentElement, proseSize)
      applyDensity(document.documentElement, density)
      this.set({ terminalTheme: terminal, density })
    })

    bridge.hosts.onChanged((hosts) => {
      this.set({ hosts })
      for (const host of hosts) void this.invalidateHost(host.id)
    })
    bridge.hosts.onStatus((status) => {
      this.set((s) => ({ hostStatus: { ...s.hostStatus, [status.id]: status } }))
      // Coming back online means whatever happened while offline was missed.
      if (status.state === 'online') void this.invalidateHost(status.id)
    })
    bridge.hosts.onEvent(({ hostId, event }) => this.onServerEvent(hostId, event))
    this.startTouchHeartbeat()

    bridge.term.onStatus(({ tabId, status }) => {
      this.set((s) => ({ tabs: s.tabs.map((t) => (t.id === tabId ? { ...t, status } : t)) }))
    })
    bridge.term.onClosed(({ tabId, reason }) => {
      this.set((s) => ({
        tabs: s.tabs.map((t) => (t.id === tabId ? { ...t, status: { ...t.status, state: 'closed', detail: reason } } : t)),
      }))
    })

    bridge.app.onOpenPairing((url) => this.set({ pairingLink: url }))
    bridge.app.onActivateNotification((target) => {
      if (!target.sessionId) return
      this.select(target.hostId, target.sessionId)
      this.setPanel('approvals')
    })

    const hosts = await bridge.hosts.list()
    const statuses = await bridge.hosts.statuses()
    this.set({
      hosts,
      hostStatus: Object.fromEntries(statuses.map((s) => [s.id, s])),
      loading: false,
    })

    if (hosts.length === 0) {
      // First run on the machine the daemon is on: pair silently rather than
      // making the user copy a token from a terminal they already trust.
      await this.tryPairLocal()
    }
    await Promise.all(this.state.hosts.map((h) => this.invalidateHost(h.id)))
  }

  private async tryPairLocal(): Promise<void> {
    try {
      await bridge.hosts.pairLocal()
    } catch {
      // No daemon on this machine. The empty state explains how to add one.
    }
  }

  // ─── Data ──────────────────────────────────────────────────────────────

  /**
   * One switch for every host: the arrangement is stored per daemon, but a
   * sidebar that sorts one group by activity and holds another still is a
   * sidebar with no answer to "why did that move".
   */
  async setSortModeEverywhere(mode: SortMode): Promise<void> {
    await Promise.all(this.state.hosts.map((host) => this.setSortMode(host.id, mode)))
  }

  async setSortMode(hostId: string, mode: SortMode): Promise<void> {
    const key = keys.settings(hostId)
    const before = queryClient.getQueryData<SettingsDocument>(key)
    // Applied here first: a list that keeps sorting itself while the daemon
    // answers reads as a switch that did nothing.
    queryClient.setQueryData<SettingsDocument>(key, (doc) =>
      mergeSettings(doc, { 'sessions.sort': mode }),
    )
    try {
      await api(hostId).updateSettings({ 'sessions.sort': mode })
      // Switching to manual freezes what is on screen, so the order the user
      // was looking at is the order they keep.
      if (mode === 'manual') await this.freezeOrder(hostId)
    } catch (err) {
      queryClient.setQueryData(key, before)
      this.fail(err)
    }
  }

  /**
   * Writes the order the sessions are in right now.
   *
   * Without this, turning manual on would sort by a sort_order nobody has set
   * — every session at 0, or at whatever negative number it was created with —
   * and the list would jump the moment it was supposed to stop moving.
   */
  private async freezeOrder(hostId: string): Promise<void> {
    const sessions = this.sessionsOf(hostId)
    if (sessions.length === 0) return
    const pending = new Map<string, number>()
    for (const notification of this.notificationsOf(hostId)) {
      pending.set(notification.source_session, (pending.get(notification.source_session) ?? 0) + 1)
    }
    const ordered = [...sessions]
      .sort((a, b) => byActivity(a, b, pending))
      .map((session) => session.session_id)
    await api(hostId).setSessionOrder(ordered)
    await this.invalidateSessions(hostId)
  }

  /**
   * A field of one session, applied here before the daemon answers.
   *
   * Pinning and renaming are direct manipulation: a star that lights and then
   * goes out again, or a title that reverts as you stop typing, reads as the
   * click having failed rather than as the round trip it is.
   */
  async patchSessionField(
    hostId: string,
    sessionId: string,
    patch: Record<string, unknown>,
  ): Promise<void> {
    const before = queryClient.getQueriesData<SessionListPage>({
      queryKey: keys.allSessions(hostId),
    })
    queryClient.setQueriesData<SessionListPage>({ queryKey: keys.allSessions(hostId) }, (page) =>
      patchSessionInPage(page, sessionId, patch as Partial<Session>),
    )
    try {
      await api(hostId).patchSession(sessionId, patch)
    } catch (err) {
      for (const [key, page] of before) queryClient.setQueryData(key, page)
      this.fail(err)
    }
  }

  /**
   * Switching mode restarts the agent, so the daemon refuses while a session is
   * mid-turn. Applied here first all the same: the control is disabled unless
   * the session is idle, so a refusal is the exception, and the rollback puts
   * the old mode back when it happens.
   */
  async setPermissionMode(hostId: string, sessionId: string, mode: string): Promise<void> {
    const before = queryClient.getQueriesData<SessionListPage>({
      queryKey: keys.allSessions(hostId),
    })
    queryClient.setQueriesData<SessionListPage>({ queryKey: keys.allSessions(hostId) }, (page) =>
      patchSessionInPage(page, sessionId, { permission_mode: mode }),
    )
    try {
      await api(hostId).setPermissionMode(sessionId, mode)
      this.notify(`Permission mode set to ${mode}`)
    } catch (err) {
      for (const [key, page] of before) queryClient.setQueryData(key, page)
      if (statusOf(err) === 409) this.notify('Session is busy — try again when it is idle', 'error')
      else this.fail(err)
    }
  }

  /** Applies a drag: the whole arrangement, in the order the user left it. */
  async reorderSessions(hostId: string, orderedIds: string[]): Promise<void> {
    const before = queryClient.getQueriesData<SessionListPage>({ queryKey: keys.allSessions(hostId) })
    // Locally first: a card that snaps back while the daemon answers reads as
    // a drag that failed.
    queryClient.setQueriesData<SessionListPage>({ queryKey: keys.allSessions(hostId) }, (page) => {
      if (!page) return page
      const byId = new Map(page.sessions.map((session) => [session.session_id, session]))
      const next: Session[] = []
      orderedIds.forEach((id, index) => {
        const session = byId.get(id)
        if (session) next.push({ ...session, sort_order: index })
      })
      return { ...page, sessions: next }
    })

    try {
      await api(hostId).setSessionOrder(orderedIds)
    } catch (err) {
      for (const [key, page] of before) queryClient.setQueryData(key, page)
      this.fail(err)
    }
  }

  /**
   * Changes how this window splits the list.
   *
   * Asking the daemon to resolve groups costs a lookup it should not do for a
   * client that will not render them, so the flag rides on the fetch and the
   * list is refetched when it changes. Only the manual tree needs it: the
   * directory grouping reads a field every session already carries, so
   * switching into or out of it is a re-render and nothing more.
   */
  async setGrouping(mode: GroupMode): Promise<void> {
    const before = this.state.grouping
    this.set({ grouping: mode })
    writeGroupMode(mode)
    if ((before === 'manual') === (mode === 'manual')) return
    await Promise.all(this.state.hosts.map((host) => this.invalidateHost(host.id)))
  }

  /**
   * Changes what orders the directory groups.
   *
   * Nothing is refetched: both orders are worked out from fields the sessions
   * already carry, so this is a re-render.
   */
  /** Records a hand-arranged directory order, first path first. Dragging one
   *  is itself the request to sort by hand, so it selects that mode. */
  reorderDirectories(paths: string[]): void {
    const dirOrder: PathOrder = {}
    paths.forEach((path, index) => {
      dirOrder[path] = index
    })
    this.set({ dirOrder, groupOrder: 'manual' })
    try {
      localStorage.setItem(DIR_ORDER_KEY, JSON.stringify(dirOrder))
      writeGroupOrder('manual')
    } catch {
      // A full or unavailable store costs the arrangement, not the list.
    }
  }

  orderGroupsBy(order: GroupOrder): void {
    this.set({ groupOrder: order })
    writeGroupOrder(order)
  }

  /** An empty parent makes it a root. */
  async createGroup(hostId: string, name: string, parent = ''): Promise<SessionGroup | null> {
    try {
      const group = await api(hostId).createGroup(name, parent)
      await this.refetchGroups(hostId)
      return group
    } catch (err) {
      this.fail(err)
      return null
    }
  }

  async renameGroup(hostId: string, key: string, name: string): Promise<void> {
    try {
      await api(hostId).renameGroup(key, name)
      await this.refetchGroups(hostId)
    } catch (err) {
      this.fail(err)
    }
  }

  /** Moves a group and everything beneath it. An empty parent makes it a root. */
  async moveGroup(hostId: string, key: string, parent: string): Promise<void> {
    try {
      await api(hostId).moveGroup(key, parent)
      await Promise.all([this.refetchGroups(hostId), this.invalidateSessions(hostId)])
    } catch (err) {
      this.fail(err)
    }
  }

  /** Deleting lifts the group's children and its sessions one level, so both
   *  the tree and the sessions have to be refetched. */
  async deleteGroup(hostId: string, key: string): Promise<void> {
    try {
      await api(hostId).deleteGroup(key)
      await Promise.all([this.refetchGroups(hostId), this.invalidateSessions(hostId)])
    } catch (err) {
      this.fail(err)
    }
  }

  /**
   * Moves a group, and every session under it with it.
   *
   * Applied here first for the same reason a dragged session is: a header that
   * snaps back while the daemon answers reads as a drag that failed.
   */
  async reorderGroups(hostId: string, parent: string, orderedKeys: string[]): Promise<void> {
    const key = keys.groups(hostId)
    const before = queryClient.getQueryData<SessionGroup[]>(key)
    queryClient.setQueryData<SessionGroup[]>(key, (groups) => {
      if (!groups) return groups
      const byKey = new Map(groups.map((group) => [group.key, group]))
      const next: SessionGroup[] = []
      orderedKeys.forEach((k, index) => {
        const group = byKey.get(k)
        if (group) next.push({ ...group, position: index })
      })
      return next
    })

    try {
      await api(hostId).setGroupOrder(parent, orderedKeys)
      // The sessions carry the position they were dragged to.
      await this.invalidateSessions(hostId)
    } catch (err) {
      queryClient.setQueryData(key, before)
      this.fail(err)
    }
  }

  /** Files a session under one group. An empty key unassigns it. */
  async setSessionGroup(hostId: string, sessionId: string, key: string): Promise<void> {
    try {
      await api(hostId).setSessionGroup(sessionId, key)
      await this.invalidateSessions(hostId)
    } catch (err) {
      this.fail(err)
    }
  }

  /**
   * The session list as the cache holds it.
   *
   * Variant-agnostic on purpose: the list is cached twice, once per `grouped`
   * flag, and the store does not care which one the sidebar last asked for.
   */
  sessionsOf(hostId: string): Session[] {
    for (const [, page] of queryClient.getQueriesData<SessionListPage>({
      queryKey: keys.allSessions(hostId),
    })) {
      if (page) return page.sessions
    }
    return []
  }

  sessionById(hostId: string, sessionId: string): Session | undefined {
    return this.sessionsOf(hostId).find((session) => session.session_id === sessionId)
  }

  /** The groups as the cache holds them, for a caller that has just awaited a
   *  write and needs the arrangement the daemon now has. */
  groupsOf(hostId: string): SessionGroup[] {
    return queryClient.getQueryData<SessionGroup[]>(keys.groups(hostId)) ?? []
  }

  private notificationsOf(hostId: string): Notification[] {
    return queryClient.getQueryData<Notification[]>(keys.notifications(hostId)) ?? []
  }

  /** Asked again after a write the caller made outside the store. */
  invalidateSessionsFor(hostId: string): Promise<void> {
    return this.invalidateSessions(hostId)
  }

  invalidateNotificationsFor(hostId: string): Promise<void> {
    return queryClient.invalidateQueries({ queryKey: keys.notifications(hostId) })
  }

  /** Every read for one host, asked again. */
  private invalidateHost(hostId: string): Promise<void> {
    return queryClient.invalidateQueries({ queryKey: keys.host(hostId) })
  }

  private invalidateSessions(hostId: string): Promise<void> {
    return queryClient.invalidateQueries({ queryKey: keys.allSessions(hostId) })
  }

  /**
   * Awaited rather than fired off, for the callers that read the answer back.
   *
   * Dropping a group header beside another is two writes, and the second reads
   * the sibling list the first produced — a group arriving from another parent
   * is not among the target's siblings until the move lands.
   */
  private refetchGroups(hostId: string): Promise<void> {
    return queryClient.refetchQueries({ queryKey: keys.groups(hostId) })
  }

  /**
   * Closes out sessions that ended while nothing was watching.
   *
   * A status event is the usual way this is heard, but a session that ends
   * while this client is disconnected only shows up in the next list. Watching
   * the cache catches both, and costs the store no copy of the list.
   */
  private watchTerminations(): void {
    queryClient.getQueryCache().subscribe((event) => {
      if (event.type !== 'updated') return
      const key = event.query.queryKey
      if (key[0] !== 'host' || key[2] !== 'sessions') return
      const hostId = String(key[1])
      const seen = this.lastStatus.get(hostId) ?? new Map<string, string>()
      for (const session of this.sessionsOf(hostId)) {
        const before = seen.get(session.session_id)
        seen.set(session.session_id, session.status)
        if (session.status === 'terminated' && before && before !== 'terminated') {
          this.onTerminated(hostId, session.session_id)
        }
      }
      this.lastStatus.set(hostId, seen)
    })
  }

  /**
   * Tells the cache what the event changed.
   *
   * Runs alongside the hand-patching below rather than instead of it, so the
   * two paths can be swapped over one screen at a time. Invalidation is already
   * coalesced, which is what `scheduleRefresh` debounces for by hand.
   */
  private applyToCache(hostId: string, event: SSEEvent): void {
    for (const effect of effectsFor(hostId, event)) {
      if (effect.kind === 'invalidate') {
        void queryClient.invalidateQueries({ queryKey: effect.queryKey })
        continue
      }
      const was = this.sessionById(hostId, effect.sessionId)
      queryClient.setQueriesData<SessionListPage>({ queryKey: keys.allSessions(hostId) }, (page) =>
        patchSessionInPage(page, effect.sessionId, effect.patch),
      )
      if (effect.patch.status === 'terminated' && was && was.status !== 'terminated') {
        this.onTerminated(hostId, effect.sessionId)
      }
    }
  }

  /**
   * Applies a server event in place where the payload allows it, and refetches
   * behind it. The patch is what paints immediately; the refetch is what makes
   * the rest of the record true, and it is debounced so a busy turn costs one
   * round trip rather than one per event.
   */
  private onServerEvent(hostId: string, event: SSEEvent): void {
    this.applyToCache(hostId, event)
    switch (event.type) {
      case 'show':
        this.applyShow(hostId, event.data)
        return

      case 'session_evicted': {
        // Say it out loud. A session that goes cold in silence and then takes
        // seconds to answer reads as Helios being slow, and its terminal tab
        // coming back empty reads as lost work.
        const project = str(event.data.project) || 'A session'
        const freed = typeof event.data.freed === 'number' ? event.data.freed : 0
        const unread = str(event.data.unread)
        this.notify(
          `${project} went cold${freed > 0 ? ` — freed ${megabytes(freed)}` : ''}` +
            `${unread ? `, not opened for ${unread}` : ''}. Open it to bring it back.`,
          'info',
        )
        return
      }

      // A shell belongs to the session, not to the client that opened it: one
      // started on the phone should be a tab here too, and one closed there
      // should not linger as a tab attached to nothing.
      case 'terminal_opened': {
        const sessionId = str(event.data.session_id)
        if (sessionId) void this.syncShells(hostId, sessionId)
        return
      }
      case 'terminal_closed': {
        const termId = str(event.data.terminal_id)
        if (termId) this.closeTab(terminalId(hostId, termId))
        return
      }

      // Everything else is a fact about data, and applyToCache has already
      // said which keys it takes out.
      default:
        return
    }
  }

  /**
   * A session that has just ended has nothing left to watch: its terminals go,
   * and it lets go of the detail pane if it was the one on screen. Driven by
   * the status rather than by the click, so a terminate from the TUI, from
   * mobile, or from the agent exiting on its own lands the same way.
   *
   * Only on the transition. Selecting a session that ended long ago is a
   * request to read its transcript, not something to undo.
   */
  private onTerminated(hostId: string, sessionId: string): void {
    for (const tab of this.state.tabs.filter((t) => t.hostId === hostId && t.sessionId === sessionId)) {
      this.closeTab(tab.id)
    }
    const { selection } = this.state
    if (selection?.hostId === hostId && selection.sessionId === sessionId) this.set({ selection: null })
  }

  // ─── Selection and panels ──────────────────────────────────────────────

  select(hostId: string, sessionId: string): void {
    this.set({ selection: { hostId, sessionId } })
    this.touch(hostId, sessionId)
  }

  /**
   * Tells the daemon a human is looking at this session.
   *
   * The daemon decides which sessions to let go cold when memory is tight, and
   * without this it can only see when the *agent* last ran — so a session being
   * read right now looks the same as one nobody has opened in a day.
   *
   * Fire and forget. A missed sample costs one interval of accuracy.
   */
  touch(hostId: string, sessionId: string): void {
    void api(hostId)
      .touchSession(sessionId)
      .catch(() => {
        // Older daemon, or offline. Neither is worth telling the user about.
      })
  }

  /**
   * Keeps the selected session marked as watched while the window has focus.
   *
   * Focus is the point. A viewer count would say a socket is open, so a desktop
   * left running overnight would pin whatever it happened to be showing.
   */
  private startTouchHeartbeat(): void {
    setInterval(() => {
      if (!document.hasFocus()) return
      const selection = this.state.selection
      if (selection) this.touch(selection.hostId, selection.sessionId)
    }, TOUCH_INTERVAL)
  }

  setPanel(panel: RightPanel): void {
    this.set((s) => ({ panels: showPanel(s, s.selection, panel) }))
  }

  /** Shows a file in the Files panel, switching to it. */
  openFile(hostId: string, path: string): void {
    this.set((s) => ({
      panels: showPanel(s, s.selection, 'files'),
      fileTarget: { hostId, path, seq: (s.fileTarget?.seq ?? 0) + 1, mode: 'open' },
    }))
  }

  /**
   * Looks a file name up in the Files panel. The transcript's path belongs to
   * the checkout the agent ran in, which is not always the one being browsed —
   * searching by name finds it wherever the panel is currently rooted.
   */
  findFile(hostId: string, path: string): void {
    this.set((s) => ({
      panels: showPanel(s, s.selection, 'files'),
      fileTarget: { hostId, path, seq: (s.fileTarget?.seq ?? 0) + 1, mode: 'find' },
    }))
  }

  /**
   * Called by the panel once it has opened the file. Without this the request
   * would fire again every time the panel remounts, dragging the user back to
   * a file they looked at an hour ago.
   */
  /**
   * Moves a session's view because its agent asked, through helios_show.
   *
   * The selection is left alone on purpose. A show for a session you are not
   * looking at prepares that session's panel and waits there — being thrown
   * between sessions by a background agent would be worse than missing it.
   */
  private applyShow(hostId: string, data: Record<string, unknown>): void {
    const sessionId = str(data.session_id)
    const view = str(data.view)
    if (!sessionId || !view) return

    const target: Selection = { hostId, sessionId }
    const note = str(data.note)
    const seq = (this.state.showNote?.seq ?? 0) + 1

    const panel: RightPanel | null =
      view === 'file' ? 'files' : view === 'diff' ? 'git' : view === 'agent' ? 'chat' : null

    if (view === 'terminal') {
      const session = this.sessionById(hostId, sessionId)
      if (session) void this.showTerminal(hostId, session)
    } else if (panel) {
      this.set((s) => ({ panels: showPanel(s, target, panel) }))
    }

    if (view === 'file') {
      const path = str(data.path)
      if (!path) return
      const line = typeof data.line === 'number' ? data.line : undefined
      this.set((s) => ({
        fileTarget: { hostId, path, seq: (s.fileTarget?.seq ?? 0) + 1, mode: 'open', line },
      }))
    }

    if (view === 'diff') {
      this.set((s) => ({
        diffTarget: {
          hostId,
          path: str(data.path) || undefined,
          base: str(data.base) || undefined,
          commit: str(data.commit) || undefined,
          layout: str(data.layout) === 'unified' ? 'unified' : 'split',
          line: typeof data.line === 'number' ? data.line : undefined,
          seq: (s.diffTarget?.seq ?? 0) + 1,
        },
      }))
    }

    this.set({ showNote: note ? { hostId, sessionId, text: note, seq } : null })
  }

  /** Dismisses the agent's note, once the human has moved on from it. */
  clearShowNote(): void {
    this.set({ showNote: null })
  }

  clearDiffTarget(): void {
    this.set({ diffTarget: null })
  }

  clearFileTarget(): void {
    this.set({ fileTarget: null })
  }

  /** Puts text in the session's composer and shows it, without sending. */
  appendPrompt(hostId: string, sessionId: string, text: string): void {
    this.set((s) => ({
      panels: showPanel(s, { hostId, sessionId }, 'chat'),
      promptDraft: { hostId, sessionId, text, seq: (s.promptDraft?.seq ?? 0) + 1 },
    }))
  }

  /** Called by the composer once the text is in the box. */
  clearPromptDraft(): void {
    this.set({ promptDraft: null })
  }

  setQuery(query: string): void {
    this.set({ query })
  }

  setPairingLink(pairingLink: string | null): void {
    this.set({ pairingLink })
  }

  // ─── Terminals ─────────────────────────────────────────────────────────

  /**
   * Shows a session's terminal panel, attaching if nothing is attached yet.
   * Waking is explicit: attaching to a cold session would otherwise spawn an
   * agent process every time someone clicked through a list.
   */
  async openTerminal(hostId: string, session: Session, wake: boolean): Promise<void> {
    // The panel belongs to whichever session is selected, so showing one
    // session's terminal means selecting it.
    const id = terminalId(hostId, session.session_id)
    const target = { hostId, sessionId: session.session_id }
    this.set((s) => ({
      selection: target,
      panels: showPanel(s, target, 'terminal'),
      activeTabs: showTab(s, target, id),
      detached: s.detached.filter((tabId) => tabId !== id),
    }))

    if (this.state.tabs.some((t) => t.id === id)) return

    const tab: Tab = {
      id,
      hostId,
      sessionId: session.session_id,
      termId: session.session_id,
      kind: 'agent',
      title: session.title ?? session.project ?? session.session_id.slice(0, 8),
      // A wake restarts the agent and reloads its transcript, which takes
      // seconds rather than milliseconds. Saying only "Connecting" for that
      // long reads as a hang.
      status: wake ? { state: 'connecting', detail: 'starting the agent' } : { state: 'connecting' },
    }
    this.set((s) => ({ tabs: [...s.tabs, tab] }))

    try {
      // Zero is an abstention, not a guess. The size is unknown until xterm has
      // measured its container, and a placeholder 80×24 is not neutral: the PTY
      // adopts the smallest interactive viewer, so attaching would shrink a
      // 192-column terminal to 80, render its snapshot at that width, and let
      // the pane stretch it back a moment later. Two reflows per attach, and a
      // shell left drawing its prompt at coordinates from the wrong geometry.
      await bridge.term.open({
        tabId: tab.id,
        hostId,
        sessionId: session.session_id,
        cols: 0,
        rows: 0,
        wake,
      })
    } catch (err) {
      this.set((s) => ({ tabs: s.tabs.filter((t) => t.id !== tab.id) }))
      this.fail(err)
    }
  }

  /**
   * Opens a login shell beside the session's agent. Not a session of its own:
   * it runs no agent and appears nowhere but this session's terminal strip.
   */
  async openShell(hostId: string, sessionId: string): Promise<void> {
    const target = { hostId, sessionId }
    this.set((s) => ({ selection: target, panels: showPanel(s, target, 'terminal') }))
    try {
      const info = await api(hostId).openShell(sessionId)
      await this.attachTerminal(hostId, sessionId, info.id, shellLabel(info.id))
      this.set((s) => ({ activeTabs: showTab(s, target, terminalId(hostId, info.id)) }))
    } catch (err) {
      this.fail(err)
    }
  }

  /**
   * Re-lists a session's shells and attaches to any this client is not showing.
   * The processes outlive the app, so without this a restart leaves them
   * running and invisible.
   */
  async syncShells(hostId: string, sessionId: string): Promise<void> {
    let terminals
    try {
      terminals = await api(hostId).terminals(sessionId)
    } catch {
      // An older daemon has no such endpoint, and a session with no shells is
      // the overwhelmingly common case. Neither is worth an error.
      return
    }
    for (const info of terminals) {
      if (info.kind !== 'shell') continue
      if (this.state.tabs.some((t) => t.id === terminalId(hostId, info.id))) continue
      await this.attachTerminal(hostId, sessionId, info.id, shellLabel(info.id))
    }
  }

  private async attachTerminal(
    hostId: string,
    sessionId: string,
    termId: string,
    title: string,
  ): Promise<void> {
    const id = terminalId(hostId, termId)
    if (this.state.tabs.some((t) => t.id === id)) return

    const tab: Tab = { id, hostId, sessionId, termId, kind: 'shell', title, status: { state: 'connecting' } }
    this.set((s) => ({
      tabs: [...s.tabs, tab],
      detached: s.detached.filter((tabId) => tabId !== id),
    }))
    try {
      // Abstain until the pane has measured itself; see openTerminal above.
      await bridge.term.open({ tabId: id, hostId, sessionId, cols: 0, rows: 0, terminalId: termId })
    } catch (err) {
      this.set((s) => ({ tabs: s.tabs.filter((t) => t.id !== id) }))
      throw err
    }
  }

  /** Closes a shell for good: the process is killed, not just detached. */
  async killShell(tabId: string): Promise<void> {
    const tab = this.state.tabs.find((t) => t.id === tabId)
    if (!tab || tab.kind !== 'shell') return
    this.closeTab(tabId)
    try {
      await api(tab.hostId).killTerminal(tab.termId)
    } catch (err) {
      this.fail(err)
    }
  }

  /**
   * Lets go of the terminal without touching what is running in it. The host
   * keeps the PTY and its scrollback, so attaching again picks the session up
   * where it was — this frees the viewer, not the agent.
   */
  disconnectTab(tabId: string): void {
    if (!this.state.tabs.some((t) => t.id === tabId)) return
    this.closeTab(tabId)
    this.set((s) => ({ detached: s.detached.includes(tabId) ? s.detached : [...s.detached, tabId] }))
  }

  /**
   * Drops the connection and dials again. The host keeps running and replays
   * its scrollback, which is what makes this safe for an agent's terminal:
   * nothing about the session changes.
   */
  async reconnectTab(tabId: string): Promise<void> {
    const tab = this.state.tabs.find((t) => t.id === tabId)
    if (!tab) return
    this.closeTab(tabId)
    if (tab.kind === 'shell') {
      await this.attachTerminal(tab.hostId, tab.sessionId, tab.termId, tab.title).catch((err: unknown) =>
        this.fail(err),
      )
      // Closing the tab took its id out of the active list, and a strip naming
      // nothing falls back to the agent's terminal — so reloading a shell
      // would move the user somewhere they did not ask to go. It comes back
      // under the same id, so it can be asked for by name.
      this.selectTab(tab.id)
      return
    }
    const session = this.sessionById(tab.hostId, tab.sessionId)
    if (session) await this.openTerminal(tab.hostId, session, false)
  }

  /**
   * Swaps the sidebar between showing a session on four lines and on one.
   *
   * Painted here before the main process answers: it replies with the resolved
   * theme rather than the preference, and a toggle that waits for a round trip
   * reads as one that missed the click.
   */
  async setDensity(density: Density): Promise<void> {
    applyDensity(document.documentElement, density)
    this.set({ density })
    try {
      await bridge.theme.set({ density })
    } catch (err) {
      this.fail(err)
    }
  }

  renameTab(tabId: string, title: string): void {
    const trimmed = title.trim()
    if (!trimmed) return
    this.set((s) => ({ tabs: s.tabs.map((t) => (t.id === tabId ? { ...t, title: trimmed } : t)) }))
  }

  /**
   * Brings a terminated session's agent back and moves the daemon's record out
   * of `terminated`, which is what re-enables prompts. Distinct from waking:
   * a wake starts the host but leaves the status alone, so a woken terminated
   * session looks alive and refuses everything.
   */
  async resumeSession(hostId: string, sessionId: string): Promise<void> {
    try {
      await api(hostId).resume(sessionId)
      this.notify('Session resumed')
    } catch (err) {
      // The daemon refuses to resume a session that is already running its
      // turn, which is not a failure worth a red error from the user's side.
      if (statusOf(err) === 409) this.notify('Session is already running', 'error')
      else this.fail(err)
    }
    await this.invalidateSessions(hostId)
  }

  /**
   * Shows the terminal panel for a session, attaching only if the host is
   * already running. A cold session is left alone — moving between tabs should
   * not start an agent process — and the panel offers the wake instead.
   */
  showTerminal(hostId: string, session: Session): void {
    const id = terminalId(hostId, session.session_id)
    const target = { hostId, sessionId: session.session_id }
    this.set((s) => ({ panels: showPanel(s, target, 'terminal'), activeTabs: showTab(s, target, id) }))
    if (this.state.tabs.some((t) => t.id === id)) return
    // Looking at a terminal that was disconnected on purpose is not a request
    // to attach again: the panel offers the button for that.
    if (this.state.detached.includes(id)) return
    if (hasTerminal(session)) void this.openTerminal(hostId, session, false)
  }

  /**
   * Drops a tab, handing the front to the one beside it.
   *
   * Naming nothing would do instead — the strip falls back to the agent's
   * terminal when it finds no name — but closing the middle of three shells is
   * not a request to leave the shells. The neighbour is the one before it,
   * since a tab is usually opened after the one it belongs with; the last one
   * closed leaves the entry empty, and the fallback is right again.
   */
  closeTab(tabId: string): void {
    void bridge.term.close(tabId)
    this.set((s) => {
      const closing = s.tabs.find((t) => t.id === tabId)
      const tabs = s.tabs.filter((t) => t.id !== tabId)
      const activeTabs = { ...s.activeTabs }
      const key = closing ? sessionKey(closing.hostId, closing.sessionId) : null

      // Naming nothing would do — the strip falls back to the agent's terminal
      // when it finds no name — but closing the middle of three shells is not
      // a request to leave the shells. The one before it, since a shell is
      // usually opened after the one it belongs with; the last shell closed
      // names nothing, and the fallback is right again.
      //
      // Shells only. Closing the agent's terminal is a disconnect, and the
      // panel has something to say about that which a shell would cover up.
      if (key && closing?.kind === 'shell' && activeTabs[key] === tabId) {
        const shells = s.tabs.filter(
          (t) => t.kind === 'shell' && t.hostId === closing.hostId && t.sessionId === closing.sessionId,
        )
        const at = shells.findIndex((t) => t.id === tabId)
        const next = shells[at - 1] ?? shells[at + 1]
        if (next) activeTabs[key] = next.id
        else delete activeTabs[key]
      } else {
        for (const [k, id] of Object.entries(activeTabs)) if (id === tabId) delete activeTabs[k]
      }

      return { tabs, activeTabs }
    })
  }

  /** Brings one of a session's terminals to the front. */
  selectTab(tabId: string): void {
    const tab = this.state.tabs.find((t) => t.id === tabId)
    if (!tab) return
    const target = { hostId: tab.hostId, sessionId: tab.sessionId }
    this.set((s) => ({ panels: showPanel(s, target, 'terminal'), activeTabs: showTab(s, target, tabId) }))
  }

  // ─── Feedback ──────────────────────────────────────────────────────────

  notify(text: string, kind: 'info' | 'error' = 'info'): void {
    this.set({ toast: { text, kind } })
    if (this.toastTimer) clearTimeout(this.toastTimer)
    this.toastTimer = setTimeout(() => this.set({ toast: null }), 4000)
  }

  fail(err: unknown): void {
    this.notify(err instanceof Error ? err.message : String(err), 'error')
  }

  /**
   * Reports a failure that belongs to one host.
   *
   * A host that cannot be reached already says so with its dot, and undici's
   * bare "fetch failed" does not even name which one — so a lost connection is
   * announced once, when the host was still believed to be up, and left to the
   * dot after that. Anything else is a real error and is shown as it comes.
   */
  failHost(hostId: string, err: unknown): void {
    const message = err instanceof Error ? err.message : String(err)
    if (!/fetch failed|ECONNREFUSED|ENOTFOUND|ETIMEDOUT|network|timeout/i.test(message)) {
      this.notify(message, 'error')
      return
    }
    if (this.state.hostStatus[hostId]?.state !== 'online') return
    const name = this.state.hosts.find((host) => host.id === hostId)?.name ?? 'The daemon'
    this.notify(`${name} is not answering`, 'error')
  }
}

export const store = new Store()

export function useStore<T>(select: (state: State) => T): T {
  return useSyncExternalStore(store.subscribe, () => select(store.getSnapshot()))
}

/** The panel the selected session is reading. */
export function currentPanel(state: State): RightPanel {
  if (!state.selection) return 'chat'
  return state.panels[sessionKey(state.selection.hostId, state.selection.sessionId)] ?? 'chat'
}

/** The terminal the selected session has in front, if it has named one. */
export function currentTab(state: State): string | null {
  if (!state.selection) return null
  return state.activeTabs[sessionKey(state.selection.hostId, state.selection.sessionId)] ?? null
}

function showPanel(state: State, target: Selection | null, panel: RightPanel): Record<string, RightPanel> {
  if (!target) return state.panels
  return { ...state.panels, [sessionKey(target.hostId, target.sessionId)]: panel }
}

function showTab(state: State, target: Selection, tabId: string): Record<string, string> {
  return { ...state.activeTabs, [sessionKey(target.hostId, target.sessionId)]: tabId }
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}
