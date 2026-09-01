import type {
  DiffAt,
  GrepOpts,
  HostStats,
  LogOpts,
  SSEEvent,
  Session,
  TranscriptMessage,
  TranscriptPage,
} from '../shared/models.ts'

/**
 * Every cache key the renderer uses, and what a server event does to them.
 *
 * Deliberately free of any import that reaches the preload bridge: this is the
 * part worth asserting directly, and `bridge.ts` binds `window.helios` at
 * module scope. The reads themselves live in `queries.ts`, which pairs these
 * keys with the calls that answer them.
 *
 * Keys are namespaced by host first, because two daemons can hold the same path
 * and must not answer for each other. They are hierarchical below that, so
 * `invalidateQueries({ queryKey: keys.git(hostId) })` takes out every git read
 * for a host in one call — which is what a commit or a branch change means.
 */
export const keys = {
  host: (hostId: string) => ['host', hostId] as const,
  sessions: (hostId: string, grouped: boolean) => ['host', hostId, 'sessions', { grouped }] as const,
  /** Both `grouped` variants at once. Matching is by prefix, so an invalidation
   *  must not name a flag the sidebar may since have changed. */
  allSessions: (hostId: string) => ['host', hostId, 'sessions'] as const,
  groups: (hostId: string) => ['host', hostId, 'groups'] as const,
  notifications: (hostId: string) => ['host', hostId, 'notifications'] as const,
  settings: (hostId: string) => ['host', hostId, 'settings'] as const,
  directories: (hostId: string) => ['host', hostId, 'directories'] as const,
  providers: (hostId: string) => ['host', hostId, 'providers'] as const,
  models: (hostId: string, provider: string) => ['host', hostId, 'models', provider] as const,
  /**
   * The epoch is deliberately not in this key.
   *
   * It is only known once the first page has arrived, so keying on it would
   * mean fetching under one key and then immediately refetching under another.
   * A fork is handled by dropping the entry instead — see `epoch_changed`.
   */
  transcript: (hostId: string, sessionId: string) =>
    ['host', hostId, 'transcript', sessionId] as const,

  git: (hostId: string) => ['host', hostId, 'git'] as const,
  gitStatus: (hostId: string, cwd: string) => ['host', hostId, 'git', 'status', cwd] as const,
  gitDiff: (hostId: string, cwd: string, file: string, at: DiffAt | undefined) =>
    ['host', hostId, 'git', 'diff', cwd, file, at ?? {}] as const,
  gitLog: (hostId: string, cwd: string, opts: LogOpts | undefined) =>
    ['host', hostId, 'git', 'log', cwd, opts ?? {}] as const,
  /** The scope menu's paged log. Apart from `gitLog` because the cache holds
   *  pages here and one answer there, and the two shapes cannot share an entry. */
  gitLogPages: (hostId: string, cwd: string, all: boolean) =>
    ['host', hostId, 'git', 'log', 'pages', cwd, { all }] as const,
  gitChanges: (hostId: string, cwd: string, to: string, from: string | undefined, mergeBase: boolean) =>
    ['host', hostId, 'git', 'changes', cwd, to, from ?? '', mergeBase] as const,
  gitWorktrees: (hostId: string, cwd: string) => ['host', hostId, 'git', 'worktrees', cwd] as const,
  reviewed: (hostId: string, cwd: string, base: string) =>
    ['host', hostId, 'git', 'reviewed', cwd, base] as const,

  files: (hostId: string) => ['host', hostId, 'files'] as const,
  fileDir: (hostId: string, path: string) => ['host', hostId, 'files', 'dir', path] as const,
  fileContent: (hostId: string, path: string) => ['host', hostId, 'files', 'content', path] as const,
  /** A file a preview pulls in behind the one that was opened. Its own key
   *  rather than fileContent's, so a blob is not pinned for the session. */
  fileAsset: (hostId: string, path: string) => ['host', hostId, 'files', 'asset', path] as const,
  fileSearch: (hostId: string, root: string, q: string) =>
    ['host', hostId, 'files', 'search', root, q] as const,
  fileGrep: (hostId: string, root: string, q: string, opts: GrepOpts) =>
    ['host', hostId, 'files', 'grep', root, q, opts] as const,
}

