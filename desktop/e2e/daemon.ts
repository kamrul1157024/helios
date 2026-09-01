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
  close(): Promise<void>
}

function session(id: string, title: string): Session {
  return {
    session_id: id,
    source: 'claude',
    cwd: REPO,
    project: 'repo',
    title,
    status: 'idle',
    pinned: false,
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

/** One file per worktree, named after it, so a wrong root is visible on sight. */
const TREES: Record<string, FileEntry[]> = {
  [REPO]: [file(REPO, 'main.go'), file(REPO, 'README.md')],
  [OTHER_WORKTREE]: [file(OTHER_WORKTREE, 'hotfix.go')],
}

function file(root: string, name: string): FileEntry {
  return { name, path: `${root}/${name}`, is_dir: false, size: 12, mod_time: '2026-01-01T00:00:00Z' }
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
  const streams = openStreams

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

    res.setHeader('Content-Type', 'application/json')
    res.end(JSON.stringify(answer(path, q)))
  })

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address() as AddressInfo

  return {
    url: `http://127.0.0.1:${port}`,
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
function answer(path: string, q: (name: string) => string): unknown {
  switch (path) {
    case '/api/health':
      return { ok: true }
    case '/api/sessions': {
      // Three lists off one route, as the daemon serves them: the ordinary one,
      // everything a schedule started, and one schedule's runs.
      const jobs = q('filter') === 'jobs' || q('schedule_id') !== ''
      return {
        sessions: jobs ? RUNS : SESSIONS,
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
    case '/api/files':
      return { path: q('path'), entries: TREES[q('path')] ?? [] }
    case '/api/file':
      return { path: q('path'), size: 12, mod_time: '2026-01-01T00:00:00Z', content: 'package main\n' }
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
        if (path.startsWith('/api/sessions/') && one) return { session: one, pending_permissions: 0 }
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
