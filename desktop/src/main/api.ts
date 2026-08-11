import { signJWT, type DeviceKey } from './keys.ts'
import type {
  CommandInfo,
  DeviceInfo,
  FileContent,
  FileEntry,
  FileSearchResult,
  GitDiff,
  GitStatus,
  GrepResult,
  ModelInfo,
  Notification,
  ProviderInfo,
  Session,
  Subagent,
  TranscriptPage,
  Worktree,
  WriteResult,
} from '../shared/models.ts'

/** Refresh a little before expiry so a request never races the clock. */
const TOKEN_LIFETIME = 3600
const TOKEN_REFRESH_MARGIN = 300
const REQUEST_TIMEOUT = 15_000

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

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    let response = await this.send(method, path, body)
    if (response.status === 401) {
      this.invalidate()
      response = await this.send(method, path, body)
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

  private async send(method: string, path: string, body?: unknown): Promise<Response> {
    const headers: Record<string, string> = { Authorization: this.authHeader() }
    if (body !== undefined) headers['Content-Type'] = 'application/json'

    return fetch(`${this.baseUrl}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(REQUEST_TIMEOUT),
    })
  }

  // ─── Sessions ──────────────────────────────────────────────────────────

  async listSessions(
    params: { q?: string; status?: string; filter?: string; cwd?: string } = {},
  ): Promise<Session[]> {
    const res = await this.request<{ sessions?: Session[] }>('GET', `/api/sessions${queryString(params)}`)
    return res.sessions ?? []
  }

  async getSession(id: string): Promise<{ session: Session; pending_permissions: number }> {
    return this.request('GET', `/api/sessions/${encodeURIComponent(id)}`)
  }

  async listDirectories(): Promise<string[]> {
    const res = await this.request<{ directories?: string[] }>('GET', '/api/sessions/directories')
    return res.directories ?? []
  }

  transcript(id: string, limit = 200, offset = 0): Promise<TranscriptPage> {
    const query = queryString({ limit: String(limit), offset: String(offset) })
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
  sendPrompt(id: string, message: string): Promise<{ success: boolean; queued?: boolean }> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/send`, { message })
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

  setPermissionMode(id: string, mode: string): Promise<unknown> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/permission-mode`, { mode })
  }

  generateTitle(id: string): Promise<unknown> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(id)}/title/generate`)
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

  gitDiff(path: string, file: string): Promise<GitDiff> {
    return this.request('GET', `/api/git/diff${queryString({ path, file })}`)
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

function queryString(params: Record<string, string | undefined>): string {
  const pairs = Object.entries(params).filter(([, v]) => v !== undefined && v !== '')
  if (pairs.length === 0) return ''
  return `?${pairs.map(([k, v]) => `${k}=${encodeURIComponent(v as string)}`).join('&')}`
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return { message: text }
  }
}
