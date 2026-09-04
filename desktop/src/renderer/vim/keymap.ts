import type { Binding, Zone } from './types.ts'

/**
 * What the keys do before anyone changes them.
 *
 * LazyVim rather than stock vim where the two differ: plain `Ctrl-h/l` for
 * window motion instead of `Ctrl-w h`, and `Shift-h/l` for the next and
 * previous panel the way LazyVim cycles buffers.
 */

export const LEADER = '<space>'

/** LazyVim's `timeoutlen`. Long enough not to flash during fluent typing. */
export const TIMEOUT_MS = 300

const LISTS: Zone[] = ['rail', 'sidebar', 'schedules', 'files', 'approvals', 'git', 'settings']

export const DEFAULT_BINDINGS: Binding[] = [
  // Columns and windows.
  { keys: '<C-h>', command: 'column.left' },
  { keys: '<C-l>', command: 'column.right' },
  { keys: '<C-w> s', command: 'window.split' },
  { keys: '<C-w> S', command: 'window.splitBefore' },
  { keys: '<C-w> q', command: 'window.close' },
  { keys: '<C-w> =', command: 'window.even' },
  { keys: '<C-w> >', command: 'window.wider' },
  { keys: '<C-w> <', command: 'window.narrower' },
  { keys: '<C-w> r', command: 'window.rotate' },

  // Panels.
  { keys: 'g c', command: 'panel.chat' },
  { keys: 'g t', command: 'panel.terminal' },
  { keys: 'g a', command: 'panel.approvals' },
  { keys: 'g d', command: 'panel.git' },
  { keys: 'g f', command: 'panel.files' },
  { keys: 'L', command: 'panel.next' },
  { keys: 'H', command: 'panel.prev' },

  // What the sidebar is showing.
  { keys: '<leader> 1', command: 'mode.sessions' },
  { keys: '<leader> 2', command: 'mode.schedules' },
  { keys: '<leader> 3', command: 'mode.settings' },

  // Lists.
  { keys: 'j', command: 'list.next', when: { zones: LISTS } },
  { keys: 'k', command: 'list.prev', when: { zones: LISTS } },
  { keys: 'g g', command: 'list.first', when: { zones: LISTS } },
  { keys: 'G', command: 'list.last', when: { zones: LISTS } },
  { keys: '<CR>', command: 'list.activate', when: { zones: LISTS } },

  // Modes.
  { keys: 'i', command: 'insert.enter' },
  { keys: '<Esc>', command: 'normal.enter', when: { modes: ['insert', 'visual'] } },
  { keys: '<C-\\> <C-n>', command: 'normal.enter', when: { modes: ['terminal', 'scroll'] } },
]
