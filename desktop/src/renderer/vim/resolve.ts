import type { Binding, CommandContext, Mode, Zone } from './types.ts'

/**
 * Turning keys into commands, as a pure function.
 *
 * Deliberately free of the DOM and of the registry: the interesting part is
 * which sequence wins and when, and a resolver that reached a KeyboardEvent or
 * a store could only be tested through a stand-in for one. The timeout lives in
 * the caller for the same reason — a timer is not a fact about a keymap.
 */

/** What has been typed so far and not yet resolved. */
export interface Pending {
  keys: string[]
  /** Digits typed before a sequence. `3j` moves three rows. */
  count: string
}

export const EMPTY: Pending = { keys: [], count: '' }

export type Action =
  | { kind: 'pending'; candidates: Binding[]; exact: Binding | null }
  | { kind: 'run'; command: string; count?: number }
  | { kind: 'none' }

export interface Resolved {
  next: Pending
  action: Action
}

/** A key event reduced to what a binding can name. */
export interface KeyStroke {
  key: string
  ctrlKey?: boolean
  altKey?: boolean
  metaKey?: boolean
}

const NAMED: Record<string, string> = {
  ' ': '<space>',
  Escape: '<Esc>',
  Enter: '<CR>',
  Tab: '<Tab>',
  Backspace: '<BS>',
  Delete: '<Del>',
  ArrowUp: '<Up>',
  ArrowDown: '<Down>',
  ArrowLeft: '<Left>',
  ArrowRight: '<Right>',
}

/**
 * The token a binding would spell this stroke as, or null if vim mode should
 * not see it at all.
 *
 * ⌘ returns null on purpose. Those are the app's own accelerators, they work in
 * every mode including inside a terminal, and a keymap that could shadow ⌘Q is
 * a keymap that can lock someone out of quitting.
 */
export function keyToken(stroke: KeyStroke): string | null {
  if (stroke.metaKey) return null

  const base = NAMED[stroke.key] ?? stroke.key
  if (base.length === 0) return null
  // A modifier pressed on its own is not a stroke.
  if (base === 'Control' || base === 'Alt' || base === 'Shift' || base === 'Meta') return null

  const inner = base.startsWith('<') && base.endsWith('>') ? base.slice(1, -1) : base
  if (stroke.ctrlKey && stroke.altKey) return `<C-M-${inner}>`
  if (stroke.ctrlKey) return `<C-${inner}>`
  if (stroke.altKey) return `<M-${inner}>`
  return base
}

export function tokens(keys: string): string[] {
  return keys.split(' ').filter((token) => token.length > 0)
}

/**
 * The bindings that could fire right now.
 *
 * Filtering here rather than inside `resolve` keeps the resolver ignorant of
 * modes and zones, and it means the hint overlay and the resolver are looking
 * at the same list — the overlay draws what `resolve` returned.
 */
export function applicable(bindings: Binding[], ctx: CommandContext): Binding[] {
  return bindings.filter((binding) => {
    if (binding.command === null) return false
    return inScope(binding.when, ctx)
  })
}

function inScope(when: { zones?: Zone[]; modes?: Mode[] } | undefined, ctx: CommandContext): boolean {
  if (!when) return ctx.mode === 'normal'
  if (when.modes && !when.modes.includes(ctx.mode)) return false
  if (when.zones && !when.zones.includes(ctx.zone)) return false
  if (!when.modes && ctx.mode !== 'normal') return false
  return true
}

/**
 * Folds one keystroke into the pending buffer.
 *
 * `bindings` must already be scoped — see `applicable`.
 */
export function resolve(state: Pending, token: string, bindings: Binding[]): Resolved {
  // Esc abandons a half-typed sequence, and only that. With nothing pending it
  // is an ordinary key, which is what lets it be bound to leaving insert mode.
  if (token === '<Esc>' && (state.keys.length > 0 || state.count !== '')) {
    return { next: EMPTY, action: { kind: 'none' } }
  }

  // A count only accumulates before a sequence starts. `0` is a motion in its
  // own right, so it opens nothing — it only extends a count already running.
  if (state.keys.length === 0 && /^[0-9]$/.test(token) && !(token === '0' && state.count === '')) {
    return { next: { keys: [], count: state.count + token }, action: { kind: 'pending', candidates: [], exact: null } }
  }

  const keys = [...state.keys, token]
  const candidates = bindings.filter((binding) => startsWith(tokens(binding.keys), keys))
  if (candidates.length === 0) return { next: EMPTY, action: { kind: 'none' } }

  const exact = candidates.find((binding) => tokens(binding.keys).length === keys.length) ?? null
  const longer = candidates.some((binding) => tokens(binding.keys).length > keys.length)

  // An exact match with nothing longer behind it fires now. With something
  // longer behind it, vim waits: `d` cannot run while `dd` is still reachable.
  if (exact && !longer) {
    return { next: EMPTY, action: { kind: 'run', command: exact.command as string, count: countOf(state) } }
  }
  return { next: { keys, count: state.count }, action: { kind: 'pending', candidates, exact } }
}

/**
 * What the pending buffer means once the caller's timer has run out.
 *
 * This is the other half of the ambiguity above: `d` held for `timeoutlen` with
 * `dd` unfinished resolves to `d`, exactly as vim does.
 */
export function flush(state: Pending, bindings: Binding[]): Resolved {
  const exact = bindings.find((binding) => same(tokens(binding.keys), state.keys))
  if (!exact) return { next: EMPTY, action: { kind: 'none' } }
  return { next: EMPTY, action: { kind: 'run', command: exact.command as string, count: countOf(state) } }
}

/** Substitutes `<leader>` so the resolver never has to know what it stands for. */
export function expandLeader(bindings: Binding[], leader: string): Binding[] {
  return bindings.map((binding) => ({
    ...binding,
    keys: tokens(binding.keys)
      .map((token) => (token === '<leader>' ? leader : token))
      .join(' '),
  }))
}

/**
 * Folds user overrides onto the defaults.
 *
 * Same keys and same scope replaces; a null command removes. Overrides rather
 * than a whole keymap so a binding added in a later version arrives bound
 * instead of missing — the reasoning `PrefsStore.load` already applies to
 * notification alerts.
 */
export function merge(defaults: Binding[], overrides: Binding[]): Binding[] {
  const out = [...defaults]
  for (const override of overrides) {
    const at = out.findIndex(
      (binding) => binding.keys === override.keys && scopeKey(binding.when) === scopeKey(override.when),
    )
    if (at === -1) out.push(override)
    else if (override.command === null) out.splice(at, 1)
    else out[at] = override
  }
  return out
}

function scopeKey(when: { zones?: Zone[]; modes?: Mode[] } | undefined): string {
  return `${(when?.modes ?? []).join(',')}|${(when?.zones ?? []).join(',')}`
}

function countOf(state: Pending): number | undefined {
  return state.count === '' ? undefined : Number(state.count)
}

function startsWith(full: string[], prefix: string[]): boolean {
  if (prefix.length > full.length) return false
  return prefix.every((token, index) => full[index] === token)
}

function same(a: string[], b: string[]): boolean {
  return a.length === b.length && startsWith(a, b)
}
