import { currentLayout, currentZone, store, type Column } from '../store.ts'
import { panelItem, type Edge, type Group, type Layout } from '../components/layout.ts'
import { register } from './registry.ts'
import { activate, enterZone, jump, move, textSurface } from './items.ts'
import type { Command, Zone } from './types.ts'

/**
 * The first set of commands, and the shape every later one copies.
 *
 * Registered at module scope rather than from a component: `:` has to reach a
 * command whose pane is not mounted, and half of these move the keyboard *to*
 * a pane that is not mounted yet.
 */

const LIST_ZONES: Zone[] = ['rail', 'sidebar', 'schedules', 'files', 'approvals', 'git', 'settings']

function focusedGroup(layout: Layout): Group | undefined {
  return layout.groups.find((group) => group.id === layout.focused) ?? layout.groups[0]
}

/** The selection, the layout and the item with the keyboard, or null. */
function panelTarget(): { selection: NonNullable<ReturnType<typeof selectionOf>>; layout: Layout; group: Group } | null {
  const selection = selectionOf()
  if (!selection) return null
  const layout = currentLayout(store.getSnapshot())
  const group = focusedGroup(layout)
  if (!group) return null
  return { selection, layout, group }
}

function selectionOf(): { hostId: string; sessionId: string } | null {
  return store.getSnapshot().selection
}

function command(entry: Command): Command {
  return entry
}

// ─── Columns ────────────────────────────────────────────────────────────────

const ORDER: Column[] = ['rail', 'sidebar', 'main']

/**
 * `Ctrl-h` and `Ctrl-l` cross the three columns, and inside the detail panel
 * they hand over to the layout's own focus.
 *
 * Crossing only when the panel has no further group in that direction is what
 * makes one key work the whole width of the window, which is how LazyVim's
 * window motions read.
 */
function stepColumn(by: -1 | 1): void {
  const state = store.getSnapshot()
  if (state.column === 'main') {
    const target = panelTarget()
    if (target) {
      const at = target.layout.groups.findIndex((group) => group.id === target.group.id)
      const next = target.layout.groups[at + by]
      if (next) {
        store.focusGroup(target.selection, next.id)
        return
      }
    }
  }
  const at = ORDER.indexOf(state.column)
  const next = ORDER[at + by]
  if (!next) return
  store.setColumn(next)
  // The ring has to follow the column, or the mode line names one zone while
  // the cursor sits in another and the next motion moves the wrong list.
  enterZone(currentZone(store.getSnapshot()))
}

// ─── Windows ────────────────────────────────────────────────────────────────

/** A fifth of the row per press, which is coarse enough to be worth a keystroke. */
const RESIZE_STEP = 0.2

function resizeFocused(by: number): void {
  const target = panelTarget()
  if (!target) return
  const at = target.layout.groups.findIndex((group) => group.id === target.group.id)
  // The last group has no sash of its own, so it borrows the one behind it and
  // pushes the other way.
  const last = at === target.layout.groups.length - 1
  const sash = last ? at - 1 : at
  if (sash < 0) return
  store.resizeGroups(target.selection, sash, last ? -by : by)
}

function splitFocused(edge: Edge): void {
  const target = panelTarget()
  if (!target) return
  store.splitItem(target.selection, target.group.active, target.group.id, edge)
}

