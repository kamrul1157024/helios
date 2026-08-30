// Wire types for the daemon's REST API. These mirror the Go structs in
// internal/store and internal/server; field names are the JSON tags, not the
// Go names, and optional fields are optional here for the same reason they are
// pointers there.

import type { BackdropStyle } from './theme/vscode.ts'

export interface Session {
  session_id: string
  source: string
  cwd: string
  project: string
  title?: string
  transcript_path?: string
  model?: string
  status: SessionStatus
  last_event?: string
  last_event_at?: string
  last_user_message?: string
  pinned: boolean
  /** Place in a hand-arranged list; only meaningful in manual sort mode.
   *  Absent from a daemon older than the feature, which sorts by activity. */
  sort_order?: number
  permission_mode?: string
  /** Socket path of the live terminal host, injected by the daemon. Absent means cold. */
  terminal?: string
  /** Resident memory of the live terminal's process tree. Absent when cold. */
  memory_bytes?: number
  created_at: string
  ended_at?: string
  supports_prompt_queue: boolean
  /** The one group this session is filed under. */
  group_key?: string
  /** That group and its ancestors, outermost first, resolved by the daemon so
   *  no client walks the tree itself. Absent unless the request asked for
   *  grouping, so a client that does not group is served what it always was. */
  group_path?: SessionGroup[]
}

/** One node of the grouping tree. Position is its place among its own siblings,
 *  so two groups under different parents may share a number. */
export interface SessionGroup {
  key: string
  name: string
  position: number
  /** Empty for a root. Present on the catalogue, absent on a resolved path. */
  parent?: string
}

export type SessionStatus =
  | 'starting'
  | 'active'
  | 'idle'
  | 'waiting_permission'
  | 'waiting_input'
  | 'compacting'
  | 'ended'
  | 'terminated'
  | 'error'

/** Statuses in which the agent is mid-turn and a restart would lose work. */
export const BUSY_STATUSES: ReadonlySet<string> = new Set([
  'active',
  'starting',
  'compacting',
  'waiting_permission',
])

/** A live terminal host is attached. Absent means the session is cold. */
export function hasTerminal(session: Session): boolean {
  return Boolean(session.terminal)
}

/**
 * The one final state. The daemon refuses prompts for a terminated session
 * outright (internal/server/api.go, session-send returns 409 session_terminated)
 * and POST /resume is the only way back — waking it would revive the process
 * but leave the record terminated, so every prompt would still be refused.
 */
export function isTerminated(session: Session): boolean {
  return session.status === 'terminated'
}

/**
 * Alive on paper with no host to run in: evicted, or the daemon restarted
 * under it. Nothing resurrects a host on its own, but anything that needs one
 * — a prompt, a terminal, a resume — brings it back in place.
 *
 * Mirrors Session.needsRecovery in mobile/lib/models/session.dart.
 */
export function needsRecovery(session: Session): boolean {
  return !hasTerminal(session) && !isTerminated(session)
}

/** Resume relaunches the agent and moves the session back to idle. */
export function canResume(session: Session): boolean {
  return isTerminated(session)
}

/** Display label: the generated title, else the last prompt, else the directory. */
export function sessionLabel(session: Session): string {
  const title = session.title?.trim()
  if (title) return title
  const message = session.last_user_message?.trim()
  if (message) return message.length > 60 ? `${message.slice(0, 60)}…` : message
  return session.project || session.cwd || session.session_id.slice(0, 8)
}

/** Status wording, matching the mobile app's session cards. */
export function statusLabel(status: string): string {
  switch (status) {
    case 'waiting_permission':
      return 'Needs approval'
    case 'waiting_input':
      return 'Needs input'
    default:
      return status.charAt(0).toUpperCase() + status.slice(1)
  }
}

/**
 * The model, without what the provider beside it already said.
 *
 * The ids are qualified for an API, not for a list: `claude-opus-5` under a
 * provider called `claude` spends half its width repeating the word next to
 * it, and `claude-haiku-4-5-20251001` spends the rest on a release date that
 * distinguishes nothing here. Callers keep the full id in a tooltip.
 */
