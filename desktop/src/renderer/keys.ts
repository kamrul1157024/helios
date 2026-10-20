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
  schedules: (hostId: string) => ['host', hostId, 'schedules'] as const,
  channels: (hostId: string) => ['host', hostId, 'channels'] as const,
  channelMessages: (hostId: string, channelId: string) =>
    ['host', hostId, 'channels', channelId, 'messages'] as const,
  channelThread: (hostId: string, channelId: string, root: string) =>
    ['host', hostId, 'channels', channelId, 'threads', root] as const,
  /** Every session a schedule started, which the sessions list keeps folded. */
  jobSessions: (hostId: string) => ['host', hostId, 'job-sessions'] as const,
  /** One session on its own, for the ones the list does not carry. */
  session: (hostId: string, sessionId: string) =>
    ['host', hostId, 'session', sessionId] as const,
  /** One schedule's runs: ordinary sessions, filtered by what started them. */
  scheduleRuns: (hostId: string, scheduleId: string) =>
    ['host', hostId, 'schedule-runs', scheduleId] as const,
  scheduleLog: (hostId: string, scheduleId: string) =>
    ['host', hostId, 'schedule-log', scheduleId] as const,
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
  /** Every folder listing on a host, for a checkout that can move any of them. */
  fileDirs: (hostId: string) => ['host', hostId, 'files', 'dir'] as const,
  fileDir: (hostId: string, path: string) => ['host', hostId, 'files', 'dir', path] as const,
  fileContent: (hostId: string, path: string) => ['host', hostId, 'files', 'content', path] as const,
  /** A file a preview pulls in behind the one that was opened. Its own key
   *  rather than fileContent's, so a blob is not pinned for the session. */
  fileAsset: (hostId: string, path: string) => ['host', hostId, 'files', 'asset', path] as const,
  /** Every quick-open answer on a host, whatever root and query. */
  fileSearches: (hostId: string) => ['host', hostId, 'files', 'search'] as const,
  fileSearch: (hostId: string, root: string, q: string) =>
    ['host', hostId, 'files', 'search', root, q] as const,
  fileGrep: (hostId: string, root: string, q: string, opts: GrepOpts) =>
    ['host', hostId, 'files', 'grep', root, q, opts] as const,
  /**
   * What one session's tool calls wrote. Not under `files`: it moves when the
   * transcript grows, which a session's status says, not when a file does.
   */
  touchedAll: (hostId: string) => ['host', hostId, 'touched'] as const,
  touched: (hostId: string, sessionId: string) => ['host', hostId, 'touched', sessionId] as const,
  /** The host's remote desktop: its state, its install steps and its DPI. */
  desktop: (hostId: string) => ['host', hostId, 'desktop'] as const,
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

/**
 * Appends a delta to the newest page, which is where the live edge lands.
 *
 * Anything already held is dropped rather than added a second time. The caller
 * asks for "everything after seq N" and is trusted about N — but N comes from
 * what the panel currently holds, and a panel that has just switched sessions
 * has held two different things in the same second. Getting that wrong printed
 * the transcript twice, which reads as the old conversation coming back.
 */
