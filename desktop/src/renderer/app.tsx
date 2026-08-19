import { useEffect, useState } from 'react'

import { bridge } from './bridge.ts'
import type { UpdateInfo } from '../shared/models.ts'
import { store, terminalId, useStore } from './store.ts'
import { Detail } from './components/detail.tsx'
import { HostsDialog } from './components/hosts.tsx'
import { NewSessionDialog } from './components/newsession.tsx'
import { SettingsDialog } from './components/settings.tsx'
import { Sidebar } from './components/sidebar.tsx'

export function App(): JSX.Element {
  const loading = useStore((s) => s.loading)
  const toast = useStore((s) => s.toast)
  const pairingLink = useStore((s) => s.pairingLink)
  const [dialog, setDialog] = useState<'new' | 'hosts' | 'settings' | null>(null)

  useEffect(() => {
    void store.init()
  }, [])

  // A pairing link from the OS should surface the dialog that can use it.
  useEffect(() => {
    if (pairingLink) setDialog('hosts')
  }, [pairingLink])

  // The Settings item in the app menu lives in the main process.
  useEffect(() => bridge.app.onOpenSettings(() => setDialog('settings')), [])

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'n') {
        event.preventDefault()
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
      <UpdateBanner />
      <div className="body">
        <Sidebar
          onNewSession={() => setDialog('new')}
          onAddHost={() => setDialog('hosts')}
          onSettings={() => setDialog('settings')}
        />
        <main className="main">
          <Detail />
        </main>
      </div>

      {dialog === 'new' && <NewSessionDialog onClose={() => setDialog(null)} />}
      {dialog === 'hosts' && <HostsDialog onClose={() => setDialog(null)} />}
      {dialog === 'settings' && <SettingsDialog onClose={() => setDialog(null)} />}

      {toast && <div className={`toast ${toast.kind}`}>{toast.text}</div>}
    </div>
  )
}

/**
 * Says once that a newer release exists.
 *
 * None of the packages update themselves, so the most this can do is notice
 * and point at the download. Dismissing is remembered per version: the next
 * release earns one more mention, this one does not.
 */
function UpdateBanner(): JSX.Element | null {
  const [update, setUpdate] = useState<UpdateInfo | null>(null)

  useEffect(() => {
    void bridge.updates.check().then(setUpdate)
  }, [])

  if (!update) return null

  return (
    <div className="update-banner">
      <span>helios {update.version} is out.</span>
      {/* Worth saying outright: terminals are their own detached processes, so
          neither the daemon restarting nor this app closing interrupts a
          session that is running. */}
      <span className="update-note">
        Updating the daemon, desktop or app leaves running sessions alone.
      </span>
      {/* An anchor rather than a bridge call: the window open handler already
          sends https elsewhere, and nothing in this app navigates itself. */}
      <a className="ext-link" href={update.url} target="_blank" rel="noreferrer noopener">
        Release notes
      </a>
      <span className="grow" />
      <button
        className="link dismiss"
        onClick={() => {
          void bridge.updates.dismiss(update.version)
          setUpdate(null)
        }}
      >
        Dismiss
      </button>
    </div>
  )
}