export function registerCoreCommands(): void {
  register(
    command({
      id: 'column.left',
      title: 'Focus the pane to the left',
      group: 'Window',
      run: () => stepColumn(-1),
    }),
    command({
      id: 'column.right',
      title: 'Focus the pane to the right',
      group: 'Window',
      run: () => stepColumn(1),
    }),
    command({
      id: 'window.split',
      title: 'Split the focused panel out',
      group: 'Window',
      run: () => splitFocused('after'),
    }),
    command({
      id: 'window.splitBefore',
      title: 'Split the focused panel out, before',
      group: 'Window',
      run: () => splitFocused('before'),
    }),
    command({
      id: 'window.close',
      title: 'Close the focused panel',
      group: 'Window',
      run: () => {
        const target = panelTarget()
        if (target) store.dropItem(target.selection, target.group.active)
      },
    }),
    command({
      id: 'window.even',
      title: 'Give every panel the same width',
      group: 'Window',
      run: () => {
        const target = panelTarget()
        if (target) store.evenGroups(target.selection)
      },
    }),
    command({ id: 'window.wider', title: 'Widen the focused panel', group: 'Window', run: () => resizeFocused(RESIZE_STEP) }),
    command({
      id: 'window.narrower',
      title: 'Narrow the focused panel',
      group: 'Window',
      run: () => resizeFocused(-RESIZE_STEP),
    }),
    command({
      id: 'window.rotate',
      title: 'Lay the panels out the other way',
      group: 'Window',
      run: () => {
        const target = panelTarget()
        if (target) store.setLayoutAxis(target.selection, target.layout.axis === 'row' ? 'column' : 'row')
      },
    }),
  )

  // ─── Panels ───────────────────────────────────────────────────────────────

  const panels: [string, string, 'chat' | 'terminal' | 'approvals' | 'git' | 'files'][] = [
    ['panel.chat', 'Show the transcript', 'chat'],
    ['panel.terminal', 'Show the terminal', 'terminal'],
    ['panel.approvals', 'Show approvals', 'approvals'],
    ['panel.git', 'Show git', 'git'],
    ['panel.files', 'Show files', 'files'],
  ]
  for (const [id, title, panel] of panels) {
    register(
      command({
        id,
        title,
        group: 'Panel',
        run: () => {
          store.setPanel(panel)
          store.setColumn('main')
        },
      }),
    )
  }

  register(
    command({
      id: 'panel.next',
      title: 'Next panel',
      group: 'Panel',
      run: () => stepPanel(1),
    }),
    command({
      id: 'panel.prev',
      title: 'Previous panel',
      group: 'Panel',
      run: () => stepPanel(-1),
    }),
  )

  // ─── Modes of the sidebar ─────────────────────────────────────────────────

  register(
    command({
      id: 'mode.sessions',
      title: 'Show sessions',
      group: 'Go to',
      run: () => {
        store.setSidebarMode('sessions')
        store.setColumn('sidebar')
      },
    }),
    command({
      id: 'mode.schedules',
      title: 'Show schedules',
      group: 'Go to',
      run: () => {
        store.setSidebarMode('schedules')
        store.setColumn('sidebar')
      },
    }),
    command({
      id: 'mode.settings',
      title: 'Show settings',
      group: 'Go to',
      run: () => {
        store.openSettings()
        store.setColumn('sidebar')
      },
    }),
  )

  // ─── Lists ────────────────────────────────────────────────────────────────

  register(
    command({
      id: 'list.next',
      title: 'Next row',
      group: 'Move',
      when: { zones: LIST_ZONES },
      run: (ctx, count) => move(ctx.zone, count ?? 1),
    }),
    command({
      id: 'list.prev',
      title: 'Previous row',
      group: 'Move',
      when: { zones: LIST_ZONES },
      run: (ctx, count) => move(ctx.zone, -(count ?? 1)),
    }),
    command({
      id: 'list.first',
      title: 'First row',
      group: 'Move',
      when: { zones: LIST_ZONES },
      run: (ctx) => jump(ctx.zone, 'first'),
    }),
    command({
      id: 'list.last',
      title: 'Last row',
      group: 'Move',
      when: { zones: LIST_ZONES },
      run: (ctx) => jump(ctx.zone, 'last'),
    }),
    command({
      id: 'list.activate',
      title: 'Open the focused row',
      group: 'Move',
      when: { zones: LIST_ZONES },
      run: (ctx) => activate(ctx.zone),
    }),
  )

  // ─── Insert ───────────────────────────────────────────────────────────────

  register(
    command({
      id: 'insert.enter',
      title: 'Type here',
      group: 'Mode',
      enabled: (ctx) => textSurface(ctx.zone) !== null,
      run: (ctx) => {
        const surface = textSurface(ctx.zone)
        if (!surface) return
        // The mode goes first: focus follows it, and a listener that saw the
        // focus before the mode would treat it as a click and race.
        store.setVimMode('insert')
        surface.focus()
      },
    }),
    command({
      id: 'normal.enter',
      title: 'Back to normal mode',
      group: 'Mode',
      when: { modes: ['insert', 'visual', 'terminal', 'scroll'] },
      run: (ctx) => {
        const active = document.activeElement
        if (active instanceof HTMLElement) active.blur()
        store.setVimMode('normal')
        // Blurring alone would leave the keyboard on the document, where the
        // next motion has no list to move. Leaving insert should put the cursor
        // back where insert was entered from.
        enterZone(ctx.zone)
      },
    }),
  )
}

function stepPanel(by: 1 | -1): void {
  const target = panelTarget()
  if (!target) return
  const strip = target.group.items
  const at = strip.indexOf(target.group.active)
  const next = strip[(at + by + strip.length) % strip.length]
  if (next) store.revealItem(target.selection, next)
}

/** Exported for the palette's sake: the zone a command would act on. */
export function activeZone(): Zone {
  return currentZone(store.getSnapshot())
}

export { panelItem }
