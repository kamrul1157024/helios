import { signJWT, type DeviceKey } from './keys.ts'
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
  ModelInfo,
  SessionGroup,
  Notification,
  ProviderInfo,
  Session,
  Subagent,
  TerminalInfo,
  TranscriptPage,
  HostStats,
  Worktree,
  WriteResult,
} from '../shared/models.ts'

/** Refresh a little before expiry so a request never races the clock. */
const TOKEN_LIFETIME = 3600
const TOKEN_REFRESH_MARGIN = 300
const REQUEST_TIMEOUT = 15_000
/** Past the daemon's own 45s ceiling, so its answer arrives before we give up. */
const TITLE_TIMEOUT = 60_000
/** An upload carries megabytes over a tunnel; the shared timeout is for JSON. */
const UPLOAD_TIMEOUT = 120_000

/** One file on its way up: the bytes, and what to call them at the far end. */
export interface UploadPart {
  name: string
  type: string
  bytes: Uint8Array<ArrayBuffer>
}

/** Where the daemon put it. The path is what goes on to the agent. */
export interface UploadedFile {
  name: string
  path: string
  size: number
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    /** The daemon's machine-readable error code, when it sent one. */
    readonly code?: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

/**
 * Authenticated HTTP client for one daemon.
 *
 * Mirrors mobile's ApiClient: cache a signed JWT, and on a 401 invalidate and
 * retry exactly once. One retry, not a loop — a genuinely revoked device would
 * otherwise spin.
 */
export class ApiClient {
  private token: string | null = null
  private tokenExpiry = 0

  constructor(
    public baseUrl: string,
    private key: DeviceKey,
  ) {}

  setKey(key: DeviceKey): void {
    this.key = key
    this.invalidate()
  }

  invalidate(): void {
    this.token = null
    this.tokenExpiry = 0
  }

  private getToken(): string {
    const now = Math.floor(Date.now() / 1000)
    if (this.token && this.tokenExpiry - TOKEN_REFRESH_MARGIN > now) return this.token
    this.token = signJWT(this.key, TOKEN_LIFETIME)
    this.tokenExpiry = now + TOKEN_LIFETIME
    return this.token
  }

  authHeader(): string {
    return `Bearer ${this.getToken()}`
  }

  async request<T>(method: string, path: string, body?: unknown, timeout = REQUEST_TIMEOUT): Promise<T> {
    let response = await this.send(method, path, body, timeout)
    if (response.status === 401) {
      this.invalidate()
      response = await this.send(method, path, body, timeout)
    }

    if (response.status === 204) return undefined as T

    const text = await response.text()
    const parsed = text ? safeParse(text) : undefined

    if (!response.ok) {
      const detail = (parsed ?? {}) as { error?: string; message?: string }
      throw new ApiError(
        response.status,
        detail.message ?? detail.error ?? `${method} ${path} failed with ${response.status}`,
        detail.error,
      )
    }
    return parsed as T
  }

