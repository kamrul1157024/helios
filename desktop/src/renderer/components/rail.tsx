import { store, useStore, type SidebarMode } from '../store.ts'
import { Chevron, Clock, Gear, ListRows } from './icons.tsx'

/**
 * What the window is showing, chosen from the left edge.
 *
 * A mode owns both columns: its own list in the sidebar, its own panel beside
 * it. That is why the switch is out here rather than inside the sidebar, where
 * it used to be a pair of words above a list it was not part of.
 *
 * Settings sits at the bottom, apart from the two lists: it is where you go to
 * change the app, not another thing the app is holding.
 */
export function Rail(): JSX.Element {
  const mode = useStore((s) => s.sidebarMode)
  const open = useStore((s) => s.sidebarOpen)

  /**
   * Picking a mode brings the list back.
   *
   * Asking for the schedules while the list is folded away and being shown
   * nothing is the obvious trap here: the button would look broken. Picking the
   * mode that is already showing folds it instead, which is the toggle every
   * editor puts on the same button.
   */
  const item = (id: SidebarMode, label: string, icon: JSX.Element): JSX.Element => (
    <button
      className={mode === id && open ? 'rail-item on' : 'rail-item'}
      // Pressed rather than current: these are three states of one control,
      // and only one of them is on at a time.
      aria-pressed={mode === id && open}
      aria-label={label}
      title={label}
      onClick={() => {
        if (mode === id) store.toggleSidebar()
        else {
          store.setSidebarMode(id)
          store.toggleSidebar(true)
        }
      }}
    >
      {icon}
    </button>
  )

  return (
    <nav className="rail" aria-label="Modes">
      <button
        className="rail-item rail-fold"
        aria-label={open ? 'Hide the list' : 'Show the list'}
        aria-expanded={open}
        title={`${open ? 'Hide' : 'Show'} the list (⌘B)`}
        onClick={() => store.toggleSidebar()}
      >
        <Chevron dir={open ? 'left' : 'right'} />
      </button>
      {item('sessions', 'Sessions', <ListRows />)}
      {item('schedules', 'Schedules', <Clock />)}
      <span className="grow" />
      {item('settings', 'Settings', <Gear />)}
    </nav>
  )
}
