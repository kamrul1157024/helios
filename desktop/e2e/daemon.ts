// A daemon the app can be pointed at, with none of a daemon behind it.
//
// The end-to-end tests are about what the UI does when the user moves between
// sessions, and a real daemon would make that a test of git, a real repository
// and two live agents as well. This serves the handful of reads the panels make
// and nothing else, so a failure is a failure of the app.
import http from 'node:http'
import { AddressInfo } from 'node:net'

import type { FileEntry, GrepMatch, Session, Worktree } from '../src/shared/models.ts'

/** The directory both sessions run in — the whole point, see session-state.spec.ts. */
export const REPO = '/repo'
export const OTHER_WORKTREE = '/repo-hotfix'

export const ALPHA = 's-alpha'
export const BETA = 's-beta'

export interface StubDaemon {
  url: string
  /** Pushes one SSE frame to every connected client, as the daemon would. */
  emit(type: string, data: unknown): void
  /** Replaces what `/api/file` answers for a path, and its mod time with it. */
  setFile(path: string, content: string): void
  /**
   * Changes a session's status in the list this daemon serves.
   *
   * A `session_status` event alone is not enough: the client patches the cache
   * from the payload and then refetches behind it, so a list still answering
   * "idle" puts the old status straight back. The event says it moved; this is
   * what makes it stay moved.
   */
  setStatus(sessionId: string, status: string): void
  /**
   * How many times `/api/file` has been asked for a path.
   *
   * The way to wait for the app to have *finished* thinking about an event when
   * the right outcome is that nothing changes. Asserting an absence on its own
   * passes before the event has even crossed the socket.
   */
  reads(path: string): number
  /**
   * How many times the content search has been run.
   *
   * A refetch of a search nobody re-typed asks the same question and gets the
   * same answer, so it leaves no mark on the results. Counting the asking is
   * how a test knows the refetch it provoked has happened at all.
   */
  greps(): number
  close(): Promise<void>
}

/** What `/api/file` answers before a test has said otherwise. */
const DEFAULT_CONTENT = 'package main\n'

function session(id: string, title: string): Session {
  return {
    session_id: id,
    source: 'claude',
    cwd: REPO,
    project: 'repo',
    title,
    status: 'idle',
    pinned: false,
    // Both are optional on the wire and absent on a session the daemon has not
    // heard from yet. Set here because the status line draws them, and a bar
    // with two empty slots proves nothing.
    model: 'opus',
    permission_mode: 'default',
    created_at: '2026-01-01T00:00:00Z',
    last_event_at: '2026-01-01T00:00:00Z',
    supports_prompt_queue: true,
    // Warm, so neither session reads as terminated: a terminated one closes its
    // panels for its own reasons and would hide the behaviour under test.
    terminal: `/tmp/helios/${id}.sock`,
  }
}

/**
 * A session whose agent has not written its log yet.
 *
 * Every session looks like this for the first second of its life: the daemon
 * answers the transcript with nothing and no epoch, because the file the agent
 * writes does not exist. What the panel does with that answer afterwards is the
 * point — it used to keep it for ever.
 */
export const GAMMA = 's-gamma'

const SESSIONS = [session(ALPHA, 'Alpha'), session(BETA, 'Beta'), session(GAMMA, 'Gamma')]

/**
 * The sessions the schedule below started, kept out of the ordinary list.
 *
 * One of them has ended, which is what a past run looks like: the daemon
 * terminates a run when its agent stops, so most of this list is terminated and
 * the section has to show them anyway.
 */
export const RUN = 's-run'
export const PAST_RUN = 's-run-past'
const RUNS = [
  session(RUN, 'Nightly run'),
  { ...session(PAST_RUN, 'Last night'), status: 'terminated' as const, terminal: undefined },
]

/** One line per session, each naming itself. */
export const TRANSCRIPT_TEXT: Record<string, string> = {
  [ALPHA]: 'this is the alpha transcript',
  [BETA]: 'this is the beta transcript',
}

