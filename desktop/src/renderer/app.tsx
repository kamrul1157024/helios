import { useEffect, useState } from 'react'

import { store, useStore } from './store.ts'
import { Detail } from './components/detail.tsx'
import { HostsDialog } from './components/hosts.tsx'
import { NewSessionDialog } from './components/newsession.tsx'
import { Sidebar } from './components/sidebar.tsx'
import { TerminalTabs } from './components/terminal.tsx'

export function App(): JSX.Element {
  const loading = useStore((s) => s.loading)
  const toast = useStore((s) => s.toast)
  const tabs = useStore((s) => s.tabs)
  const pairingLink = useStore((s) => s.pairingLink)
  const [dialog, setDialog] = useState<'new' | 'hosts' | null>(null)

  useEffect(() => {
    void store.init()
  }, [])

  // A pairing link from the OS should surface the dialog that can use it.
  useEffect(() => {
    if (pairingLink) setDialog('hosts')
  }, [pairingLink])

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'n') {
        event.preventDefault()
        setDialog('new')
      }
      if ((event.metaKey || event.ctrlKey) && event.key === 'w' && store.getSnapshot().activeTab) {
        event.preventDefault()
        const active = store.getSnapshot().activeTab
        if (active) store.closeTab(active)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  if (loading) {
    return (
      <div className="boot">
        <span className="spinner" />
      </div>
    )
  }

  return (
    <div className="app">
      <div className="titlebar" />
      <div className="body">
        <Sidebar onNewSession={() => setDialog('new')} onAddHost={() => setDialog('hosts')} />
        <main className={tabs.length > 0 ? 'main split' : 'main'}>
          <Detail />
          <TerminalTabs />
        </main>
      </div>

      {dialog === 'new' && <NewSessionDialog onClose={() => setDialog(null)} />}
      {dialog === 'hosts' && <HostsDialog onClose={() => setDialog(null)} />}

      {toast && <div className={`toast ${toast.kind}`}>{toast.text}</div>}
    </div>
  )
}
