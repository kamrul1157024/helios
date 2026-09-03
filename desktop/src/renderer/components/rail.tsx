import { store, useStore, type SidebarMode } from '../store.ts'
import { Clock, Gear, ListRows } from './icons.tsx'

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

  const item = (id: SidebarMode, label: string, icon: JSX.Element): JSX.Element => (
    <button
      className={mode === id ? 'rail-item on' : 'rail-item'}
      // Pressed rather than current: these are three states of one control,
      // and only one of them is on at a time.
      aria-pressed={mode === id}
      aria-label={label}
      title={label}
      onClick={() => store.setSidebarMode(id)}
    >
      {icon}
    </button>
  )

  return (
    <nav className="rail" aria-label="Modes">
      {item('sessions', 'Sessions', <ListRows />)}
      {item('schedules', 'Schedules', <Clock />)}
      <span className="grow" />
      {item('settings', 'Settings', <Gear />)}
    </nav>
  )
}
