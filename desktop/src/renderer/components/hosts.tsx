import { useEffect, useState } from 'react'

import { bridge } from '../bridge.ts'
import { store, useStore } from '../store.ts'
import { Modal } from './newsession.tsx'

/**
 * Adding a daemon.
 *
 * A local one pairs itself — the app can already reach the daemon's loopback
 * admin port, which is the same trust the CLI assumes. A remote one needs the
 * `helios://pair` link that `helios setup` prints as a QR, since a desktop has
 * no camera to point at it.
 */
export function HostsDialog({ onClose }: { onClose: () => void }): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const hostStatus = useStore((s) => s.hostStatus)
  const pairingLink = useStore((s) => s.pairingLink)
  const [link, setLink] = useState('')
  const [busy, setBusy] = useState(false)

  // A helios:// link handed to the app by the OS lands straight in the field.
  useEffect(() => {
    if (pairingLink) {
      setLink(pairingLink)
      store.setPairingLink(null)
    }
  }, [pairingLink])

  const pairLocal = async (): Promise<void> => {
    setBusy(true)
    try {
      await bridge.hosts.pairLocal()
      store.notify('Paired with the daemon on this machine')
    } catch (err) {
      store.fail(err)
    } finally {
      setBusy(false)
    }
  }

  const pairRemote = async (): Promise<void> => {
    if (!link.trim()) return
    setBusy(true)
    try {
      await bridge.hosts.pairURL(link.trim())
      setLink('')
      store.notify('Host added')
    } catch (err) {
      store.fail(err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title="Hosts" onClose={onClose}>
      <div className="host-rows">
        {hosts.map((host) => (
          <div key={host.id} className="host-row">
            <span className={`dot ${hostStatus[host.id]?.state ?? 'connecting'}`} />
            <div className="host-row-text">
              <input
                className="host-name-input"
                defaultValue={host.name}
                onBlur={(event) => {
                  const name = event.target.value.trim()
                  if (name && name !== host.name) void bridge.hosts.rename(host.id, name)
                }}
              />
              <span className="muted">
                {host.url}
                {host.local ? ' · local' : ''}
              </span>
              {hostStatus[host.id]?.error && (
                <span className="error-text">{hostStatus[host.id]?.error}</span>
              )}
            </div>
            <button
              className="ghost"
              onClick={() => {
                void bridge.hosts.remove(host.id)
              }}
            >
              Remove
            </button>
          </div>
        ))}
        {hosts.length === 0 && <p className="empty-note">No hosts yet.</p>}
      </div>

      <hr />

      <div className="field">
        <span>This machine</span>
        <button disabled={busy} onClick={() => void pairLocal()}>
          Pair with local daemon
        </button>
        <small>Requires a daemon running here; it authorises itself over loopback.</small>
      </div>

      <label className="field">
        <span>Remote daemon</span>
        <input
          value={link}
          placeholder="helios://pair?url=…&token=…"
          spellCheck={false}
          onChange={(event) => setLink(event.target.value)}
        />
        <small>
          Run <code>helios setup</code> on the other machine and paste the link under its QR code.
        </small>
      </label>

      <div className="modal-actions">
        <button className="ghost" onClick={onClose}>
          Close
        </button>
        <button disabled={busy || !link.trim()} onClick={() => void pairRemote()}>
          {busy ? 'Pairing…' : 'Add host'}
        </button>
      </div>
    </Modal>
  )
}