export function shortModel(model: string, source: string): string {
  const withoutVendor = model.startsWith(`${source}-`) ? model.slice(source.length + 1) : model
  // A trailing release date, but not a version: 20251001 is a date, 4-5 is not.
  return withoutVendor.replace(/-\d{8}$/, '')
}

/** Permission modes as a list can afford to say them. */
export function shortMode(mode: string): string {
  switch (mode) {
    case 'acceptEdits':
      return 'accept'
    case 'bypassPermissions':
      return 'bypass'
    default:
      return mode
  }
}

/** Compact relative time — "3m", "2h", "5d" — as on the mobile cards. */
export function timeAgo(iso?: string): string {
  if (!iso) return ''
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return ''
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.round(hours / 24)}d`
}

/** The last two path segments, which is as much as a narrow card can show. */
export function shortCwd(cwd: string): string {
  const parts = cwd.split('/').filter(Boolean)
  return parts.length <= 2 ? cwd : `…/${parts.slice(-2).join('/')}`
}

/** internal/store/sessions.go:258 — one directory Helios has seen sessions in. */
export interface DirectoryInfo {
  cwd: string
  project: string
  session_count: number
  active_count: number
}

/** internal/store/notifications.go:8. `type` is provider-scoped, e.g. claude.permission. */
export interface Notification {
  id: string
  source: string
  source_session: string
  cwd: string
  type: string
  status: 'pending' | 'approved' | 'denied' | 'answered' | 'dismissed' | 'expired'
  title?: string
  detail?: string
  /** JSON blob whose shape depends on `type`; parsed lazily by the panel. */
  payload?: string
  response?: string
  resolved_at?: string
  resolved_source?: string
  created_at: string
  /** Injected into SSE notification events so a client can tell if the session is live. */
  terminal?: string
}

/** Payload of a claude.permission notification, once parsed. */
export interface PermissionPayload {
  tool_name?: string
  tool_input?: Record<string, unknown>
  session_id?: string
  cwd?: string
  suggestions?: unknown
}

/** Payload of a claude.question notification, once parsed. */
export interface QuestionPayload {
  questions?: {
    header?: string
    question: string
    multi_select?: boolean
    options?: { label: string; description?: string }[]
  }[]
}

/** internal/transcript/reader.go:22 */
export interface TranscriptMessage {
  /** Position in the whole transcript, not in the page. */
  seq: number
  role: 'user' | 'assistant' | 'tool' | 'system' | string
  content?: string
  tool?: string
  summary?: string
  success?: boolean
  metadata?: Record<string, unknown>
  timestamp: string
}

export interface TranscriptPage {
  messages: TranscriptMessage[]
  total: number
  returned: number
  offset: number
  has_more: boolean
  /** Which parse the seq numbers count against. */
  epoch?: string
  /**
   * Set when a delta was asked for under an epoch that no longer holds. The
   * messages are then a fresh newest page and replace what the caller holds.
   */
  epoch_changed?: boolean
}

/** internal/store/sessions.go:64 */
export interface Subagent {
  agent_id: string
  parent_session_id: string
  agent_type?: string
  description?: string
  status: string
  transcript_path?: string
  created_at: string
  ended_at?: string
}

/** internal/server/git.go:11 */
export interface GitChange {
  path: string
  status: string
}

export interface GitStatus {
  root: string
  branch: string
  dirty: boolean
  ahead: number
  behind: number
  staged: GitChange[]
  unstaged: GitChange[]
  untracked: GitChange[]
}

export interface GitDiff {
  file: string
  language: string
  diff: string
  stat: string
}

/**
 * One live terminal host. A session's agent runs in the one whose id is the
 * session id; shells the user opens beside it carry that id with an index
 * appended, which is how the daemon tells them apart without a second table.
 */
export interface TerminalInfo {
  id: string
  parent: string
  kind: 'agent' | 'shell'
  socket: string
  cwd: string
  pid: number
}

export interface Worktree {
  path: string
  branch: string
  is_main: boolean
  head?: string
  subject?: string
  /** ISO-8601 date of the last commit — what "last touched" orders by. */
  date?: string
  detached: boolean
  locked: boolean
  ahead: number
  behind: number
  /** A count of changed files, not a flag: "7 touched" says an agent is mid-flight. */
  dirty: number
  /** What ahead and behind were measured against. */
  base?: string
}

/**
 * Most recently committed first. Worktrees with no date — pruned, or past the
 * detail cap the daemon enforces — keep their listing order at the end.
 */
export function byLastTouched(worktrees: Worktree[]): Worktree[] {
  return worktrees
    .map((worktree, index) => ({ worktree, index }))
    .sort((a, b) => {
      const da = Date.parse(a.worktree.date ?? '')
      const db = Date.parse(b.worktree.date ?? '')
      if (Number.isNaN(da) || Number.isNaN(db)) {
        if (Number.isNaN(da) && Number.isNaN(db)) return a.index - b.index
        return Number.isNaN(da) ? 1 : -1
      }
      return db - da || a.index - b.index
    })
    .map((entry) => entry.worktree)
}

/** Matches a worktree on branch, path or last commit subject. */
export function matchesWorktree(worktree: Worktree, query: string): boolean {
  const needle = query.trim().toLowerCase()
  if (!needle) return true
  return (
    worktree.branch.toLowerCase().includes(needle) ||
    worktree.path.toLowerCase().includes(needle) ||
    (worktree.subject ?? '').toLowerCase().includes(needle)
  )
}

/** internal/server/githistory.go:35 */
export interface Commit {
  sha: string
  short: string
  author: string
  date: string
  subject: string
  files: number
  insertions: number
  deletions: number
}

export interface GitLog {
  root: string
  branch: string
  base: string
  /** 'branch' is base..HEAD; 'all' is the whole history. */
  scope: 'branch' | 'all'
  commits: Commit[]
  has_more: boolean
}

/** Which revision of a file a diff is asked for. */
export interface DiffAt {
  from?: string
  to?: string
  staged?: boolean
  untracked?: boolean
  mergeBase?: boolean
}

export interface LogOpts {
  base?: string
  all?: boolean
  limit?: number
  skip?: number
}

export interface GrepOpts {
  regex?: boolean
  caseSensitive?: boolean
  limit?: number
}

export interface CommitFile {
  path: string
  /** Set on a rename or a copy. */
  from?: string
  status: string
  insertions: number
  deletions: number
}

export interface GitChanges {
  from: string
  to: string
  single: boolean
  subject?: string
  body?: string
  author?: string
  date?: string
  parents?: string[]
  files: CommitFile[]
  insertions: number
  deletions: number
  truncated: boolean
}

/** internal/server/files.go:16 */
export interface FileEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
}

export interface FileContent {
  path: string
  size: number
  mod_time: string
  content: string
}

/** internal/server/filesearch.go — one quick-open candidate. */
export interface FileMatch {
  path: string
  rel: string
  score: number
}

export interface FileSearchResult {
  root: string
  matches: FileMatch[]
  scanned: number
  truncated: boolean
}

/** internal/server/filesearch.go — one find-in-files hit. */
export interface GrepMatch {
  path: string
  rel: string
  line: number
  column: number
  text: string
}

export interface GrepResult {
  root: string
  matches: GrepMatch[]
  truncated: boolean
}

export interface WriteResult {
  path: string
  size: number
  mod_time: string
}

/** internal/provider/provider.go */
export interface ProviderInfo {
  id: string
  name: string
  icon: string
  capabilities: { prompt_queue: boolean }
  /**
   * Served rather than hardcoded: the vocabulary is the CLI's, and it has
   * already gained a mode between releases.
   */
  permission_modes?: string[]
  /**
   * Whether a session started now would work: the agent installed, its hooks
   * written and current. Session creation offers only ready providers, so a
   * user is never given a choice that fails; `helios start` shows them all
   * and uses `blocker` to say what is missing.
   */
  ready?: boolean
  blocker?: string
  hint?: string
}

export interface ModelInfo {
  id: string
  name: string
  description: string
  context_window?: string
}

export interface CommandInfo {
  name: string
  description?: string
  provider?: string
}

export interface DeviceInfo {
  kid: string
  name?: string
  platform?: string
  status: string
  created_at?: string
  last_seen?: string
}

/** A daemon the app is connected to. The device key lives in safeStorage, not here. */
export interface HostRecord {
  id: string
  name: string
  url: string
  device_id: string
  /** True when the daemon is on this machine, so terminals dial the unix socket. */
  local: boolean
}

export type HostConnectionState = 'connecting' | 'online' | 'offline' | 'unpaired'

export interface HostStatus {
  id: string
  state: HostConnectionState
  error?: string
}

/** Terminal tab lifecycle, as reported to the renderer. */
export interface TabStatus {
  state: 'connecting' | 'live' | 'reconnecting' | 'closed'
  hostState?: string
  writer?: string
  viewers?: number
  cols?: number
  rows?: number
  detail?: string
}

/**
 * internal/server/sse.go:11. The daemon writes the type as the SSE event name
 * and the payload as data, so `data` is the Go Data field verbatim.
 */
export interface SSEEvent {
  type: string
  data: Record<string, unknown>
}

/**
 * Per-machine notification preferences. Alerting only: a silenced type still
 * raises its HUD card and its tray entry, it just makes no sound.
 */
export interface NotificationPrefs {
  sound: boolean
  alerts: Record<string, boolean>
}

/**
 * Per-machine appearance. Two theme slots rather than one so that following the
 * OS has both a light and a dark answer to give; `mode` decides which slot is
 * read, and 'system' hands that decision to the OS.
 */
export interface AppearancePrefs {
  mode: 'system' | 'light' | 'dark'
  lightTheme: string
  darkTheme: string
  /** A theme id, or 'match' to follow whichever UI theme is active. */
  terminalTheme: string
  /** Body size of rendered markdown, in px; headings and tables scale with it. */
  proseSize: number
  /** How much of a session the sidebar shows: everything, or one line each. */
  density: Density
}

export type Density = 'comfortable' | 'compact'

/**
 * What the backdrop picker shows, for whichever theme is active.
 *
 * Not part of AppearancePrefs: the backdrop is saved into the theme file, since
 * a gradient drawn from one theme's palette means nothing under another.
 */
export interface BackdropState {
  themeId: string
  themeName: string
  /** False for an opaque theme, where a backdrop would never be seen. */
  glass: boolean
  style: BackdropStyle
  intensity: number
  /** How far the glass surfaces blur what is behind them, in px. */
  blur: number
  /** What the gradients are drawn from, whether named by the theme or derived. */
  palette: string[]
  /** True when those colours were named rather than derived from the theme. */
  custom: boolean
  /** File name of the chosen image, or null where none has been imported. */
  image: string | null
  /** Whether this platform can show the desktop behind the window. */
  desktopSupported: boolean
}

/** A theme as the picker lists it; `swatch` is a handful of representative colours. */
export interface ThemeSummary {
  id: string
  name: string
  mode: 'dark' | 'light'
  swatch: string[]
}

/** What a host's warm pool costs, and what the machine behind it has left.
 *  The daemon evicts nothing, so `budget` is only the point past which it is
 *  worth telling the user. */
export interface HostStats {
  warm: number
  warm_rss: number
  budget: number
  /** One-minute load average over the core count: 1 means saturated. */
  load: number
  memory_used: number
  memory_total: number
}

/** A release newer than what is running, and where to get it. */
export interface UpdateInfo {
  version: string
  url: string
}
