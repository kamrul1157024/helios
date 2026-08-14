import type {
  CommandInfo,
  DeviceInfo,
  DirectoryInfo,
  FileContent,
  FileEntry,
  FileSearchResult,
  GitChanges,
  GitDiff,
  GitLog,
  GitStatus,
  GrepResult,
  HostRecord,
  HostStatus,
  ModelInfo,
  Notification,
  NotificationPrefs,
  ProviderInfo,
  Session,
  SSEEvent,
  Subagent,
  TabStatus,
  TerminalInfo,
  TranscriptPage,
  Worktree,
  WriteResult,
} from '../shared/models.ts'

/**
 * Typed view of the preload bridge.
 *
 * The preload deals in `unknown` because it cannot import app types without
 * dragging them into the sandboxed context; this file is where the shapes are
 * asserted, once, instead of at every call site.
 */

type Unsubscribe = () => void

interface RawBridge {
  hosts: {
    list(): Promise<HostRecord[]>
    statuses(): Promise<HostStatus[]>
    pairLocal(name?: string): Promise<HostRecord>
    pairURL(url: string, name?: string): Promise<HostRecord>
    remove(id: string): Promise<void>
    rename(id: string, name: string): Promise<void>
    onChanged(fn: (payload: HostRecord[]) => void): Unsubscribe
    onStatus(fn: (payload: HostStatus) => void): Unsubscribe
    onEvent(fn: (payload: { hostId: string; event: SSEEvent }) => void): Unsubscribe
  }
  api: {
    call<T>(hostId: string, method: string, args?: unknown[]): Promise<T>
  }
  term: {
    open(req: {
      tabId: string
      hostId: string
      sessionId: string
      cols: number
      rows: number
      wake?: boolean
      /** Which of the session's terminals; absent means its agent. */
      terminalId?: string
    }): Promise<void>
    input(tabId: string, data: Uint8Array): Promise<void>
    resize(tabId: string, cols: number, rows: number): Promise<void>
    close(tabId: string): Promise<void>
    status(tabId: string): Promise<TabStatus | undefined>
    onOutput(fn: (payload: { tabId: string; data: Uint8Array }) => void): Unsubscribe
    onStatus(fn: (payload: { tabId: string; status: TabStatus }) => void): Unsubscribe
    onExited(fn: (payload: { tabId: string; code: number }) => void): Unsubscribe
    onClosed(fn: (payload: { tabId: string; reason: string }) => void): Unsubscribe
  }
  prefs: {
    get(): Promise<NotificationPrefs>
    setSound(enabled: boolean): Promise<NotificationPrefs>
    setAlert(type: string, enabled: boolean): Promise<NotificationPrefs>
    reset(): Promise<NotificationPrefs>
  }
  hud: {
    resize(height: number): void
    dismiss(): void
    activate(target: { hostId: string; notificationId: string; sessionId: string }): void
    resolved(key: string): void
    onPresent(fn: (card: { hostId: string; hostName?: string; notification: Notification }) => void): Unsubscribe
    onRetract(fn: (key: string) => void): Unsubscribe
  }
  app: {
    onActivateNotification(
      fn: (payload: { hostId: string; sessionId: string; notificationId: string; command?: string }) => void,
    ): Unsubscribe
    onOpenPairing(fn: (url: string) => void): Unsubscribe
    onOpenSettings(fn: () => void): Unsubscribe
  }
}

declare global {
  interface Window {
    helios: RawBridge
  }
}

export const bridge: RawBridge = window.helios

/** Errors from the main process carry the daemon's HTTP status when it had one. */
export interface BridgeError extends Error {
  status?: number
  code?: string
}

export function statusOf(err: unknown): number | undefined {
  return (err as BridgeError | undefined)?.status
}

/** One daemon's REST surface, bound to a host id. */
export class HostApi {
  constructor(readonly hostId: string) {}

  private call<T>(method: string, ...args: unknown[]): Promise<T> {
    return bridge.api.call<T>(this.hostId, method, args)
  }

