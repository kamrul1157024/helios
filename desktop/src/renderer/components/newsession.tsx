import { useEffect, useRef, useState } from 'react'

import { api } from '../bridge.ts'
import { store, useStore } from '../store.ts'
import type { DirectoryInfo, ModelInfo, ProviderInfo } from '../../shared/models.ts'

export function NewSessionDialog({
  seed,
  onClose,
}: {
  /** Host and directory the dialog was opened for, from a project's own
   *  new-session button. Null when it was opened from the toolbar. */
  seed?: { hostId: string; cwd: string; group?: string } | null
  onClose: () => void
}): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const [hostId, setHostId] = useState(seed?.hostId ?? hosts[0]?.id ?? '')
  const [providers, setProviders] = useState<ProviderInfo[]>([])
  const [models, setModels] = useState<ModelInfo[]>([])
  const [directories, setDirectories] = useState<DirectoryInfo[]>([])
  const [provider, setProvider] = useState('claude')
  const [model, setModel] = useState('')
  const [cwd, setCwd] = useState('')
  const [mode, setMode] = useState('')
  const [prompt, setPrompt] = useState('')
  const [starting, setStarting] = useState(false)
  // Spent on the first load and not again: switching hosts after that has to
  // reset the directory, because a path from one machine is not a path on the
  // next, and the seed was a path on the machine it came from.
  const seeded = useRef(seed?.cwd ?? '')

  useEffect(() => {
    if (!hostId) return
    let cancelled = false
    const client = api(hostId)
    void Promise.all([client.providers(), client.listDirectories()])
      .then(([providerList, dirs]) => {
        if (cancelled) return
        setProviders(providerList)
        setDirectories(dirs)
        const first = providerList[0]
        if (first && !providerList.some((p) => p.id === provider)) setProvider(first.id)
        // Reset rather than preserve: a directory is meaningful only on the
        // host it came from, and carrying one across a host switch starts the
        // session in a path that does not exist there.
        setCwd(seeded.current || dirs[0]?.cwd || '')
        seeded.current = ''
      })
      .catch((err: unknown) => store.fail(err))
    return () => {
      cancelled = true
    }
    // Runs on host change only, so edits the user makes while staying on one
    // host survive; provider and cwd are deliberately not dependencies.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostId])

  useEffect(() => {
    if (!hostId || !provider) return
    let cancelled = false
    api(hostId)
      .models(provider)
      .then((list) => {
        if (!cancelled) setModels(list)
      })
      .catch(() => {
        if (!cancelled) setModels([])
      })
    return () => {
      cancelled = true
    }
  }, [hostId, provider])

  const selected = providers.find((p) => p.id === provider)
  const modes = selected?.permission_modes ?? []

  const start = async (): Promise<void> => {
    if (!hostId || starting) return
    setStarting(true)
    try {
      const result = await api(hostId).createSession({
        provider,
        cwd: cwd || undefined,
        model: model || undefined,
        prompt: prompt || undefined,
        permission_mode: mode || undefined,
      })
      // Filed before the refresh, so the list arrives with it already in the
      // group the + was pressed on. The directory only seeded the dialog; the
      // group is what the button actually promised.
      if (seed?.group) await store.setSessionGroup(hostId, result.session_id, seed.group)
      await store.refreshSessions(hostId)
      store.select(hostId, result.session_id)
      const session = store
        .getSnapshot()
        .sessions[hostId]?.find((s) => s.session_id === result.session_id)
      // Freshly created sessions are always warm, so attaching never wakes.
      if (session) await store.openTerminal(hostId, session, false)
      onClose()
    } catch (err) {
      store.fail(err)
    } finally {
      setStarting(false)
    }
  }

  return (
    <Modal title="New session" onClose={onClose}>
      {hosts.length > 1 && (
        <label className="field">
          <span>Host</span>
          <select value={hostId} onChange={(event) => setHostId(event.target.value)}>
            {hosts.map((host) => (
              <option key={host.id} value={host.id}>
                {host.name}
              </option>
            ))}
          </select>
        </label>
      )}

      <label className="field">
        <span>Provider</span>
        <select value={provider} onChange={(event) => setProvider(event.target.value)}>
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>

      <label className="field">
        <span>Directory</span>
        <input
          list="known-directories"
          value={cwd}
          placeholder="~ (home)"
          spellCheck={false}
          onChange={(event) => setCwd(event.target.value)}
        />
        {/* The daemon sends objects, not paths: cwd is the value, and the
            project name is the label that tells two checkouts of the same
            repository apart. */}
        <datalist id="known-directories">
          {directories.map((dir) => (
            <option key={dir.cwd} value={dir.cwd} label={dir.project || undefined} />
          ))}
        </datalist>
      </label>

      <label className="field">
        <span>Model</span>
        <select value={model} onChange={(event) => setModel(event.target.value)}>
          <option value="">Default</option>
          {models.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name}
            </option>
          ))}
        </select>
      </label>

      {modes.length > 0 && (
        <label className="field">
          <span>Permission mode</span>
          <select value={mode} onChange={(event) => setMode(event.target.value)}>
            <option value="">Provider default</option>
            {modes.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </label>
      )}

      <label className="field">
        <span>First prompt (optional)</span>
        {/* The box says a field; the placeholder says what the field is for.
            Without it this reads as a note to self rather than the first thing
            the agent will be asked. */}
        <textarea
          rows={4}
          value={prompt}
          placeholder="What should the agent start on? Left empty, the session opens at a prompt."
          onChange={(event) => setPrompt(event.target.value)}
        />
      </label>

      <div className="modal-actions">
        <button className="ghost" onClick={onClose}>
          Cancel
        </button>
        <button disabled={!hostId || starting} onClick={() => void start()}>
          {starting ? 'Starting…' : 'Start session'}
        </button>
      </div>
    </Modal>
  )
}

export function Modal({
  title,
  onClose,
  children,
}: {
  title: string
  onClose: () => void
  children: React.ReactNode
}): JSX.Element {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" onClick={(event) => event.stopPropagation()}>
        <header className="modal-head">
          <h2>{title}</h2>
          <button className="icon-btn" onClick={onClose}>
            ×
          </button>
        </header>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  )
}