  private async send(method: string, path: string, body?: unknown, timeout = REQUEST_TIMEOUT): Promise<Response> {
    const headers: Record<string, string> = { Authorization: this.authHeader() }
    if (body !== undefined) headers['Content-Type'] = 'application/json'

    return fetch(`${this.baseUrl}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(timeout),
    })
  }

  // ─── Sessions ──────────────────────────────────────────────────────────

  async listSessions(
    params: {
      q?: string
      status?: string
      filter?: string
      cwd?: string
      /** "1" asks the daemon to resolve each session's groups. */
      grouped?: string
      group_key?: string
    } = {},
  ): Promise<{ sessions: Session[]; host?: HostStats }> {
    const res = await this.request<{ sessions?: Session[]; host?: HostStats }>(
      'GET',
      `/api/sessions${queryString(params)}`,
    )
    return { sessions: res.sessions ?? [], host: res.host }
  }

  // ─── Groups ────────────────────────────────────────────────────────────

  async listGroups(): Promise<SessionGroup[]> {
    const res = await this.request<{ groups?: SessionGroup[] }>('GET', '/api/groups')
    return res.groups ?? []
  }

  async createGroup(name: string): Promise<SessionGroup> {
    return this.request('POST', '/api/groups', { name })
  }

  async renameGroup(key: string, name: string): Promise<void> {
    await this.request('PATCH', `/api/groups/${encodeURIComponent(key)}`, { name })
  }

  async deleteGroup(key: string): Promise<void> {
    await this.request('DELETE', `/api/groups/${encodeURIComponent(key)}`)
  }

  async setGroupOrder(order: string[]): Promise<void> {
    await this.request('POST', '/api/groups/order', { order })
  }

  /** Replaces the session's groups, outermost first. An empty list clears it. */
  async setSessionGroups(id: string, groups: string[]): Promise<void> {
    await this.request('PATCH', `/api/sessions/${encodeURIComponent(id)}`, { groups })
  }

  async getSession(id: string): Promise<{ session: Session; pending_permissions: number }> {
    return this.request('GET', `/api/sessions/${encodeURIComponent(id)}`)
  }

  async listDirectories(): Promise<DirectoryInfo[]> {
    const res = await this.request<{ directories?: DirectoryInfo[] }>(
      'GET',
      '/api/sessions/directories',
    )
    return res.directories ?? []
  }

  transcript(id: string, limit = 50, offset = 0): Promise<TranscriptPage> {
    const query = queryString({ limit: String(limit), offset: String(offset) })
    return this.request('GET', `/api/sessions/${encodeURIComponent(id)}/transcript${query}`)
  }

  /**
   * Asks only for what has been written since seq, under the epoch those seq
   * numbers came from. A reply with epoch_changed set is a whole page instead:
   * the transcript is no longer the one being followed.
   */
  transcriptSince(id: string, afterSeq: number, epoch: string, limit = 50): Promise<TranscriptPage> {
    const query = queryString({ after_seq: String(afterSeq), epoch, limit: String(limit) })
    return this.request('GET', `/api/sessions/${encodeURIComponent(id)}/transcript${query}`)
  }

  async subagents(id: string): Promise<Subagent[]> {
    const res = await this.request<{ subagents?: Subagent[] }>(
      'GET',
      `/api/sessions/${encodeURIComponent(id)}/subagents`,
    )
    return res.subagents ?? []
  }

  /**
   * Sends a prompt. A 409 is not a failure but an answer: the session is busy
   * and does not queue, or it is terminated. Callers need the code to say which.
   */
  /** Records that a human is looking at this session. Fire and forget. */
  touchSession(id: string): Promise<unknown> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/touch`)
  }