/**
 * What each session has written, and a way for a test to add to it.
 *
 * Growing behind the reader's back is the case that matters: the panel has to
 * come back to a session and pick up what arrived while it was away, without
 * printing what it already had.
 */
const EXTRA: Record<string, string[]> = {}

export function appendTranscript(sessionId: string, text: string): void {
  ;(EXTRA[sessionId] ??= []).push(text)
  // The record moves with the log, as it does on a real daemon: last_event_at
  // is the only thing that tells a panel there is more to read.
  const record = [...SESSIONS, ...RUNS].find((s) => s.session_id === sessionId)
  if (record) record.last_event_at = new Date().toISOString()
}

/** Back to how each test expects to find the daemon. */
export function resetTranscripts(): void {
  for (const key of Object.keys(EXTRA)) delete EXTRA[key]
  for (const record of [...SESSIONS, ...RUNS]) {
    record.last_event_at = '2026-01-01T00:00:00Z'
    if (record.session_id === PAST_RUN) continue
    record.status = 'idle'
    record.terminal = `/tmp/helios/${record.session_id}.sock`
  }
}

/**
 * Moves a session the way the daemon does before it announces it.
 *
 * A stub that only pushed the event would have the refetch behind it put the
 * old status straight back, which is not a failure any daemon can produce.
 */
export function setSessionStatus(sessionId: string, status: Session['status']): void {
  const record = [...SESSIONS, ...RUNS].find((s) => s.session_id === sessionId)
  if (!record) return
  record.status = status
  if (status === 'terminated') delete record.terminal
}

/**
 * Wakes every connected client, the way a status change does.
 *
 * The type rides in the SSE event name and the payload in data, which is the
 * shape the daemon writes (internal/server/sse.go:85) and the only one the
 * client reads a session id out of.
 */
export function pushEvent(type: string, data: Record<string, unknown>): void {
  for (const stream of openStreams) {
    stream.write(`event: ${type}\ndata: ${JSON.stringify(data)}\n\n`)
  }
}

// Held outside startDaemon so a test can push an event without a handle.
const openStreams = new Set<http.ServerResponse>()

function transcriptFor(id: string): { seq: number; role: string; content: string; timestamp: string }[] {
  const first = TRANSCRIPT_TEXT[id]
  const all = first === undefined ? (EXTRA[id] ?? []) : [first, ...(EXTRA[id] ?? [])]
  return all.map((content, seq) => ({
    seq,
    role: 'assistant',
    content,
    timestamp: '2026-01-01T00:00:00Z',
  }))
}

/** One schedule, so the schedules list has something in it to switch to. */
const SCHEDULES = [
  {
    id: 'sched-1',
    name: 'nightly-sweep',
    kind: 'timer',
    enabled: true,
    cron: '0 2 * * *',
    mode: 'new',
    prompt: 'sweep the dependency updates',
    cwd: REPO,
    next_run_at: '2030-01-01T02:00:00Z',
    fail_streak: 0,
    fires_today: 0,
    created_at: '2026-01-01T00:00:00Z',
  },
]

const WORKTREES: Worktree[] = [
  {
    path: REPO,
    branch: 'main',
    is_main: true,
    detached: false,
    locked: false,
    ahead: 0,
    behind: 0,
    dirty: 0,
    date: '2026-01-02T00:00:00Z',
  },
  {
    path: OTHER_WORKTREE,
    branch: 'hotfix',
    is_main: false,
    detached: false,
    locked: false,
    ahead: 1,
    behind: 0,
    dirty: 2,
    date: '2026-01-03T00:00:00Z',
  },
]

/**
 * Home, and the one directory under it with anything in it.
 *
 * The new-session picker completes a typed path against the disk, and a bare
 * segment completes under home — so `~/` has to answer with directories, not
 * with the flat list of files the other roots serve.
 */
export const HOME = '/home/dev'
export const WORKSPACE = `${HOME}/workspace`