  listSessions(params: { q?: string; status?: string; filter?: string; cwd?: string } = {}): Promise<Session[]> {
    return this.call('listSessions', params)
  }
  getSession(id: string): Promise<{ session: Session; pending_permissions: number }> {
    return this.call('getSession', id)
  }
  listDirectories(): Promise<DirectoryInfo[]> {
    return this.call('listDirectories')
  }
  transcript(id: string, limit?: number, offset?: number): Promise<TranscriptPage> {
    return this.call('transcript', id, limit, offset)
  }
  subagents(id: string): Promise<Subagent[]> {
    return this.call('subagents', id)
  }
  sendPrompt(id: string, message: string): Promise<{ success: boolean; queued?: boolean }> {
    return this.call('sendPrompt', id, message)
  }
  stop(id: string): Promise<unknown> {
    return this.call('stop', id)
  }
  terminate(id: string): Promise<unknown> {
    return this.call('terminate', id)
  }
  resume(id: string): Promise<unknown> {
    return this.call('resume', id)
  }
  wake(id: string): Promise<{ success: boolean; terminal: string }> {
    return this.call('wake', id)
  }
  openShell(sessionId: string): Promise<TerminalInfo> {
    return this.call('openShell', sessionId)
  }
  terminals(sessionId: string): Promise<TerminalInfo[]> {
    return this.call('terminals', sessionId)
  }
  killTerminal(terminalId: string): Promise<unknown> {
    return this.call('killTerminal', terminalId)
  }
  setPermissionMode(id: string, mode: string): Promise<unknown> {
    return this.call('setPermissionMode', id, mode)
  }
  generateTitle(id: string): Promise<unknown> {
    return this.call('generateTitle', id)
  }
  patchSession(id: string, patch: Record<string, unknown>): Promise<unknown> {
    return this.call('patchSession', id, patch)
  }
  deleteSession(id: string): Promise<unknown> {
    return this.call('deleteSession', id)
  }
  createSession(spec: {
    provider?: string
    prompt?: string
    cwd?: string
    model?: string
    permission_mode?: string
    dangerously_skip_permissions?: boolean
  }): Promise<{ success: boolean; session_id: string; terminal: string; cwd: string }> {
    return this.call('createSession', spec)
  }
  notifications(params: { source?: string; status?: string; type?: string } = {}): Promise<Notification[]> {
    return this.call('notifications', params)
  }
  notificationAction(id: string, body: Record<string, unknown>): Promise<{ success: boolean }> {
    return this.call('notificationAction', id, body)
  }
  dismissNotification(id: string): Promise<unknown> {
    return this.call('dismissNotification', id)
  }
  gitStatus(path: string): Promise<GitStatus> {
    return this.call('gitStatus', path)
  }
  gitDiff(
    path: string,
    file: string,
    at?: { from?: string; to?: string; staged?: boolean; untracked?: boolean; mergeBase?: boolean },
  ): Promise<GitDiff> {
    return this.call('gitDiff', path, file, at)
  }
  gitLog(path: string, opts?: { base?: string; all?: boolean; limit?: number; skip?: number }): Promise<GitLog> {
    return this.call('gitLog', path, opts)
  }
  gitChanges(path: string, to: string, from?: string, mergeBase?: boolean): Promise<GitChanges> {
    return this.call('gitChanges', path, to, from, mergeBase)
  }
  gitWorktrees(path: string): Promise<Worktree[]> {
    return this.call('gitWorktrees', path)
  }
  listFiles(path: string): Promise<{ path: string; entries: FileEntry[] }> {
    return this.call('listFiles', path)
  }
  readFile(path: string): Promise<FileContent> {
    return this.call('readFile', path)
  }
  searchFiles(path: string, q: string, limit?: number): Promise<FileSearchResult> {
    return this.call('searchFiles', path, q, limit)
  }
  grepFiles(
    path: string,
    q: string,
    opts: { regex?: boolean; caseSensitive?: boolean; limit?: number } = {},
  ): Promise<GrepResult> {
    return this.call('grepFiles', path, q, opts)
  }
  writeFile(path: string, content: string, baseModTime?: string): Promise<WriteResult> {
    return this.call('writeFile', path, content, baseModTime)
  }
  providers(): Promise<ProviderInfo[]> {
    return this.call('providers')
  }
  models(provider: string): Promise<ModelInfo[]> {
    return this.call('models', provider)
  }
  commands(): Promise<CommandInfo[]> {
    return this.call('commands')
  }
  settings(): Promise<Record<string, unknown>> {
    return this.call('settings')
  }
  updateSettings(settings: Record<string, unknown>): Promise<unknown> {
    return this.call('updateSettings', settings)
  }
  devices(): Promise<DeviceInfo[]> {
    return this.call('devices')
  }
}

const apis = new Map<string, HostApi>()

/** Cached so `api(hostId)` is stable enough to sit in a dependency array. */
export function api(hostId: string): HostApi {
  let existing = apis.get(hostId)
  if (!existing) {
    existing = new HostApi(hostId)
    apis.set(hostId, existing)
  }
  return existing
}