  sendPrompt(id: string, message: string): Promise<{ success: boolean; queued?: boolean }> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/send`, { message })
  }

  /**
   * Stores files beside the session and answers with the path of each.
   *
   * The bytes stop here. What reaches the agent is a path, which it opens with
   * its own tools — so an attachment costs the model nothing until it looks.
   */
  async uploadFiles(id: string, files: UploadPart[]): Promise<UploadedFile[]> {
    const path = `/api/sessions/${encodeURIComponent(id)}/files`
    let response = await this.sendForm(path, files)
    if (response.status === 401) {
      this.invalidate()
      response = await this.sendForm(path, files)
    }

    const text = await response.text()
    const parsed = (text ? safeParse(text) : undefined) as
      | { files?: UploadedFile[]; error?: string; message?: string }
      | undefined

    if (!response.ok) {
      throw new ApiError(
        response.status,
        parsed?.message ?? parsed?.error ?? `upload failed with ${response.status}`,
        parsed?.error,
      )
    }
    return parsed?.files ?? []
  }

  /** A form is rebuilt per attempt: a consumed body cannot be sent twice. */
  private sendForm(path: string, files: UploadPart[]): Promise<Response> {
    const form = new FormData()
    for (const file of files) {
      const blob = new Blob([file.bytes], { type: file.type || 'application/octet-stream' })
      form.append('file', blob, file.name)
    }
    // No Content-Type of ours: fetch writes it with the multipart boundary.
    return fetch(`${this.baseUrl}${path}`, {
      method: 'POST',
      headers: { Authorization: this.authHeader() },
      body: form,
      signal: AbortSignal.timeout(UPLOAD_TIMEOUT),
    })
  }

  /** The whole arrangement, first id first. See store.SetSessionOrder. */
  setSessionOrder(order: string[]): Promise<unknown> {
    return this.request('POST', '/api/sessions/order', { order })
  }

  stop(id: string): Promise<unknown> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/stop`)
  }

  terminate(id: string): Promise<unknown> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/terminate`)
  }

  resume(id: string): Promise<unknown> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/resume`)
  }

  /** Warms a cold session's terminal host and returns its socket path. */
  wake(id: string): Promise<{ success: boolean; terminal: string }> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/wake`)
  }

  /** Opens a login shell beside the session's agent, in its directory. */
  openShell(sessionId: string): Promise<TerminalInfo> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(sessionId)}/terminals`)
  }

  /** The session's live terminals: its agent first, then its shells. */
  async terminals(sessionId: string): Promise<TerminalInfo[]> {
    const res = await this.request<{ terminals?: TerminalInfo[] }>(
      'GET',
      `/api/sessions/${encodeURIComponent(sessionId)}/terminals`,
    )
    return res.terminals ?? []
  }

  /** Closes a shell. The daemon refuses this for a session's own agent. */
  killTerminal(terminalId: string): Promise<unknown> {
    return this.request('DELETE', `/api/terminals/${encodeURIComponent(terminalId)}`)
  }

  setPermissionMode(id: string, mode: string): Promise<unknown> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/permission-mode`, { mode })
  }

  /**
   * Waits for the model, which is why it gets its own budget.
   *
   * The daemon answers this one only once the title is written, and the model
   * behind it runs 3-6s on a good day and spikes well past that. Under the
   * shared 15s the desktop gave up first — "operation was aborted due to
   * timeout" — while the daemon carried on and set a title nobody was told
   * about. Longer here than the daemon's own ceiling, so whoever gives up
   * first, it is the side that knows why.
   */
  generateTitle(id: string): Promise<{ success: boolean; title?: string; error?: string }> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/title/generate`, undefined, TITLE_TIMEOUT)
  }

  patchSession(id: string, patch: Record<string, unknown>): Promise<unknown> {
    return this.request('PATCH', `/api/sessions/${encodeURIComponent(id)}`, patch)
  }

  deleteSession(id: string): Promise<unknown> {
    return this.request('DELETE', `/api/sessions/${encodeURIComponent(id)}`)
  }

  createSession(spec: {
    provider?: string
    prompt?: string
    cwd?: string
    model?: string
    permission_mode?: string
    dangerously_skip_permissions?: boolean
  }): Promise<{ success: boolean; session_id: string; terminal: string; cwd: string }> {
    return this.request('POST', '/api/sessions', spec)
  }

  // ─── Notifications ─────────────────────────────────────────────────────

  async notifications(params: { source?: string; status?: string; type?: string } = {}): Promise<Notification[]> {
    const res = await this.request<{ notifications?: Notification[] }>(
      'GET',
      `/api/notifications${queryString(params)}`,
    )
    return res.notifications ?? []
  }

  /**
   * Answers a notification. The body is passed straight to the provider's
   * action handler (internal/provider/claude/actions.go), so its shape depends
   * on the notification type — {action:'approve'}, {action:'answer', answers},
   * and so on. Typing it further here would only duplicate that dispatch.
   */
  notificationAction(id: string, body: Record<string, unknown>): Promise<{ success: boolean }> {
    return this.request('POST', `/api/notifications/${encodeURIComponent(id)}/action`, body)
  }

  dismissNotification(id: string): Promise<unknown> {
    return this.request('POST', `/api/notifications/${encodeURIComponent(id)}/dismiss`)
  }

  // ─── Git, files ────────────────────────────────────────────────────────

  gitStatus(path: string): Promise<GitStatus> {
    return this.request('GET', `/api/git/status${queryString({ path })}`)
  }

  /**
   * The working-tree diff for a file, or its diff at a revision: `to` alone is
   * that commit against its parent, `from` and `to` together are a range.
   */
  gitDiff(
    path: string,
    file: string,
    at: {
      from?: string
      to?: string
      staged?: boolean
      untracked?: boolean
      /** Compare against where the two revisions parted, as a review does. */
      mergeBase?: boolean
    } = {},
  ): Promise<GitDiff> {
    return this.request(
      'GET',
      `/api/git/diff${queryString({
        path,
        file,
        from: at.from,
        to: at.to,
        staged: at.staged ? 'true' : undefined,
        untracked: at.untracked ? 'true' : undefined,
        merge_base: at.mergeBase ? 'true' : undefined,
      })}`,
    )
  }

  gitLog(path: string, opts: { base?: string; all?: boolean; limit?: number; skip?: number } = {}): Promise<GitLog> {
    return this.request(
      'GET',
      `/api/git/log${queryString({
        path,
        base: opts.base,
        all: opts.all ? 'true' : undefined,
        limit: opts.limit,
        skip: opts.skip,
      })}`,
    )
  }

  gitChanges(path: string, to: string, from?: string, mergeBase?: boolean): Promise<GitChanges> {
    return this.request(
      'GET',
      `/api/git/changes${queryString({ path, to, from, merge_base: mergeBase ? 'true' : undefined })}`,
    )
  }

  async gitWorktrees(path: string): Promise<Worktree[]> {
    const res = await this.request<{ worktrees?: Worktree[] }>('GET', `/api/git/worktrees${queryString({ path })}`)
    return res.worktrees ?? []
  }

  async listFiles(path: string): Promise<{ path: string; entries: FileEntry[] }> {
    const res = await this.request<{ path: string; entries?: FileEntry[] }>(
      'GET',
      `/api/files${queryString({ path })}`,
    )
    return { path: res.path, entries: res.entries ?? [] }
  }

  async reviewedFiles(path: string, base: string): Promise<string[]> {
    const res = await this.request<{ files?: string[] }>(
      'GET',
      `/api/git/reviewed${queryString({ path, base })}`,
    )
    return res.files ?? []
  }

  setReviewed(path: string, base: string, file: string, reviewed: boolean): Promise<unknown> {
    return this.request('POST', '/api/git/reviewed', { path, base, file, reviewed })
  }

  readFile(path: string): Promise<FileContent> {
    return this.request('GET', `/api/file${queryString({ path })}`)
  }

  searchFiles(path: string, q: string, limit?: number): Promise<FileSearchResult> {
    const query = queryString({ path, q, limit: limit ? String(limit) : undefined })
    return this.request('GET', `/api/files/search${query}`)
  }

  grepFiles(
    path: string,
    q: string,
    opts: { regex?: boolean; caseSensitive?: boolean; limit?: number } = {},
  ): Promise<GrepResult> {
    const query = queryString({
      path,
      q,
      regex: opts.regex ? 'true' : undefined,
      case: opts.caseSensitive ? 'true' : undefined,
      limit: opts.limit ? String(opts.limit) : undefined,
    })
    return this.request('GET', `/api/files/grep${query}`)
  }

  /**
   * Saves a file. base_mod_time is what the editor last read; the daemon
   * refuses the write when the file has changed since, which is the common case
   * of the agent editing the same file mid-session.
   */
  writeFile(path: string, content: string, baseModTime?: string): Promise<WriteResult> {
    return this.request('PUT', '/api/file', { path, content, base_mod_time: baseModTime })
  }

  // ─── Meta ──────────────────────────────────────────────────────────────

  async providers(): Promise<ProviderInfo[]> {
    const res = await this.request<{ providers?: ProviderInfo[] }>('GET', '/api/providers')
    return res.providers ?? []
  }

  async models(provider: string): Promise<ModelInfo[]> {
    const res = await this.request<{ models?: ModelInfo[] }>(
      'GET',
      `/api/providers/${encodeURIComponent(provider)}/models`,
    )
    return res.models ?? []
  }

  async commands(): Promise<CommandInfo[]> {
    const res = await this.request<{ commands?: CommandInfo[] }>('GET', '/api/commands')
    return res.commands ?? []
  }

  settings(): Promise<Record<string, unknown>> {
    return this.request('GET', '/api/settings')
  }

  updateSettings(settings: Record<string, unknown>): Promise<unknown> {
    return this.request('POST', '/api/settings', settings)
  }

  async devices(): Promise<DeviceInfo[]> {
    const res = await this.request<{ devices?: DeviceInfo[] }>('GET', '/api/auth/devices')
    return res.devices ?? []
  }

  /** Unauthenticated — used to check a host is reachable before pairing. */
  async health(): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/api/health`, { signal: AbortSignal.timeout(5_000) })
      return res.ok
    } catch {
      return false
    }
  }
}

function queryString(params: Record<string, string | number | undefined>): string {
  const pairs = Object.entries(params).filter(([, v]) => v !== undefined && v !== '')
  if (pairs.length === 0) return ''
  return `?${pairs.map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`).join('&')}`
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return { message: text }
  }
}
