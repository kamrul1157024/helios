// Wire types for the daemon's REST API. These mirror the Go structs in
// internal/store and internal/server; field names are the JSON tags, not the
// Go names, and optional fields are optional here for the same reason they are
// pointers there.

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
  archived: boolean
  managed: boolean
  permission_mode?: string
  /** Socket path of the live terminal host, injected by the daemon. Absent means cold. */
  terminal?: string
  created_at: string
  ended_at?: string
  supports_prompt_queue: boolean
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

export function isLive(session: Session): boolean {
  return session.status !== 'ended' && session.status !== 'terminated'
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

export interface Worktree {
  path: string
  branch: string
  is_main: boolean
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

/** internal/provider/registry.go:107 */
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
