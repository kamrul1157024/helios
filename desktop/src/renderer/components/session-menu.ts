import { store } from '../store.ts'
import type { MenuAction } from './selection-menu.tsx'
import type { ProviderInfo, Session } from '../../shared/models.ts'

/**
 * The permission modes one session may be switched between.
 *
 * Two callers want the same list: the row's context menu, and the status line
 * segment that shows which one is in force. Neither owns it, so it lives here.
 *
 * Switching restarts the agent, because the CLI takes the mode as a launch
 * flag. The daemon refuses while a session is mid-turn; the entries are
 * disabled to match, but the server check is the one that counts.
 */
export function modeActions(
  hostId: string,
  session: Session,
  providers: ProviderInfo[] | undefined,
): MenuAction[] {
  if (!providers) return [{ label: 'Loading…', disabled: true }]

  const modes = providers.find((provider) => provider.id === session.source)?.permission_modes ?? []
  if (modes.length === 0) return [{ label: 'This provider has no modes', disabled: true }]

  const switchable = session.status === 'idle'
  return modes.map((mode) => ({
    // The tick convention the group entries in this menu already use.
    label: `${mode === session.permission_mode ? '✓ ' : ' '}${mode}`,
    disabled: !switchable,
    title: switchable ? 'Switching restarts the agent' : 'Only while the session is idle',
    run: () => void store.setPermissionMode(hostId, session.session_id, mode),
  }))
}