// ─── The session list ───────────────────────────────────────────────────────

/** The session list and the host's own figures, which ride the same envelope. */
export interface SessionListPage {
  sessions: Session[]
  host?: HostStats
}

/** Patches one row wherever the list is cached, across both `grouped` variants. */
export function patchSessionInPage(
  page: SessionListPage | undefined,
  sessionId: string,
  patch: Partial<Session>,
): SessionListPage | undefined {
  if (!page) return page
  return {
    ...page,
    sessions: page.sessions.map((session) =>
      session.session_id === sessionId ? { ...session, ...patch } : session,
    ),
  }
}

// ─── The transcript ─────────────────────────────────────────────────────────

/**
 * Flattens the transcript's pages into reading order.
 *
 * Page 0 is the tail of the conversation and each further page is older, so the
 * page order reverses; within a page the messages are already chronological.
 */
export function transcriptMessages(
  data: { pages: TranscriptPage[] } | undefined,
): TranscriptMessage[] {
  if (!data) return []
  return [...data.pages].reverse().flatMap((page) => page.messages)
}

/** Appends a delta to the newest page, which is where the live edge lands. */
export function appendDelta<T extends { pages: TranscriptPage[] }>(
  held: T | undefined,
  delta: TranscriptPage,
): T | undefined {
  if (!held) return held
  const [head, ...rest] = held.pages
  if (!head) return held
  return {
    ...held,
    pages: [{ ...head, messages: [...head.messages, ...delta.messages], total: delta.total }, ...rest],
  }
}

// ─── The settings document ──────────────────────────────────────────────────

/**
 * The daemon answers with the settings under a key, alongside personas and
 * event types. Reading the envelope as though it were the map gives undefined
 * for every setting, which is indistinguishable from a fresh install.
 */
export interface SettingsDocument {
  settings?: Record<string, string>
}

export function settingValues(doc: SettingsDocument | undefined): Record<string, string> {
  return doc?.settings ?? {}
}

/** Sorted by what the sessions are doing, or by hand. */
export type SortMode = 'activity' | 'manual'

/** Anything but an explicit 'manual' is activity, which needs no daemon. */
export function sortModeOf(doc: SettingsDocument | undefined): SortMode {
  return settingValues(doc)['sessions.sort'] === 'manual' ? 'manual' : 'activity'
}

/**
 * Folds a write into the cached document.
 *
 * Three panes write disjoint parts of one settings map — the memory budget, the
 * auto-titler and the sidebar's sort mode — and the daemon merges by key. So an
 * optimistic update has to merge too: replacing the map would blank the other
 * panes' fields until the next fetch.
 */
export function mergeSettings(
  doc: SettingsDocument | undefined,
  written: Record<string, string>,
): SettingsDocument {
  return { ...doc, settings: { ...doc?.settings, ...written } }
}

// ─── What a server event does to the cache ──────────────────────────────────

/**
 * What one SSE event means for the cache, as data rather than as calls.
 *
 * Returned rather than applied so the mapping can be asserted directly: the
 * interesting part is which keys an event takes out, and a function that calls
 * a QueryClient can only be tested through a stand-in for one.
 */
export type CacheEffect =
  | { kind: 'invalidate'; queryKey: readonly unknown[] }
  | { kind: 'patchSession'; sessionId: string; patch: Partial<Session> }

