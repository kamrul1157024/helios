// Completing a directory path against the filesystem, for the new-session
// composer's place chip.
//
// Kept out of the component because it is the part with rules in it, and the
// rules are worth stating once: what the daemon will expand, what a relative
// path means to a client that has no working directory of its own, and which
// entries a half-typed name is offering to become.
import { type FileEntry } from '../../shared/models.ts'

/** Home, in the form the daemon expands. A bare `~` it does not (files.go). */
export const HOME = '~/'

/** How many completions are worth showing before the list is just a directory. */
export const MAX_COMPLETIONS = 40

/**
 * The directory a half-typed path is completing inside, and the prefix to keep.
 *
 * Everything up to the last separator names a directory to read; what follows
 * is what the entries in it have to start with. A path with no separator in it
 * yet, or a relative one, completes under home — which is where a session with
 * no directory of its own starts anyway, so it is the same place the composer
 * would have used.
 */
export function completionTarget(typed: string): { parent: string; prefix: string } {
  const cut = typed.lastIndexOf('/')
  const prefix = typed.slice(cut + 1)
  if (cut === -1) return { parent: HOME, prefix }
  const parent = typed.slice(0, cut)
  if (parent === '') return { parent: '/', prefix }
  if (parent === '~') return { parent: HOME, prefix }
  if (!parent.startsWith('/') && !parent.startsWith('~')) return { parent: HOME + parent, prefix }
  return { parent, prefix }
}

/**
 * The directories in a listing that a typed prefix is reaching for.
 *
 * Files are not offered: a session runs in a directory. Dot directories are
 * not either, until the prefix asks for one by name — `.claude` and
 * `.worktrees` are real answers, but they are not what an empty prefix means.
 * Anything already shown as a recent is left out rather than printed twice.
 */
export function completionsIn(entries: FileEntry[], prefix: string, exclude: Set<string>): FileEntry[] {
  const want = prefix.toLowerCase()
  return entries
    .filter(
      (entry) =>
        entry.is_dir &&
        entry.name.toLowerCase().startsWith(want) &&
        (want.startsWith('.') || !entry.name.startsWith('.')) &&
        !exclude.has(entry.path),
    )
    .slice(0, MAX_COMPLETIONS)
}
