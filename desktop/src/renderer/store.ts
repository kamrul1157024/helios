import { useSyncExternalStore } from 'react'

import { api, bridge, statusOf } from './bridge.ts'
import { applyTheme } from '../shared/theme/apply.ts'
import { hasTerminal } from '../shared/models.ts'
import type { HostRecord, HostStatus, Notification, Session, SSEEvent, TabStatus } from '../shared/models.ts'
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

/** Sorted by what the sessions are doing, or by hand. */
export type SortMode = 'activity' | 'manual'

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
  sessions: Record<string, Session[]>
  notifications: Record<string, Notification[]>
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
  promptDraft: PromptDraft | null
  /** Sidebar filter, matched against title, project and cwd. */
  query: string
  showArchived: boolean
  /**
   * How each host's session list is ordered, by host id.
   *
   * 'activity' recomputes the order from what the sessions are doing —
   * approvals first, then live, then most recent. 'manual' leaves them where
   * they were put. The daemon holds it, so every client agrees.
   */
  sortMode: Record<string, SortMode>
  loading: boolean
  toast: { text: string; kind: 'info' | 'error' } | null
  pairingLink: string | null
  /** The palette xterm draws with; the CSS side is set on <html>, not here. */
  terminalTheme: XtermTheme
}

const initial: State = {
  hosts: [],
  hostStatus: {},
  sessions: {},
  notifications: {},
  tabs: [],
  selection: null,
  panels: {},
  activeTabs: {},
  detached: [],
  fileTarget: null,
  promptDraft: null,
  query: '',
  showArchived: false,
  sortMode: {},
  loading: true,
  terminalTheme: bridge.theme.boot().terminal,
  toast: null,
  pairingLink: null,
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
 * A single mutable store with manual subscription.
 *
 * The app's state is small, mostly server-owned, and updated from three
 * independent sources — user actions, SSE, and terminal events. A reducer per
 * source would be more ceremony than the whole thing is worth.
 */
class Store {
  private state: State = initial
  private listeners = new Set<Listener>()
  private toastTimer: ReturnType<typeof setTimeout> | null = null
  private refreshTimers = new Map<string, ReturnType<typeof setTimeout>>()

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
    // The preload painted the boot theme already; this keeps up with changes
    // made afterwards, whether from the settings dialog or the OS switching
    // between light and dark underneath us.
    bridge.theme.onChanged(({ theme, terminal, glass }) => {
      applyTheme(document.documentElement, theme, glass)
      this.set({ terminalTheme: terminal })
    })

    bridge.hosts.onChanged((hosts) => {
      this.set({ hosts })
      for (const host of hosts) void this.refreshHost(host.id)
    })
    bridge.hosts.onStatus((status) => {
      this.set((s) => ({ hostStatus: { ...s.hostStatus, [status.id]: status } }))
      // Coming back online means whatever happened while offline was missed.
      if (status.state === 'online') void this.refreshHost(status.id)
    })
    bridge.hosts.onEvent(({ hostId, event }) => this.onServerEvent(hostId, event))

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
    await Promise.all(this.state.hosts.map((h) => this.refreshHost(h.id)))
  }

  private async tryPairLocal(): Promise<void> {
    try {
      await bridge.hosts.pairLocal()
    } catch {
      // No daemon on this machine. The empty state explains how to add one.
    }
  }

  // ─── Data ──────────────────────────────────────────────────────────────

  async refreshHost(hostId: string): Promise<void> {
    await Promise.all([this.refreshSessions(hostId), this.refreshNotifications(hostId), this.refreshSortMode(hostId)])
  }

  /** The daemon owns the sort mode, so a second window opens on the same one. */
  async refreshSortMode(hostId: string): Promise<void> {
    try {
      const body = (await api(hostId).settings()) as { settings?: Record<string, string> }
      const mode: SortMode = body.settings?.['sessions.sort'] === 'manual' ? 'manual' : 'activity'
      this.set((s) => ({ sortMode: { ...s.sortMode, [hostId]: mode } }))
    } catch {
      // Offline. The list falls back to sorting by activity, which needs
      // nothing from the daemon.
    }
  }

  /**
   * One switch for every host: the arrangement is stored per daemon, but a
   * sidebar that sorts one group by activity and holds another still is a
   * sidebar with no answer to "why did that move".
   */
  async setSortModeEverywhere(mode: SortMode): Promise<void> {
    await Promise.all(this.state.hosts.map((host) => this.setSortMode(host.id, mode)))
  }

  async setSortMode(hostId: string, mode: SortMode): Promise<void> {
    const before = this.state.sortMode[hostId] ?? 'activity'
    this.set((s) => ({ sortMode: { ...s.sortMode, [hostId]: mode } }))
    try {
      await api(hostId).updateSettings({ 'sessions.sort': mode })
      // Switching to manual freezes what is on screen, so the order the user
      // was looking at is the order they keep.
      if (mode === 'manual') await this.freezeOrder(hostId)
    } catch (err) {
      this.set((s) => ({ sortMode: { ...s.sortMode, [hostId]: before } }))
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
    const sessions = this.state.sessions[hostId] ?? []
    if (sessions.length === 0) return
    const pending = new Map<string, number>()
    for (const notification of this.state.notifications[hostId] ?? []) {
      pending.set(notification.source_session, (pending.get(notification.source_session) ?? 0) + 1)
    }
    const ordered = [...sessions]
      .sort((a, b) => byActivity(a, b, pending))
      .map((session) => session.session_id)
    await api(hostId).setSessionOrder(ordered)
    await this.refreshSessions(hostId)
  }

  /** Applies a drag: the whole arrangement, in the order the user left it. */
  async reorderSessions(hostId: string, orderedIds: string[]): Promise<void> {
    const before = this.state.sessions[hostId] ?? []
    // Locally first: a card that snaps back while the daemon answers reads as
    // a drag that failed.
    const byId = new Map(before.map((session) => [session.session_id, session]))
    const next: Session[] = []
    orderedIds.forEach((id, index) => {
      const session = byId.get(id)
      if (session) next.push({ ...session, sort_order: index })
    })
    this.set((s) => ({ sessions: { ...s.sessions, [hostId]: next } }))

    try {
      await api(hostId).setSessionOrder(orderedIds)
    } catch (err) {
      this.set((s) => ({ sessions: { ...s.sessions, [hostId]: before } }))
      this.fail(err)
    }
  }

  async refreshSessions(hostId: string): Promise<void> {
    try {
      const sessions = await api(hostId).listSessions()
      const before = this.state.sessions[hostId]
      this.set((s) => ({ sessions: { ...s.sessions, [hostId]: sessions } }))
      for (const session of sessions) {
        if (session.status !== 'terminated') continue
        const was = before?.find((s) => s.session_id === session.session_id)
        if (was && was.status !== 'terminated') this.onTerminated(hostId, session.session_id)
      }
    } catch (err) {
      this.fail(err)
    }
  }

  async refreshNotifications(hostId: string): Promise<void> {
    try {
      const notifications = await api(hostId).notifications({ status: 'pending' })
      this.set((s) => ({ notifications: { ...s.notifications, [hostId]: notifications } }))
    } catch (err) {
      this.fail(err)
    }
  }

  /**
   * Applies a server event in place where the payload allows it, and refetches
   * behind it. The patch is what paints immediately; the refetch is what makes
   * the rest of the record true, and it is debounced so a busy turn costs one
   * round trip rather than one per event.
   */
  private onServerEvent(hostId: string, event: SSEEvent): void {
    switch (event.type) {
      case 'session_status': {
        const id = str(event.data.session_id)
        const status = str(event.data.status)
        if (!id || !status) return
        // A resume carries the new host handle. Taking it matters: the session
        // is cold in this client's copy until something says otherwise, and
        // most session_status events say nothing about the terminal at all —
        // so an absent handle is no evidence the host went away.
        const terminal = str(event.data.terminal)
        this.patchSession(hostId, id, (terminal ? { status, terminal } : { status }) as Partial<Session>)
        // The payload carries a status and little else, but the record behind
        // it moved with it — last_event_at above all, which is the only thing
        // telling the transcript there is more of it to read.
        this.scheduleRefresh(hostId)
        return
      }
      case 'session_updated':
      case 'session_deleted':
        void this.refreshSessions(hostId)
        return
      case 'notification':
      case 'notification_resolved':
        void this.refreshNotifications(hostId)
        // A permission request writes waiting_permission to the session and
        // then announces only the notification (claude/hooks.go:104,142), so
        // refetching the list is the one way the sidebar hears about it.
        this.scheduleRefresh(hostId)
        return
      default:
        return
    }
  }

  /**
   * Refetches a host's sessions once the events stop arriving. An agent mid-turn
   * fires several in a row, and one list per event is what makes it feel laggy.
   */
  private scheduleRefresh(hostId: string): void {
    const pending = this.refreshTimers.get(hostId)
    if (pending) clearTimeout(pending)
    this.refreshTimers.set(
      hostId,
      setTimeout(() => {
        this.refreshTimers.delete(hostId)
        void this.refreshSessions(hostId)
      }, REFRESH_DEBOUNCE),
    )
  }

  private patchSession(hostId: string, sessionId: string, patch: Partial<Session>): void {
    const was = this.state.sessions[hostId]?.find((s) => s.session_id === sessionId)
    this.set((s) => {
      const list = s.sessions[hostId]
      if (!list) return {}
      return {
        sessions: {
          ...s.sessions,
          [hostId]: list.map((session) =>
            session.session_id === sessionId ? { ...session, ...patch } : session,
          ),
        },
      }
    })
    if (patch.status === 'terminated' && was && was.status !== 'terminated') {
      this.onTerminated(hostId, sessionId)
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

  setShowArchived(showArchived: boolean): void {
    this.set({ showArchived })
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
      status: { state: 'connecting' },
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
      return
    }
    const session = this.state.sessions[tab.hostId]?.find((s) => s.session_id === tab.sessionId)
    if (session) await this.openTerminal(tab.hostId, session, false)
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
    await this.refreshSessions(hostId)
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

  closeTab(tabId: string): void {
    void bridge.term.close(tabId)
    this.set((s) => ({
      tabs: s.tabs.filter((t) => t.id !== tabId),
      activeTabs: Object.fromEntries(Object.entries(s.activeTabs).filter(([, id]) => id !== tabId)),
    }))
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