export function effectsFor(hostId: string, event: SSEEvent): CacheEffect[] {
  switch (event.type) {
    case 'session_status': {
      const sessionId = text(event.data.session_id)
      const status = text(event.data.status)
      if (!sessionId || !status) return []
      // A resume carries the new host handle, and taking it matters: the
      // session is cold in this client's copy until something says otherwise.
      // Most session_status events say nothing about the terminal at all, so an
      // absent handle is no evidence the host went away.
      const terminal = text(event.data.terminal)
      const patch = (terminal ? { status, terminal } : { status }) as Partial<Session>
      // The payload carries a status and little else, but the record behind it
      // moved with it — last_event_at above all, which is the only thing telling
      // the transcript there is more of it to read.
      return [
        { kind: 'patchSession', sessionId, patch },
        { kind: 'invalidate', queryKey: keys.allSessions(hostId) },
      ]
    }

    case 'session_updated':
    case 'session_deleted':
      return [{ kind: 'invalidate', queryKey: keys.allSessions(hostId) }]

    case 'notification':
    case 'notification_resolved':
      // Sessions too: a permission request writes waiting_permission to the
      // session and then announces only the notification
      // (internal/provider/claude/hooks.go:110,148), so refetching the list is
      // the one way the sidebar hears about it.
      return [
        { kind: 'invalidate', queryKey: keys.notifications(hostId) },
        { kind: 'invalidate', queryKey: keys.allSessions(hostId) },
      ]

    case 'session_evicted':
      return [{ kind: 'invalidate', queryKey: keys.host(hostId) }]

    // Every path named has changed *content*, not merely a changed mtime: the
    // daemon compares digests before it says anything (spec 54). Which paths
    // they are does not change the answer here — both prefixes go, and React
    // Query refetches only what has an observer, so naming one costs the same
    // as naming all of them.
    //
    // Git comes too. A working-tree write moves `git status`, and a repo entry
    // is a commit or a checkout.
    case 'file_changed': {
      const named = event.data.paths
      if (!Array.isArray(named) || named.length === 0) return []
      return [
        { kind: 'invalidate', queryKey: keys.files(hostId) },
        { kind: 'invalidate', queryKey: keys.git(hostId) },
      ]
    }

    // The socket was down and the daemon keeps no replay buffer, so anything
    // that moved in the gap was announced to nobody. Files and git for a second
    // reason: a path whose watch expired while a tab sat open is not being
    // swept, and re-reading is what registers it again.
    case 'stream_reconnected':
      return [
        { kind: 'invalidate', queryKey: keys.allSessions(hostId) },
        { kind: 'invalidate', queryKey: keys.notifications(hostId) },
        { kind: 'invalidate', queryKey: keys.files(hostId) },
        { kind: 'invalidate', queryKey: keys.git(hostId) },
      ]

    // 'show' instructs the window, and the terminal events move connections:
    // 'terminal_opened' re-lists a session's shells and attaches the ones this
    // client is missing, 'terminal_closed' tears a tab down. The store handles
    // all three directly. None is a fact about anything cached.
    default:
      return []
  }
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

// ─── What a file event does to one open tab ─────────────────────────────────

/**
 * What to do with a tab once its file has been re-read.
 *
 * `reload` replaces the buffer, `mark` raises the changed-on-disk bar, and
 * `ignore` does nothing at all.
 */
export type FileEffect = 'reload' | 'mark' | 'ignore'

/**
 * Whether an event means anything for one open tab, decided by comparing bytes.
 *
 * Kept out of the component so the rule that protects unsaved work can be
 * asserted: there is no component test framework here, and this is the half
 * that must never be wrong.
 *
 * Bytes rather than mod times, for two reasons. It drops the echo of this
 * window's own save — the broadcaster has no addressing, so a writer hears
 * itself — without which every save would remount the editor of whoever pressed
 * ⌘S. And it is the second line of defence behind the daemon's own hash: the
 * daemon compares digests so it does not broadcast noise, and this compares
 * text so a false alarm never reaches a dirty buffer.
 *
 * Comparing `mod_time` would not survive the API anyway: the read route formats
 * it with RFC3339 and the write route with RFC3339Nano, so a tab's cached value
 * changes precision depending on how it was last filled.
 */
export function fileEffectFor(
  tab: { dirty: boolean },
  saved: string,
  fetched: string | null,
): FileEffect {
  // A file that has gone leaves the buffer alone and says so. Closing the tab
  // would discard work that saving could still put back on disk.
  if (fetched === null) return 'mark'
  if (fetched === saved) return 'ignore'
  return tab.dirty ? 'mark' : 'reload'
}
