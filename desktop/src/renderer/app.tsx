import { useEffect, useState } from 'react'

import { bridge } from './bridge.ts'
import { store, terminalId, useStore } from './store.ts'
import { startVim } from './vim/driver.ts'
import { Detail } from './components/detail.tsx'
import { NewSessionDialog } from './components/newsession.tsx'
import { Rail } from './components/rail.tsx'
import { Sidebar } from './components/sidebar.tsx'
import { ReleaseNotes } from './components/updates.tsx'

export function App(): JSX.Element {
  const loading = useStore((s) => s.loading)
  const toast = useStore((s) => s.toast)
  const pairingLink = useStore((s) => s.pairingLink)
  // Starting a session is the last thing that interrupts the window. Settings
  // and hosts are modes now, reached from the rail.
  const [dialog, setDialog] = useState<'new' | null>(null)
  // Where the new-session dialog should start when it was opened from a
  // project rather than from the toolbar: the point of the project's own
  // button is that it does not ask again which project it meant.
  const [seed, setSeed] = useState<{ hostId: string; cwd: string; group?: string } | null>(null)

  useEffect(() => {
    void store.init()
  }, [])

  // A pairing link from the OS should surface the pane that can use it.
  useEffect(() => {
    if (pairingLink) store.openSettings('hosts')
  }, [pairingLink])

  // The Settings item in the app menu lives in the main process.
  useEffect(() => bridge.app.onOpenSettings(() => store.openSettings()), [])

  // One listener for the whole window, whether or not vim mode is on: the
  // driver reads the flag itself, so turning it on has to bind nothing.
  useEffect(() => startVim(), [])

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'n') {
        event.preventDefault()
        setSeed(null)
        setDialog('new')
      }
      // ⌘W closes the selected session's terminal, not the window: the
      // terminal is the only thing in this layout that can be closed.
      if ((event.metaKey || event.ctrlKey) && event.key === 'w') {
        const { selection, tabs } = store.getSnapshot()
        const id = selection ? terminalId(selection.hostId, selection.sessionId) : null
        if (id && tabs.some((t) => t.id === id)) {
          event.preventDefault()
          store.closeTab(id)
        }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  if (loading) {
    return (
      <div className="boot">
        {/* The window is frameless, so without a drag region the splash is a
            surface the user cannot move while it is up. */}
        <div className="titlebar" />
        <div className="boot-card">
          <span className="boot-mark">helios</span>
          <span className="spinner" />
          <span className="boot-caption">Connecting to your daemons…</span>
        </div>
      </div>
    )
  }

  return (
    <div className="app">
      <div className="titlebar" />
      <ReleaseNotes />
      <div className="body">
        <Rail />
        <Sidebar
          onNewSession={(from) => {
            setSeed(from ?? null)
            setDialog('new')
          }}
        />
        <main className="main" onFocusCapture={() => store.setColumn('main')}>
          <Detail />
        </main>
      </div>

      {dialog === 'new' && <NewSessionDialog seed={seed} onClose={() => setDialog(null)} />}

      {toast && <div className={`toast ${toast.kind}`}>{toast.text}</div>}
    </div>
  )
}
