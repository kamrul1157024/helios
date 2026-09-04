/**
 * What vim mode is made of, as types the rest of the feature agrees on.
 *
 * Free of any import that reaches React or the preload bridge, so the resolver
 * beside it stays a pure function of keys and bindings.
 */

/**
 * The input state.
 *
 * The mode is authoritative and DOM focus follows it, not the other way round.
 * Entering `insert` focuses the zone's text surface; leaving it blurs. A click
 * into a text field is the one reverse edge, and it sets `insert` so the two
 * can never disagree.
 */
export type Mode = 'normal' | 'insert' | 'visual' | 'command' | 'terminal' | 'scroll'

/**
 * Which part of the window owns the keyboard, and so what a bare letter means.
 *
 * Below the detail panel this is derived from `Layout.focused` rather than
 * tracked separately: the layout already knows which group has the keyboard.
 */
export type Zone =
  | 'rail'
  | 'sidebar'
  | 'transcript'
  | 'terminal'
  | 'approvals'
  | 'git'
  | 'files'
  | 'editor'
  | 'diff'
  | 'schedules'
  | 'settings'

export interface CommandContext {
  mode: Mode
  zone: Zone
}

/**
 * One thing the app can be asked to do.
 *
 * Every mouse action is one of these, which is what lets `:` reach what the
 * keymap does not. `run` takes no component instance: a command must work when
 * the pane that owns it is not mounted.
 */
export interface Command {
  id: string
  /** Imperative, and shown as typed. "Rename session". */
  title: string
  /** Groups the hint overlay's columns. "Session", "Window", "Git". */
  group: string
  when?: { zones?: Zone[]; modes?: Mode[] }
  /** Asks before running. Destructive or agent-visible actions set it. */
  confirm?: string
  /** Shown but refusing, with the reason, when the target is not there. */
  enabled?: (ctx: CommandContext) => boolean
  run: (ctx: CommandContext, count?: number) => void | Promise<void>
}

/** A sequence of key tokens, and what it runs. */
export interface Binding {
  /** Space-separated tokens: `g c`, `<C-w> s`, `d d`. */
  keys: string
  /** A null command unbinds a default. */
  command: string | null
  when?: { zones?: Zone[]; modes?: Mode[] }
}
