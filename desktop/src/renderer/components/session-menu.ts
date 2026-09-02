import { api } from '../bridge.ts'
import { store } from '../store.ts'
import type { MenuAction } from './selection-menu.tsx'
import { canResume, type ProviderInfo, type Session, type SessionGroup } from '../../shared/models.ts'

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
  const entries: MenuAction[] = modes.map((mode) => ({
    // The tick convention the group entries in this menu already use.
    label: `${mode === session.permission_mode ? '✓ ' : ' '}${mode}`,
    disabled: !switchable,
    title: switchable ? 'Switching restarts the agent' : undefined,
    run: () => void store.setPermissionMode(hostId, session.session_id, mode),
  }))

  if (switchable) return entries

  // A row of its own rather than a tooltip on the greyed-out modes. A disabled
  // button fires no pointer events, so its `title` never opens — the one place
  // the explanation cannot be read is the control it explains.
  //
  // Which way out depends on why it is refused: a session mid-turn has to be
  // stopped, and a terminated one has nothing left running to restart.
  return [{ label: canResume(session) ? RESUME_FIRST : STOP_FIRST, disabled: true }, ...entries]
}

/* Named after the control that clears it rather than the state it wants: Stop
   is the ■ beside the prompt, Resume is on the session's own row. Kept short
   because the widest entry sets the width of the whole menu. */
const STOP_FIRST = 'Stop the agent to change this'
const RESUME_FIRST = 'Resume the session to change this'

/**
 * What can be done to a session, as the right-click on its row offers it.
 *
 * These were buttons in the detail header, where they applied to whichever
 * session happened to be open: acting on any other one meant selecting it
 * first and reading the header to check it had changed. On the row they name
 * the session under the pointer, which is the one being pointed at.
 */
export function sessionActions(
  hostId: string,
  session: Session,
  groups: SessionGroup[],
  providers: ProviderInfo[] | undefined,
): MenuAction[] {
  const run = async (fn: () => Promise<unknown>): Promise<void> => {
    try {
      await fn()
      await store.invalidateSessionsFor(hostId)
    } catch (err) {
      store.fail(err)
    }
  }

  const held = session.group_key ?? ''

  // One entry per group, ticked on the one this session is filed under.
  // Choosing another moves it; choosing the current one takes it out.
  const actions: MenuAction[] = groups.map((group) => {
    const inside = held === group.key
    return {
      label: `${inside ? '✓ ' : '\u2003'}${group.name}`,
      run: () => void store.setSessionGroup(hostId, session.session_id, inside ? '' : group.key),
    }
  })

  actions.push({
    label: 'New group…',
    run: () => {
      const name = window.prompt('Name the group')?.trim()
      if (!name) return
      void store.createGroup(hostId, name).then((group) => {
        if (group) void store.setSessionGroup(hostId, session.session_id, group.key)
      })
    },
  })

  actions.push(
    {
      // Prompted rather than edited in place, as New group above is. The title
      // used to be an input in the detail header; with the header gone this is
      // the only way to set one by hand.
      label: 'Rename…',
      run: () => {
        const title = window.prompt('Name the session', session.title ?? '')?.trim()
        if (title === undefined || title === (session.title ?? '')) return
        void store.patchSessionField(hostId, session.session_id, { title })
      },
    },
    {
      label: 'Regenerate title',
      // The daemon waits for the model before answering, so this can sit for
      // several seconds. Saying what came back is the difference between a
      // slow menu item and a broken one.
      run: () =>
        void run(async () => {
          store.notify('Naming the session…')
          const result = await api(hostId).generateTitle(session.session_id)
          if (result.title) store.notify(result.title)
          else store.notify('The model did not return a usable title', 'error')
        }),
    },
    {
      label: session.pinned ? 'Unpin' : 'Pin',
      run: () => void store.patchSessionField(hostId, session.session_id, { pinned: !session.pinned }),
    },
  )

  // A child menu rather than entries of its own: the list comes from the daemon
  // when the menu opens, and a parent row keeps its one-row height whether or
  // not it has arrived — entries appearing here would grow the menu under the
  // pointer, on top of the actions below.
  actions.push({
    label: 'Permission mode',
    children: modeActions(hostId, session, providers),
  })

  // Nothing to end when it has already ended, and the row itself carries the
  // Resume that a terminated session is waiting for.
  if (!canResume(session)) {
    actions.push({
      label: 'Terminate',
      danger: true,
      run: () => {
        if (confirm('Terminate this session? The agent stops, and only Resume brings it back.')) {
          void run(() => api(hostId).terminate(session.session_id))
        }
      },
    })
  }

  actions.push({
    label: 'Delete',
    danger: true,
    run: () => {
      // Deleting drops the daemon's record; the agent's own transcript on disk
      // is untouched, which is worth saying before the click.
      if (confirm('Remove this session from Helios? The transcript file stays on disk.')) {
        store.closeTab(`${hostId}:${session.session_id}`)
        void run(() => api(hostId).deleteSession(session.session_id))
      }
    },
  })

  return actions
}