/** One file per worktree, named after it, so a wrong root is visible on sight. */
const TREES: Record<string, FileEntry[]> = {
  [REPO]: [file(REPO, 'main.go'), file(REPO, 'README.md')],
  [OTHER_WORKTREE]: [file(OTHER_WORKTREE, 'hotfix.go')],
  [HOME]: [dir(HOME, 'workspace'), dir(HOME, 'worktrees'), dir(HOME, '.config'), file(HOME, 'notes.md')],
  [WORKSPACE]: [dir(WORKSPACE, 'acme-api'), dir(WORKSPACE, 'acme-web')],
}

function file(root: string, name: string): FileEntry {
  return { name, path: `${root}/${name}`, is_dir: false, size: 12, mod_time: '2026-01-01T00:00:00Z' }
}

function dir(root: string, name: string): FileEntry {
  return { ...file(root, name), is_dir: true, size: 0 }
}

/** What the daemon's resolveSafePath does to a path before it reads it. */
function resolveStubPath(asked: string): string {
  const expanded = asked.startsWith('~/') ? `${HOME}/${asked.slice(2)}` : asked
  return expanded.length > 1 ? expanded.replace(/\/+$/, '') : expanded
}

function grepMatches(root: string, query: string): GrepMatch[] {
  const entries = TREES[root] ?? []
  return entries.map((entry, index) => ({
    path: entry.path,
    rel: entry.name,
    line: index + 1,
    column: 1,
    text: `${query} in ${entry.name}`,
  }))
}

export async function startDaemon(): Promise<StubDaemon> {
  // Held open so the process can be shut down without waiting for the event
  // stream, which by design never ends on its own.
  // The module's own set, so a test can push an event without holding the
  // handle — `emit` below writes to the same one.
  const streams = openStreams
  // What each path currently holds. A test writes here and then emits, which is
  // the order the real daemon works in: the sweep reads the disk, then speaks.
  const contents = new Map<string, string>()
  // Bumped on every write so a changed file reports a changed mod time, as a
  // real one would. The app must not need it — it compares bytes — and that is
  // worth having true here so the test cannot pass for the wrong reason.
  let revision = 0
  const readCount = new Map<string, number>()
  let grepCount = 0
  // Per daemon, not on the fixtures: SESSIONS is a module constant shared by
  // every test in the worker, and one test leaving a session busy would be the
  // next test's starting state.
  const statuses = new Map<string, string>()
  const statusOf = (one: Session): Session => {
    const status = statuses.get(one.session_id)
    return status === undefined ? one : { ...one, status: status as Session['status'] }
  }

  const held = (target: string): FileAnswer => {
    readCount.set(target, (readCount.get(target) ?? 0) + 1)
    return {
      content: contents.get(target) ?? DEFAULT_CONTENT,
      modTime: `2026-01-01T00:00:${String(revision).padStart(2, '0')}Z`,
    }
  }

  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://127.0.0.1')
    const path = url.pathname
    const q = (name: string): string => url.searchParams.get(name) ?? ''

    if (path === '/api/events') {
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
      res.write(': open\n\n')
      streams.add(res)
      req.on('close', () => streams.delete(res))
      return
    }

    if (path === '/api/files/grep') grepCount += 1
    res.setHeader('Content-Type', 'application/json')
    res.end(JSON.stringify(answer(path, q, held, statusOf)))
  })

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address() as AddressInfo

  return {
    url: `http://127.0.0.1:${port}`,
    emit: (type, data) => {
      const frame = `event: ${type}\ndata: ${JSON.stringify(data)}\n\n`
      for (const stream of streams) stream.write(frame)
    },
    reads: (target) => readCount.get(target) ?? 0,
    greps: () => grepCount,
    setFile: (target, content) => {
      contents.set(target, content)
      revision += 1
    },
    setStatus: (sessionId, status) => statuses.set(sessionId, status),
    close: () =>
      new Promise<void>((resolve) => {
        for (const stream of streams) stream.end()
        server.close(() => resolve())
      }),
  }
}

/**
 * Every read a panel makes, and an empty object for the rest.
 *
 * The fallback is deliberate: the app asks for more than these tests care
 * about — settings, providers, notifications — and an empty answer is enough
 * for all of it. A 404 would put an error where a panel should be.
 */
