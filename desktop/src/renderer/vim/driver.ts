import { currentZone, store, type Column } from '../store.ts'
import { command } from './registry.ts'
import { registerCoreCommands } from './commands.ts'
import { DEFAULT_BINDINGS, LEADER, TIMEOUT_MS } from './keymap.ts'
import { applicable, EMPTY, expandLeader, flush, keyToken, merge, resolve, type Pending } from './resolve.ts'
import type { Binding, CommandContext, Mode } from './types.ts'

/**
 * The one keydown listener, and the timer the resolver deliberately does not
 * own.
 *
 * Everything decided here is decided by `resolve`; this only supplies the
 * keystrokes, the clock and the running. Keeping it that thin is what lets the
 * interesting half be tested without a DOM.
 */

let pending: Pending = EMPTY
let timer: ReturnType<typeof setTimeout> | null = null
let keymap: Binding[] = expandLeader(DEFAULT_BINDINGS, LEADER)

export function setKeymap(overrides: Binding[]): void {
  keymap = expandLeader(merge(DEFAULT_BINDINGS, overrides), LEADER)
}

/**
 * Whether the event is typing rather than commanding.
 *
 * Mode is meant to be authoritative, but the mouse can put a caret in a field
 * without asking, and a user who has clicked into the composer must not have
 * their next letter eaten. So focus is trusted in one direction only: it can
 * force insert, never normal.
 */
function typingInto(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
}

/**
 * The mode a focused element demands, if it demands one.
 *
 * A terminal is checked first and separately. xterm takes its keys on a hidden
 * textarea, so the ordinary test would call it insert — and insert lets the
 * keymap keep `Esc`, which is the one key Claude Code must receive.
 */
function surfaceMode(target: EventTarget | null): Mode | null {
  if (!(target instanceof HTMLElement)) return null
  if (target.closest('.xterm')) return 'terminal'
  return typingInto(target) ? 'insert' : null
}

function contextOf(mode: Mode): CommandContext {
  return { mode, zone: currentZone(store.getSnapshot()) }
}

function clear(): void {
  pending = EMPTY
  if (timer) clearTimeout(timer)
  timer = null
  store.setVimPending('')
}

function show(): void {
  store.setVimPending([pending.count, ...pending.keys].filter(Boolean).join(' '))
}

function run(id: string, ctx: CommandContext, count: number | undefined): void {
  const entry = command(id)
  if (!entry) return
  void entry.run(ctx, count)
}

function onKeyDown(event: KeyboardEvent): void {
  const state = store.getSnapshot()
  if (!state.vimEnabled) return

  // The app's own accelerators keep working in every mode, so they are never
  // this listener's business.
  if (event.metaKey) return

  const token = keyToken(event)
  if (token === null) return

  // An IME uses Escape to dismiss its candidate window, and Enter to accept a
  // candidate. Taking either mid-composition strands a half-typed word.
  if (event.isComposing) return

  const demanded = surfaceMode(event.target)
  const mode: Mode = state.vimMode === 'normal' && demanded ? demanded : state.vimMode
  if (mode !== state.vimMode) store.setVimMode(mode)

  const ctx = contextOf(mode)
  const scoped = applicable(keymap, ctx)
  const step = resolve(pending, token, scoped)
  pending = step.next

  if (timer) clearTimeout(timer)
  timer = null

  switch (step.action.kind) {
    case 'run':
      event.preventDefault()
      clear()
      run(step.action.command, ctx, step.action.count)
      return

    case 'pending': {
      show()
      // A key that has started a sequence must not also reach the page, or `g`
      // would type a letter on its way to `g c`.
      if (pending.keys.length > 0) event.preventDefault()
      const exact = step.action.exact
      if (exact?.command) {
        const held = pending
        timer = setTimeout(() => {
          const timed = flush(held, applicable(keymap, contextOf(store.getSnapshot().vimMode)))
          clear()
          if (timed.action.kind === 'run') {
            run(timed.action.command, contextOf(store.getSnapshot().vimMode), timed.action.count)
          }
        }, TIMEOUT_MS)
      }
      return
    }

    case 'none':
      clear()
  }
}

/**
 * A click into a text field is the one place focus is allowed to set the mode.
 * Without it the mode line would say NORMAL while the caret blinks in the
 * composer, and the two disagreeing is the failure this design exists to avoid.
 */
/**
 * Which column an element sits in.
 *
 * Read off the document rather than announced by each column, because the
 * things that take focus are not always React's to know about: xterm focuses a
 * textarea it owns, and CodeMirror focuses one of its own too.
 */
function columnOf(target: EventTarget | null): Column | null {
  if (!(target instanceof HTMLElement)) return null
  if (target.closest('.rail')) return 'rail'
  if (target.closest('.sidebar')) return 'sidebar'
  if (target.closest('main.main')) return 'main'
  return null
}

function onFocusIn(event: FocusEvent): void {
  const state = store.getSnapshot()
  if (!state.vimEnabled) return

  const column = columnOf(event.target)
  if (column) store.setColumn(column)

  const demanded = surfaceMode(event.target)
  if (demanded) {
    if (state.vimMode !== demanded) store.setVimMode(demanded)
  } else if (state.vimMode === 'insert' || state.vimMode === 'terminal') {
    store.setVimMode('normal')
  }
}

export function startVim(): () => void {
  registerCoreCommands()
  // Capture, so a component's own handler cannot swallow a command key first.
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('focusin', onFocusIn, true)
  return () => {
    window.removeEventListener('keydown', onKeyDown, true)
    window.removeEventListener('focusin', onFocusIn, true)
    clear()
  }
}
