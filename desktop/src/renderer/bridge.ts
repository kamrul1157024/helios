import type { SegmentId } from '../shared/status-line.ts'
import type { HeliosTheme, XtermTheme } from '../shared/theme/resolve.ts'
import type { BackdropSpec } from '../shared/theme/vscode.ts'

/** What the main process hands over whenever the appearance changes. */
export interface ThemePayload {
  theme: HeliosTheme
  terminal: XtermTheme
  /** A backdrop is on: some surfaces stop painting so it shows through. */
  glass: boolean
  /** This platform can show it at all; false hides the toggle entirely. */
  glassSupported: boolean
  /** Reading size for rendered markdown, in px. */
  proseSize: number
  density: Density
  /** Which segments the session status line draws, in the order it draws them. */
  statusLine: SegmentId[]
}
import type {
  AppearancePrefs,
  BackdropState,
  CommandInfo,
  Density,
  DeviceInfo,
  DiffAt,
  DirectoryInfo,
  FileContent,
  FileEntry,
  FileSearchResult,
  GitChanges,
  GitDiff,
  GitLog,
  GitStatus,
  GrepOpts,
  GrepResult,
  LogOpts,
  HostRecord,
  HostStats,
  HostStatus,
  ModelInfo,
  Notification,
  NotificationPrefs,
  ProviderInfo,
  SSEEvent,
  Session,
  CheckResult,
  Schedule,
  SessionGroup,
  Subagent,
  TabStatus,
  TerminalInfo,
  ThemeSummary,
  TranscriptPage,
  UpdateInfo,
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

/** One file on its way up: the bytes, and what to call them at the far end. */
export interface UploadPart {
  name: string
  type: string
  bytes: Uint8Array
}

/** Where the daemon put it. The path is what goes on to the agent. */
export interface UploadedFile {
  name: string
  path: string
  size: number
}

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
  preview: {
    /** Stages an HTML page and returns the URL to point a frame at. */
    stage(html: string): Promise<string>
  }
  prefs: {
    get(): Promise<NotificationPrefs>
    setSound(enabled: boolean): Promise<NotificationPrefs>
    setAlert(type: string, enabled: boolean): Promise<NotificationPrefs>
    reset(): Promise<NotificationPrefs>
  }
  theme: {
    /** The theme the preload already painted, before this code ran. */
    boot(): ThemePayload
    list(): Promise<ThemeSummary[]>
    prefs(): Promise<AppearancePrefs>
    set(next: Partial<AppearancePrefs>): Promise<ThemePayload>
    reload(): Promise<ThemeSummary[]>
    /** The active theme's backdrop, which lives in the theme file itself. */
    backdrop(): Promise<BackdropState>
    setBackdrop(spec: BackdropSpec): Promise<BackdropState>
    /** Opens a file dialog, imports the chosen image, and switches to it. */
    pickBackdropImage(): Promise<BackdropState>
    onChanged(fn: (payload: ThemePayload) => void): Unsubscribe
  }
  hud: {
    resize(height: number): void
    dismiss(): void
    activate(target: { hostId: string; notificationId: string; sessionId: string }): void
    resolved(key: string): void
    onPresent(fn: (card: { hostId: string; hostName?: string; notification: Notification }) => void): Unsubscribe
    onRetract(fn: (key: string) => void): Unsubscribe
  }
  updates: {
    /** The release worth mentioning, or null when there is nothing new. */
    check(): Promise<UpdateInfo | null>
    /** Stops this version being mentioned again on this machine. */
    dismiss(version: string): Promise<void>
  }
  app: {
    /** Quits for real; closing the window only hides it behind the tray. */
    quit(): Promise<void>
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

export { statusOf, type BridgeError } from './errors.ts'

/** One daemon's REST surface, bound to a host id. */
export class HostApi {
  constructor(readonly hostId: string) {}

  private call<T>(method: string, ...args: unknown[]): Promise<T> {
    return bridge.api.call<T>(this.hostId, method, args)
  }

  listSessions(
    params: {
      q?: string
      status?: string
      filter?: string
      cwd?: string
      grouped?: string
      group_key?: string
      schedule_id?: string
    } = {},
  ): Promise<{ sessions: Session[]; host?: HostStats }> {
    return this.call('listSessions', params)
  }
  // ─── Schedules ─────────────────────────────────────────────────────────

  listSchedules(): Promise<Schedule[]> {
    return this.call('listSchedules')
  }
  createSchedule(fields: Partial<Schedule>): Promise<Schedule> {
    return this.call('createSchedule', fields)
  }
  updateSchedule(id: string, fields: Partial<Schedule>): Promise<Schedule> {
    return this.call('updateSchedule', id, fields)
  }
  deleteSchedule(id: string): Promise<void> {
    return this.call('deleteSchedule', id)
  }
  runSchedule(id: string): Promise<void> {
    return this.call('runSchedule', id)
  }
  checkSchedule(id: string): Promise<CheckResult> {
    return this.call('checkSchedule', id)
  }
  scheduleLog(id: string, tail = 200): Promise<string[]> {
    return this.call('scheduleLog', id, tail)
  }

  listGroups(): Promise<SessionGroup[]> {
    return this.call('listGroups')
  }
  createGroup(name: string, parent = ''): Promise<SessionGroup> {
    return this.call('createGroup', name, parent)
  }
  moveGroup(key: string, parent: string): Promise<void> {
    return this.call('moveGroup', key, parent)
  }
  renameGroup(key: string, name: string): Promise<void> {
    return this.call('renameGroup', key, name)
  }
  deleteGroup(key: string): Promise<void> {
    return this.call('deleteGroup', key)
  }
  setGroupOrder(parent: string, order: string[]): Promise<void> {
    return this.call('setGroupOrder', parent, order)
  }
  setSessionGroup(id: string, group: string): Promise<void> {
    return this.call('setSessionGroup', id, group)
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
  transcriptSince(id: string, afterSeq: number, epoch: string, limit?: number): Promise<TranscriptPage> {
    return this.call('transcriptSince', id, afterSeq, epoch, limit)
  }
  subagents(id: string): Promise<Subagent[]> {
    return this.call('subagents', id)
  }
  sendPrompt(id: string, message: string): Promise<{ success: boolean; queued?: boolean }> {
    return this.call('sendPrompt', id, message)
  }
  touchSession(id: string): Promise<unknown> {
    return this.call('touchSession', id)
  }
  /** The bytes cross to the main process, which does the multipart POST. */
  uploadFiles(id: string, files: UploadPart[]): Promise<UploadedFile[]> {
    return this.call('uploadFiles', id, files)
  }
  setSessionOrder(order: string[]): Promise<unknown> {
    return this.call('setSessionOrder', order)
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
  /** Answers only once the model has, so the caller can report what it said. */
  generateTitle(id: string): Promise<{ success: boolean; title?: string; error?: string }> {
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
  gitDiff(path: string, file: string, at?: DiffAt): Promise<GitDiff> {
    return this.call('gitDiff', path, file, at)
  }
  gitLog(path: string, opts?: LogOpts): Promise<GitLog> {
    return this.call('gitLog', path, opts)
  }
  gitChanges(path: string, to: string, from?: string, mergeBase?: boolean): Promise<GitChanges> {
    return this.call('gitChanges', path, to, from, mergeBase)
  }
  gitWorktrees(path: string): Promise<Worktree[]> {
    return this.call('gitWorktrees', path)
  }
  reviewedFiles(path: string, base: string): Promise<string[]> {
    return this.call('reviewedFiles', path, base)
  }
  setReviewed(path: string, base: string, file: string, reviewed: boolean): Promise<unknown> {
    return this.call('setReviewed', path, base, file, reviewed)
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
  grepFiles(path: string, q: string, opts: GrepOpts = {}): Promise<GrepResult> {
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
