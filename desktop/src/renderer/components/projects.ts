// How the sidebar decides which project a session belongs to.
//
// Kept out of the component so it can be tested: the grouping is the one piece
// of the sidebar that changes what the list contains rather than how it looks.

import type { Session } from '../../shared/models.ts'

/** Which project a session belongs to. */
export interface Place {
  /** What sessions are gathered on — the project's name, folded. */
  key: string
  name: string
  cwd: string
}

/**
 * The directory the agent was started in, and nothing inferred from it.
 *
 * The daemon already answers this: `project` is the provider's name for the
 * session's directory, or its basename when the provider gives none
 * (internal/store/sessions.go:89). A worktree is a working directory in its own
 * right — `conductor/workspaces/opal-app/vilnius` is where that session lives
 * and is the thing being worked in — so it groups as itself rather than being
 * folded back under the checkout it was branched from. Grouping arranges what
 * the daemon reports; it does not reinterpret it.
 */
export function placeOf(session: Session): Place {
  const cwd = (session.cwd ?? '').replace(/\/+$/, '')
  const base = cwd.split('/').pop() ?? ''
  const name = session.project || base || 'sessions'
  return { key: name.toLowerCase(), name, cwd }
}

/**
 * A stable colour for a project, drawn from its name.
 *
 * Twelve hues rather than the whole wheel: adjacent hues are not
 * distinguishable at 22px, so a continuous hash spends its range on
 * differences nobody can see, while twelve stops are twelve badges the eye can
 * actually tell apart. Saturation and lightness are fixed so no project gets a
 * badge that shouts.
 */
export function tintOf(key: string): string {
  let hash = 0
  for (let index = 0; index < key.length; index += 1) {
    hash = (hash * 31 + key.charCodeAt(index)) % 4096
  }
  return `hsl(${(hash % 12) * 30} 62% 62%)`
}
