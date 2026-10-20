// Which files the Changes mode's current scope changed, and how to ask git for
// one of their diffs (spec 65, B8).
//
// The same answer serves the Changes list, the Diff toggle on a file tab, and
// the diff itself, so it is worked out once, here, where it can be asserted.
import type { DiffAt, GitChanges, GitStatus } from '../../shared/models.ts'
import type { Scope } from './commits.tsx'
import { gitMarks, type GitLetter } from './tree-rows.ts'

/** One file the scope changed. */
export interface ScopeFile {
  /** Absolute, as the tabs and the tree name files. */
  path: string
  /** From the repository's top, as git names it. */
  rel: string
  letter: GitLetter
  insertions?: number
  deletions?: number
  /** The old path, for a rename. */
  from?: string
}

/** The files in scope, by absolute path, in the order git listed them. */
export function scopeFiles(
  scope: Scope | null,
  status: GitStatus | null | undefined,
  changes: GitChanges | null | undefined,
  root: string,
): Map<string, ScopeFile> {
  const out = new Map<string, ScopeFile>()
  if (!scope) return out
  const top = (status?.root || root).replace(/\/+$/, '')
  if (scope.kind === 'working') {
    for (const [path, letter] of gitMarks(status).files) {
      out.set(path, { path, rel: path.slice(top.length + 1), letter })
    }
    return out
  }
  for (const file of changes?.files ?? []) {
    const path = `${top}/${file.path}`
    out.set(path, {
      path,
      rel: file.path,
      letter: letterOf(file.status),
      insertions: file.insertions,
      deletions: file.deletions,
      ...(file.from ? { from: file.from } : {}),
    })
  }
  return out
}

/**
 * What to ask `/api/git/diff` for one file in a scope. An untracked file has
 * nothing to diff against in the index, so it is diffed against nothing.
 */
export function scopeDiffAt(scope: Scope, letter: GitLetter | undefined): DiffAt {
  if (scope.kind === 'working') return letter === 'U' ? { untracked: true } : {}
  // Merge base, not the branch tip: commits landed on the base since this
  // branch was cut are not this branch's work.
  if (scope.kind === 'review') return { from: scope.base, to: 'HEAD', mergeBase: true }
  return { ...(scope.from ? { from: scope.from } : {}), to: scope.to }
}

/** The `/api/git/changes` question a history scope asks, or null for the working tree. */
export function scopeRange(scope: Scope | null): { to: string; from?: string; mergeBase: boolean } | null {
  if (!scope || scope.kind === 'working') return null
  if (scope.kind === 'review') return { to: 'HEAD', from: scope.base, mergeBase: true }
  return { to: scope.to, ...(scope.from ? { from: scope.from } : {}), mergeBase: false }
}

function letterOf(status: string): GitLetter {
  const letter = status.trim().charAt(0)
  if (letter === 'A' || letter === 'C') return 'A'
  if (letter === 'D') return 'D'
  if (letter === 'R') return 'R'
  if (letter === '?') return 'U'
  return 'M'
}
