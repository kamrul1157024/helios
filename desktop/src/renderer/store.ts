import { useSyncExternalStore } from 'react'

import { api, bridge, statusOf } from './bridge.ts'
import { hasTerminal } from '../shared/models.ts'
import type { HostRecord, HostStatus, Notification, Session, SSEEvent, TabStatus } from '../shared/models.ts'

export interface Tab {
  id: string
  hostId: string
  sessionId: string
  title: string
  status: TabStatus
}

/** One terminal per session, so the session it belongs to is its identity. */
export function terminalId(hostId: string, sessionId: string): string {
  return `${hostId}:${sessionId}`
}

export interface Selection {
  hostId: string
  sessionId: string
}

export type RightPanel = 'chat' | 'terminal' | 'approvals' | 'git' | 'files'

/**
 * A file the user asked to see, from a chip in the transcript. The counter is
 * what makes clicking the same chip twice reopen it: the path alone would not
 * change, and the panel would ignore it.
 */
export interface FileTarget {
  hostId: string
  path: string
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
  panel: RightPanel
  fileTarget: FileTarget | null
  /** Sidebar filter, matched against title, project and cwd. */
  query: string
  showArchived: boolean
  loading: boolean
  toast: { text: string; kind: 'info' | 'error' } | null
  pairingLink: string | null
}

const initial: State = {
  hosts: [],
  hostStatus: {},
  sessions: {},
  notifications: {},
  tabs: [],
  selection: null,
  panel: 'chat',
  fileTarget: null,
  query: '',
  showArchived: false,
  loading: true,
  toast: null,
  pairingLink: null,
}

type Listener = () => void

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
      this.set({ panel: 'approvals' })
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
    await Promise.all([this.refreshSessions(hostId), this.refreshNotifications(hostId)])
  }

  async refreshSessions(hostId: string): Promise<void> {
    try {
      const sessions = await api(hostId).listSessions()
      this.set((s) => ({ sessions: { ...s.sessions, [hostId]: sessions } }))
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
   * Applies a server event in place where the payload allows it, and falls back
   * to a refetch where it does not. Refetching everything on every event is
   * what makes a busy agent feel laggy.
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
        return
      }
      case 'session_updated':
      case 'session_deleted':
        void this.refreshSessions(hostId)
        return
      case 'notification':
      case 'notification_resolved':
        void this.refreshNotifications(hostId)
        return
      default:
        return
    }
  }

  private patchSession(hostId: string, sessionId: string, patch: Partial<Session>): void {
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
  }

  // ─── Selection and panels ──────────────────────────────────────────────

  select(hostId: string, sessionId: string): void {
    this.set({ selection: { hostId, sessionId } })
  }

  setPanel(panel: RightPanel): void {
    this.set({ panel })
  }

  /** Shows a file in the Files panel, switching to it. */
  openFile(hostId: string, path: string): void {
    this.set((s) => ({
      panel: 'files',
      fileTarget: { hostId, path, seq: (s.fileTarget?.seq ?? 0) + 1 },
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
    this.set({ selection: { hostId, sessionId: session.session_id }, panel: 'terminal' })

    const id = terminalId(hostId, session.session_id)
    if (this.state.tabs.some((t) => t.id === id)) return

    const tab: Tab = {
      id,
      hostId,
      sessionId: session.session_id,
      title: session.title ?? session.project ?? session.session_id.slice(0, 8),
      status: { state: 'connecting' },
    }
    this.set((s) => ({ tabs: [...s.tabs, tab] }))

    try {
      // Geometry is a placeholder; the tab reports its real size once xterm has
      // measured the container, which cannot happen before it is mounted.
      await bridge.term.open({
        tabId: tab.id,
        hostId,
        sessionId: session.session_id,
        cols: 80,
        rows: 24,
        wake,
      })
    } catch (err) {
      this.set((s) => ({ tabs: s.tabs.filter((t) => t.id !== tab.id) }))
      this.fail(err)
    }
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
    this.set({ panel: 'terminal' })
    if (this.state.tabs.some((t) => t.id === terminalId(hostId, session.session_id))) return
    if (hasTerminal(session)) void this.openTerminal(hostId, session, false)
  }

  closeTab(tabId: string): void {
    void bridge.term.close(tabId)
    this.set((s) => ({ tabs: s.tabs.filter((t) => t.id !== tabId) }))
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

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}
