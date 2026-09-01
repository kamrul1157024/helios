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

const SESSIONS = [session(ALPHA, 'Alpha'), session(BETA, 'Beta')]

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
}

export function resetTranscripts(): void {
  for (const key of Object.keys(EXTRA)) delete EXTRA[key]
}

function transcriptFor(id: string): { seq: number; role: string; content: string; timestamp: string }[] {
  const first = TRANSCRIPT_TEXT[id]
  if (!first) return []
  return [first, ...(EXTRA[id] ?? [])].map((content, seq) => ({
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
  const streams = new Set<http.ServerResponse>()

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
    case '/api/sessions':
      return {
        sessions: SESSIONS,
        host: { warm: 2, warm_rss: 0, budget: 8, load: 0.1, memory_used: 1, memory_total: 8 },
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
    default:
      // A transcript that says which session it belongs to, which is the whole
      // point of the sync test: a panel showing the wrong one is only visible
      // if the two differ. It also answers the delta the panel asks for on the
      // live edge — after_seq — because appending what is already held is how
      // a transcript came to print itself twice.
      if (path.endsWith('/transcript')) {
        const id = path.slice('/api/sessions/'.length, -'/transcript'.length)
        const all = transcriptFor(id)
        const after = q('after_seq')
        const messages = after === '' ? all : all.filter((m) => m.seq > Number(after))
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
