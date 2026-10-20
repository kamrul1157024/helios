// The tree as data: the open folders flattened into one list of rows, and the
// marks each row carries — git state, the agent's edits.
//
// One flat list is what lets the tree draw only the rows on screen (spec 65,
// 2.5) and move a keyboard cursor by index. Pure, so the order and the marks
// can be asserted without a component test framework.
import type { FileEntry, GitStatus, TouchedFile } from '../../shared/models.ts'

/** Version-control metadata. Never something a person browses into. */
const HIDDEN = new Set(['.git', '.hg', '.svn'])

/** What is known about one folder's listing. */
export interface DirState {
  /** Nothing to show yet: the first read is still out. */
  loading: boolean
  /** The read failed, in words. */
  error?: string
}

export type TreeRow =
  | { kind: 'entry'; entry: FileEntry; depth: number }
  /** A folder that could not be listed, said on the folder's own row. */
  | { kind: 'error'; dir: string; depth: number; message: string }

/**
 * Every visible row, depth first, in the order the daemon listed each folder.
 * A folder's error goes under the folder, where its children would have been.
 */
export function flattenTree(
  root: string,
  children: Record<string, FileEntry[]>,
  expanded: ReadonlySet<string>,
  states: Record<string, DirState>,
): TreeRow[] {
  const rows: TreeRow[] = []
  const walk = (dir: string, depth: number): void => {
    const failed = states[dir]?.error
    if (failed !== undefined) {
      rows.push({ kind: 'error', dir, depth, message: failed })
      return
    }
    for (const entry of children[dir] ?? []) {
      if (HIDDEN.has(entry.name)) continue
      rows.push({ kind: 'entry', entry, depth })
      if (entry.is_dir && expanded.has(entry.path)) walk(entry.path, depth + 1)
    }
  }
  walk(root, 0)
  return rows
}

/** The letter a changed file wears, and what it means in words. */
export type GitLetter = 'M' | 'A' | 'D' | 'U' | 'R'

export const GIT_LETTER_TITLES: Record<GitLetter, string> = {
  M: 'Modified',
  A: 'Added',
  D: 'Deleted',
  U: 'Untracked',
  R: 'Renamed',
}

/** Git state by absolute path, and the folders that hold any of it. */
export interface GitMarks {
  files: Map<string, GitLetter>
  dirs: Set<string>
}

/**
 * Reads the status the Changes mode already fetches into marks for the tree.
 *
 * The working tree wins over the index when a file is in both: what is on disk
 * is what the reader is looking at.
 */
export function gitMarks(status: GitStatus | null | undefined): GitMarks {
  const files = new Map<string, GitLetter>()
  const dirs = new Set<string>()
  if (!status?.root) return { files, dirs }
  const root = status.root.replace(/\/+$/, '')
  const add = (rel: string, letter: GitLetter): void => {
    const path = `${root}/${rel.replace(/\/+$/, '')}`
    if (files.has(path)) return
    files.set(path, letter)
    for (const dir of ancestors(path, root)) dirs.add(dir)
  }
  for (const change of status.unstaged ?? []) add(change.path, letterOf(change.status))
  for (const change of status.staged ?? []) add(change.path, letterOf(change.status))
  for (const change of status.untracked ?? []) add(change.path, 'U')
  return { files, dirs }
}

function letterOf(code: string): GitLetter {
  const letter = code.replace(/[\s.]/g, '').charAt(0)
  if (letter === 'A' || letter === 'C') return 'A'
  if (letter === 'D') return 'D'
  if (letter === 'R') return 'R'
  if (letter === '?') return 'U'
  return 'M'
}

/** What this session wrote, by path, and the folders those files sit in. */
export interface AgentMarks {
  files: Map<string, TouchedFile>
  dirs: Set<string>
}

export function agentMarks(touched: readonly TouchedFile[] | undefined, root: string): AgentMarks {
  const files = new Map<string, TouchedFile>()
  const dirs = new Set<string>()
  for (const file of touched ?? []) {
    if (files.has(file.path)) continue
    files.set(file.path, file)
    for (const dir of ancestors(file.path, root)) dirs.add(dir)
  }
  return { files, dirs }
}

/** Every folder between a path and the root, the root itself excluded. */
function ancestors(path: string, root: string): string[] {
  const out: string[] = []
  let cut = path.lastIndexOf('/')
  while (cut > 0) {
    const dir = path.slice(0, cut)
    if (dir.length <= root.length) break
    out.push(dir)
    cut = dir.lastIndexOf('/')
  }
  return out
}

/**
 * The index of the next row whose name starts with what was typed, searching
 * forward from just after the current row and wrapping round. Type-to-jump.
 */
export function typeAhead(rows: readonly TreeRow[], from: number, typed: string): number {
  const needle = typed.toLowerCase()
  if (!needle || rows.length === 0) return from
  // A single repeated letter cycles through the rows that start with it.
  const start = needle.length === 1 ? from + 1 : from
  for (let step = 0; step < rows.length; step++) {
    const index = (((start + step) % rows.length) + rows.length) % rows.length
    const row = rows[index]
    if (row?.kind === 'entry' && row.entry.name.toLowerCase().startsWith(needle)) return index
  }
  return from
}