export function appendDelta<T extends { pages: TranscriptPage[] }>(
  held: T | undefined,
  delta: TranscriptPage,
): T | undefined {
  if (!held) return held
  const [head, ...rest] = held.pages
  if (!head) return held

  const last = head.messages[head.messages.length - 1]?.seq ?? -1
  const fresh = delta.messages.filter((message) => message.seq > last)
  if (fresh.length === 0) {
    // Still worth the total: the count moved even when the tail did not.
    return { ...held, pages: [{ ...head, total: delta.total }, ...rest] }
  }
  return {
    ...held,
    pages: [{ ...head, messages: [...head.messages, ...fresh], total: delta.total }, ...rest],
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
  | {
      kind: 'invalidate'
      queryKey: readonly unknown[]
      /** This key alone, not everything under it as a prefix. */
      exact?: boolean
      /** Mark it stale and leave it: the next read refetches, nothing does now. */
      refetchType?: 'none'
    }
  | { kind: 'patchSession'; sessionId: string; patch: Partial<Session> }
  /** The event carries the whole answer, so it replaces the cached one. */
  | { kind: 'set'; queryKey: readonly unknown[]; data: unknown }

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
      // Three lists hold a session and all three have to hear this: the
      // ordinary one, the automated section — where every scheduled run lives,
      // and where a run that has finished used to sit reading "Starting" for
      // ever — and the single-session read the detail panel falls back to for a
      // run the ordinary list does not carry.
      return [
        { kind: 'patchSession', sessionId, patch },
        { kind: 'invalidate', queryKey: keys.allSessions(hostId) },
        { kind: 'invalidate', queryKey: keys.jobSessions(hostId) },
        { kind: 'invalidate', queryKey: keys.session(hostId, sessionId) },
        // The transcript moved, so the files it says were written may have too.
        // Costs nothing for a session nobody is looking at: only an observed
        // query refetches, and the Changes view and the tree are the observers.
        { kind: 'invalidate', queryKey: keys.touched(hostId, sessionId) },
      ]
    }

    case 'session_updated':
    case 'session_deleted':
      // The automated section holds sessions too, and they change for the same
      // reasons — a run going idle is a status the folded list still shows.
      return [
        { kind: 'invalidate', queryKey: keys.allSessions(hostId) },
        { kind: 'invalidate', queryKey: keys.jobSessions(hostId) },
      ]

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

    case 'schedule_created':
    case 'schedule_updated':
    case 'schedule_deleted':
      return [{ kind: 'invalidate', queryKey: keys.schedules(hostId) }]

    // A fire produces a session, so the runs list has a new row in it — and the
    // schedule's own summary moved to "running".
    case 'schedule_fired':
      return [
        { kind: 'invalidate', queryKey: keys.schedules(hostId) },
        { kind: 'invalidate', queryKey: keys.allSessions(hostId) },
        { kind: 'invalidate', queryKey: keys.jobSessions(hostId) },
      ]

    case 'session_evicted':
      return [{ kind: 'invalidate', queryKey: keys.host(hostId) }]

    // Every path named has changed *content*, not merely a changed mtime: the
    // daemon compares digests before it says anything (spec 54). The paths are
    // what keep the answer narrow (spec 65, 2.1): one agent write used to take
    // out every read under `files` — the open folders, quick open, a whole
    // ripgrep run — and with them the open tab, which is how a write next to a
    // dirty buffer moved `saved` under it.
    case 'file_changed':
      return fileChangedEffects(hostId, event.data.paths)

    // The same body GET /api/desktop answers, sent on every step of an install
    // and on every DPI change, so there is nothing to refetch.
    case 'desktop_state':
      return [{ kind: 'set', queryKey: keys.desktop(hostId), data: event.data }]

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
        { kind: 'invalidate', queryKey: keys.touchedAll(hostId) },
        { kind: 'invalidate', queryKey: keys.desktop(hostId) },
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

/** One entry of a `file_changed` payload, as far as the cache cares. */
interface ChangedPath {
  path: string
  kind: 'file' | 'dir' | 'repo'
}

/** The usable entries of a payload. An entry with no kind is read as a file. */
export function changedPaths(named: unknown): ChangedPath[] {
  if (!Array.isArray(named)) return []
  const out: ChangedPath[] = []
  for (const entry of named) {
    const path = text((entry as { path?: unknown } | null)?.path)
    if (!path) continue
    const kind = text((entry as { kind?: unknown }).kind)
    out.push({ path, kind: kind === 'dir' || kind === 'repo' ? kind : 'file' })
  }
  return out
}

/** The folder a path sits in. The root's parent is the root. */
export function parentDir(path: string): string {
  const cut = path.replace(/\/+$/, '').lastIndexOf('/')
  return cut <= 0 ? '/' : path.slice(0, cut)
}

/**
 * The narrow answer to a file event.
 *
 * - The listing of each named path's folder, and of the path itself when it is
 *   a folder. A checkout (`repo`) can move any listing, so all of them go.
 * - File contents are marked stale and never refetched from here. The open tabs
 *   follow the disk through the panel's own sync, which knows whether a buffer
 *   is dirty; a closed file simply reads fresh the next time it opens.
 * - Quick-open answers are marked stale too. The next keystroke asks again, and
 *   a list that is open does not flash under the reader.
 * - Content search is not touched at all. The panel says its results may be out
 *   of date and leaves the rerun to the reader.
 * - Git once, as before. The daemon caches its status per generation.
 */
function fileChangedEffects(hostId: string, named: unknown): CacheEffect[] {
  const changed = changedPaths(named)
  if (changed.length === 0) return []

  const effects: CacheEffect[] = []
  const dirs = new Set<string>()
  let everyListing = false
  for (const { path, kind } of changed) {
    if (kind === 'repo') {
      everyListing = true
      continue
    }
    dirs.add(parentDir(path))
    if (kind === 'dir') dirs.add(path)
  }
  if (everyListing) effects.push({ kind: 'invalidate', queryKey: keys.fileDirs(hostId) })
  else for (const dir of dirs) effects.push({ kind: 'invalidate', queryKey: keys.fileDir(hostId, dir), exact: true })

  for (const { path, kind } of changed) {
    if (kind !== 'file') continue
    effects.push({ kind: 'invalidate', queryKey: keys.fileContent(hostId, path), exact: true, refetchType: 'none' })
    // A picture or a stylesheet a preview pulled in. Only an observed one
    // refetches, which is a preview showing it right now.
    effects.push({ kind: 'invalidate', queryKey: keys.fileAsset(hostId, path), exact: true })
  }
  effects.push({ kind: 'invalidate', queryKey: keys.fileSearches(hostId), refetchType: 'none' })
  effects.push({ kind: 'invalidate', queryKey: keys.git(hostId) })
  return effects
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
