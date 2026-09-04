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

/** True of the two spellings that already say where they start from. */
function rooted(path: string): boolean {
  return path.startsWith('/') || path === '~' || path.startsWith('~/')
}

/** Where a relative path starts: the picker's directory, or home without one. */
function startFrom(base: string): string {
  return base.trim() || HOME
}

/**
 * The absolute path a typed string means, read from where the picker already is.
 *
 * A relative path is relative to the directory the chip names, the way it would
 * be in a shell sitting in it. Two things depend on that being written down
 * once. Completion has to read the directory the typing is actually inside, or
 * typing `desk` in a picker set to `~/workspace/helios` offers whatever home
 * happens to hold and calls it an answer. And the escape hatch has to hand the
 * daemon a path it accepts: `resolveCWD` refuses anything not absolute
 * (internal/server/cwd.go), so committing the raw text would make "anything
 * typed is committable" true only of the paths that were already absolute.
 */
export function resolveTyped(typed: string, base: string): string {
  const path = typed.trim()
  if (path === '' || rooted(path)) return path
  const from = startFrom(base)
  return from.endsWith('/') ? from + path : `${from}/${path}`
}

/**
 * The directory a half-typed path is completing inside, and the prefix to keep.
 *
 * Everything up to the last separator names a directory to read; what follows
 * is what the entries in it have to start with. A path with no separator in it
 * yet, or a relative one, completes under `base` — the directory the composer
 * is set to. With no directory set that is home, which is where a session
 * without one starts anyway.
 */
export function completionTarget(typed: string, base: string = HOME): { parent: string; prefix: string } {
  const cut = typed.lastIndexOf('/')
  const prefix = typed.slice(cut + 1)
  if (cut === -1) return { parent: startFrom(base), prefix }
  const parent = typed.slice(0, cut)
  if (parent === '') return { parent: '/', prefix }
  if (parent === '~') return { parent: HOME, prefix }
  return { parent: resolveTyped(parent, base), prefix }
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
