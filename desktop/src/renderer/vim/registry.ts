import type { Command, CommandContext } from './types.ts'

/**
 * Every action the app can be asked to do, in one place.
 *
 * The registry is the guarantee behind the mouse-free goal: `:` lists what is
 * in here, so an action that is registered is reachable whether or not anyone
 * gave it a key. An action that is *not* registered is invisible in the palette
 * and in the hint overlay, which is how the omission gets noticed.
 *
 * A module-scope map rather than React context: commands register beside the
 * component that owns them, and `:` has to reach one whose pane is not mounted.
 */
const commands = new Map<string, Command>()

export function register(...added: Command[]): void {
  for (const command of added) {
    const clash = commands.get(command.id)
    // Two actions under one id would make the palette run the wrong one, and
    // the keymap file names ids — so the collision has to be loud.
    if (clash && clash !== command) throw new Error(`duplicate command id: ${command.id}`)
    commands.set(command.id, command)
  }
}

export function command(id: string): Command | undefined {
  return commands.get(id)
}

export function all(): Command[] {
  return [...commands.values()]
}

/**
 * What `:` offers, in the order it offers them.
 *
 * Out-of-scope commands are listed and refused rather than hidden: a palette
 * that changes shape as panes mount is one you cannot learn.
 */
export function palette(ctx: CommandContext): { command: Command; enabled: boolean }[] {
  return all()
    .map((entry) => ({ command: entry, enabled: runnable(entry, ctx) }))
    .sort((a, b) => rank(a) - rank(b) || a.command.title.localeCompare(b.command.title))
}

export function runnable(entry: Command, ctx: CommandContext): boolean {
  if (entry.when?.zones && !entry.when.zones.includes(ctx.zone)) return false
  if (entry.when?.modes && !entry.when.modes.includes(ctx.mode)) return false
  return entry.enabled?.(ctx) ?? true
}

function rank(entry: { enabled: boolean }): number {
  return entry.enabled ? 0 : 1
}

/** Test seam. Nothing in the app clears the registry. */
export function reset(): void {
  commands.clear()
}