interface FileAnswer {
  content: string
  modTime: string
}

function answer(
  path: string,
  q: (name: string) => string,
  held: (target: string) => FileAnswer,
  statusOf: (session: Session) => Session,
): unknown {
  switch (path) {
    case '/api/health':
      return { ok: true }
    case '/api/sessions': {
      // Three lists off one route, as the daemon serves them: the ordinary one,
      // everything a schedule started, and one schedule's runs.
      const jobs = q('filter') === 'jobs' || q('schedule_id') !== ''
      return {
        sessions: (jobs ? RUNS : SESSIONS).map(statusOf),
        host: { warm: 2, warm_rss: 0, budget: 8, load: 0.1, memory_used: 1, memory_total: 8 },
      }
    }
    case '/api/sessions/directories':
      return { directories: [{ cwd: REPO, project: 'repo', session_count: 2, active_count: 2 }] }
    case '/api/git/worktrees':
      return { worktrees: WORKTREES }
    case '/api/git/status':
      return {
        root: q('path'),
        branch: q('path') === OTHER_WORKTREE ? 'hotfix' : 'main',
        dirty: false,
        ahead: 0,
        behind: 0,
        staged: [],
        unstaged: [],
        untracked: [],
      }
    case '/api/git/log':
      // No base branch and no commits: the git panel then opens on the working
      // tree, which is the view these tests drive.
      return { root: q('path'), branch: 'main', base: '', scope: 'all', commits: [], has_more: false }
    case '/api/files': {
      // `~/` is expanded before the listing is read, and the answer says which
      // directory it turned out to be (internal/server/files.go).
      const root = resolveStubPath(q('path'))
      return { path: root, entries: TREES[root] ?? [] }
    }
    case '/api/file': {
      const { content, modTime } = held(q('path'))
      return { path: q('path'), size: content.length, mod_time: modTime, content }
    }
    case '/api/files/grep':
      return { root: q('path'), matches: grepMatches(q('path'), q('q')), scanned: 2, truncated: false }
    case '/api/files/search':
      return { root: q('path'), matches: [], scanned: 0, truncated: false }
    case '/api/schedules':
      return { schedules: SCHEDULES }
    // What the new-schedule form offers: a schedule runs unattended, so the
    // model and the permission mode are picked rather than guessed.
    case '/api/providers':
      return {
        providers: [
          { id: 'claude', name: 'Claude', permission_modes: ['default', 'bypassPermissions'] },
        ],
      }
    case '/api/providers/claude/models':
      return { models: [{ id: 'opus', name: 'Opus' }, { id: 'sonnet', name: 'Sonnet' }] }
    default:
      // A transcript that says which session it belongs to, which is the whole
      // point of the sync test: a panel showing the wrong one is only visible
      // if the two differ. It also answers the delta the panel asks for on the
      // live edge — after_seq — because appending what is already held is how
      // a transcript came to print itself twice.
      // One session by id. A run is not in the ordinary list, so this is the
      // only way the detail panel can find the one the sidebar selected.
      {
        const id = path.slice('/api/sessions/'.length)
        const one = [...SESSIONS, ...RUNS].find((s) => s.session_id === id)
        if (path.startsWith('/api/sessions/') && one) return { session: statusOf(one), pending_permissions: 0 }
      }
      if (path.endsWith('/transcript')) {
        const id = path.slice('/api/sessions/'.length, -'/transcript'.length)
        const all = transcriptFor(id)
        const after = q('after_seq')
        const messages = after === '' ? all : all.filter((m) => m.seq > Number(after))
        // Nothing written yet: the daemon takes an early return that carries no
        // epoch at all (internal/server/api.go:446), and a client that treats a
        // missing epoch as "wait for a delta" waits for ever.
        if (all.length === 0) {
          return { messages: [], total: 0, returned: 0, offset: 0, has_more: false }
        }
        return {
          messages,
          total: all.length,
          returned: messages.length,
          offset: 0,
          has_more: false,
          epoch: 'e2e-epoch',
        }
      }
      return {}
  }
}
