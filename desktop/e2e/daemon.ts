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
   * How many times `/api/file` has been asked for a path.
   *
   * The way to wait for the app to have *finished* thinking about an event when
   * the right outcome is that nothing changes. Asserting an absence on its own
   * passes before the event has even crossed the socket.
   */
  reads(path: string): number
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
    created_at: '2026-01-01T00:00:00Z',
    last_event_at: '2026-01-01T00:00:00Z',
    supports_prompt_queue: true,
    // Warm, so neither session reads as terminated: a terminated one closes its
    // panels for its own reasons and would hide the behaviour under test.
    terminal: `/tmp/helios/${id}.sock`,
  }
}

const SESSIONS = [session(ALPHA, 'Alpha'), session(BETA, 'Beta')]

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
  // What each path currently holds. A test writes here and then emits, which is
  // the order the real daemon works in: the sweep reads the disk, then speaks.
  const contents = new Map<string, string>()
  // Bumped on every write so a changed file reports a changed mod time, as a
  // real one would. The app must not need it — it compares bytes — and that is
  // worth having true here so the test cannot pass for the wrong reason.
  let revision = 0
  const readCount = new Map<string, number>()

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

    res.setHeader('Content-Type', 'application/json')
    res.end(JSON.stringify(answer(path, q, held)))
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
    setFile: (target, content) => {
      contents.set(target, content)
      revision += 1
    },
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
): unknown {
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
    case '/api/file': {
      const { content, modTime } = held(q('path'))
      return { path: q('path'), size: content.length, mod_time: modTime, content }
    }
    case '/api/files/grep':
      return { root: q('path'), matches: grepMatches(q('path'), q('q')), scanned: 2, truncated: false }
    case '/api/files/search':
      return { root: q('path'), matches: [], scanned: 0, truncated: false }
    default:
      if (path.endsWith('/transcript')) return { messages: [], total: 0, returned: 0, offset: 0, has_more: false }
      return {}
  }
}
