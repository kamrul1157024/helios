import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { directoriesQuery, modelsQuery, providersQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import { Chevron, Console, Shield, Spark } from './icons.tsx'
import { tintOf } from './grouping.ts'
import { type DirectoryInfo, type ModelInfo, type ProviderInfo } from '../../shared/models.ts'

const NO_PROVIDERS: ProviderInfo[] = []
const NO_MODELS: ModelInfo[] = []
const NO_DIRECTORIES: DirectoryInfo[] = []

/** Module scope so the cache memoises the filtered list. */
const onlyReady = (all: ProviderInfo[]): ProviderInfo[] => all.filter((p) => p.ready !== false)

/**
 * A composer, not a form.
 *
 * The one thing a new session needs is the first prompt, and a stack of
 * labelled selects buried it under five rows the user answers the same way
 * every time. So the prompt is the dialog — a single box that takes focus on
 * open — and everything else is a chip around it: where it runs across the
 * top, what runs it along the bottom. Each chip already says its own value, so
 * none of them needs a label.
 */
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
  const hostStatus = useStore((s) => s.hostStatus)
  const [hostId, setHostId] = useState(seed?.hostId ?? hosts[0]?.id ?? '')
  const [provider, setProvider] = useState('claude')
  const [model, setModel] = useState('')
  const [cwd, setCwd] = useState('')
  const [mode, setMode] = useState('')
  const [prompt, setPrompt] = useState('')
  const [starting, setStarting] = useState(false)
  const shell = useRef<HTMLDivElement | null>(null)
  // Spent on the first load and not again: switching hosts after that has to
  // reset the directory, because a path from one machine is not a path on the
  // next, and the seed was a path on the machine it came from.
  const seeded = useRef(seed?.cwd ?? '')

  // Only agents that would actually start. An unready one — not installed, or
  // hooks missing — produces a session that runs and is never heard from, which
  // reads as a hang. `helios start` is where an unready agent is shown, with
  // what to do about it. `ready` is optional so an older daemon, which does not
  // send it, keeps offering everything it did before.
  const { data: providers = NO_PROVIDERS } = useQuery({
    ...providersQuery(hostId ?? ''),
    enabled: !!hostId,
    select: onlyReady,
  })
  const { data: directories = NO_DIRECTORIES } = useQuery({
    ...directoriesQuery(hostId ?? ''),
    enabled: !!hostId,
  })

  /**
   * Settles the provider and directory once the host has answered.
   *
   * On host change rather than on every answer: edits the user makes while
   * staying on one host have to survive, and a directory is meaningful only on
   * the machine it came from — carrying one across a switch starts the session
   * in a path that does not exist there.
   */
  const settled = useRef<string | null>(null)
  useEffect(() => {
    if (!hostId || providers.length === 0 || settled.current === hostId) return
    settled.current = hostId
    const first = providers[0]
    if (first && !providers.some((p) => p.id === provider)) setProvider(first.id)
    setCwd(seeded.current || directories[0]?.cwd || '')
    seeded.current = ''
    // provider and cwd are deliberately not dependencies: this settles them.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostId, providers, directories])

  const { data: models = NO_MODELS } = useQuery({
    ...modelsQuery(hostId ?? '', provider),
    enabled: !!hostId && provider !== '',
  })

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
      await store.invalidateSessionsFor(hostId)
      store.select(hostId, result.session_id)
      const session = store.sessionById(hostId, result.session_id)
      // Freshly created sessions are always warm, so attaching never wakes.
      if (session) await store.openTerminal(hostId, session, false)
      onClose()
    } catch (err) {
      store.fail(err)
    } finally {
      setStarting(false)
    }
  }

  // Escape closes the composer — unless a chip is open, which owns the key
  // first and would otherwise take the whole dialog down with its own popover.
  // Capture phase, because a chip's own dismissal listens on the window too
  // and runs first: by the time it bubbled here, the chip would already have
  // closed and this would see nothing open.
  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (event.key !== 'Escape') return
      if (shell.current?.querySelector('details.picker[open]')) return
      onClose()
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])

  const place = directories.find((dir) => dir.cwd === cwd)
  const where = place?.project || basename(cwd) || 'Home'
  const host = hosts.find((h) => h.id === hostId)
  const modelName = models.find((m) => m.id === model)?.name ?? 'Default model'

  return (
    <div
      className="modal-backdrop"
      // A stray click on the backdrop should not throw away a written prompt;
      // Escape and Cancel are the deliberate ways out.
      onClick={() => {
        if (!prompt.trim()) onClose()
      }}
    >
      <div className="composer" ref={shell} onClick={(event) => event.stopPropagation()}>
        <header className="composer-head">
          <Picker
            title="Working directory"
            trigger={
              <>
                <span className="composer-dot" style={{ background: tintOf(where.toLowerCase()) }} />
                <span className="composer-chip-name">{where}</span>
              </>
            }
            className="composer-place"
          >
            {(close) => (
              <DirectoryList
                directories={directories}
                cwd={cwd}
                onPick={(next) => {
                  setCwd(next)
                  close()
                }}
              />
            )}
          </Picker>

          {/* Shown even on a single host. The dot is the machine's connection
              state, and "which machine, and is it up" is worth answering before
              a prompt is written rather than after the session fails to start. */}
          <Picker
            title="Host"
            align="right"
            className="composer-host"
            trigger={
              <>
                <span className={`dot ${hostStatus[hostId]?.state ?? 'offline'}`} />
                <span className="composer-chip-name">{host?.name ?? 'Pick a host'}</span>
              </>
            }
          >
            {(close) =>
              hosts.map((option) => (
                <button
                  key={option.id}
                  className={`picker-item ${option.id === hostId ? 'is-on' : ''}`}
                  onClick={() => {
                    setHostId(option.id)
                    close()
                  }}
                >
                  <span className="picker-item-name">
                    <span className={`dot ${hostStatus[option.id]?.state ?? 'offline'}`} />
                    {option.name}
                  </span>
                </button>
              ))
            }
          </Picker>
        </header>

        <textarea
          className="composer-prompt"
          autoFocus
          rows={4}
          value={prompt}
          placeholder="What do you want to work on?"
          onChange={(event) => setPrompt(event.target.value)}
          onKeyDown={(event) => {
            // Enter starts the session, as the button's ↵ promises; a prompt
            // that wants paragraphs gets them with Shift.
            if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return
            event.preventDefault()
            void start()
          }}
        />

        <footer className="composer-foot">
          <div className="composer-chips">
            <Picker
              title="Provider"
              trigger={
                <>
                  <Console />
                  <span className="composer-chip-name">{selected?.name ?? provider}</span>
                </>
              }
            >
              {(close) =>
                providers.map((option) => (
                  <button
                    key={option.id}
                    className={`picker-item ${option.id === provider ? 'is-on' : ''}`}
                    onClick={() => {
                      setProvider(option.id)
                      close()
                    }}
                  >
                    <span className="picker-item-name">{option.name}</span>
                  </button>
                ))
              }
            </Picker>

            <Picker
              title="Model"
              trigger={
                <>
                  <Spark />
                  <span className="composer-chip-name">{modelName}</span>
                </>
              }
            >
              {(close) => (
                <>
                  <button
                    className={`picker-item ${model === '' ? 'is-on' : ''}`}
                    onClick={() => {
                      setModel('')
                      close()
                    }}
                  >
                    <span className="picker-item-name">Default model</span>
                    <span className="picker-item-hint">Whatever the provider picks</span>
                  </button>
                  {models.map((option) => (
                    <button
                      key={option.id}
                      className={`picker-item ${option.id === model ? 'is-on' : ''}`}
                      onClick={() => {
                        setModel(option.id)
                        close()
                      }}
                    >
                      <span className="picker-item-name">{option.name}</span>
                      {option.description && <span className="picker-item-hint">{option.description}</span>}
                    </button>
                  ))}
                </>
              )}
            </Picker>

            {modes.length > 0 && (
              <Picker
                title="Permission mode"
                trigger={
                  <>
                    <Shield />
                    <span className="composer-chip-name">{mode || 'Default permissions'}</span>
                  </>
                }
              >
                {(close) => (
                  <>
                    <button
                      className={`picker-item ${mode === '' ? 'is-on' : ''}`}
                      onClick={() => {
                        setMode('')
                        close()
                      }}
                    >
                      <span className="picker-item-name">Default permissions</span>
                    </button>
                    {modes.map((option) => (
                      <button
                        key={option}
                        className={`picker-item ${option === mode ? 'is-on' : ''}`}
                        onClick={() => {
                          setMode(option)
                          close()
                        }}
                      >
                        <span className="picker-item-name">{option}</span>
                      </button>
                    ))}
                  </>
                )}
              </Picker>
            )}

          </div>

          <div className="composer-actions">
            <button className="ghost" onClick={onClose}>
              Cancel
            </button>
            <button className="filled" disabled={!hostId || starting} onClick={() => void start()}>
              {starting ? 'Creating…' : 'Create'}
              {!starting && <kbd className="composer-key">↵</kbd>}
            </button>
          </div>
        </footer>
      </div>
    </div>
  )
}

/**
 * The directories the daemon knows, and any path the user types.
 *
 * The old field was an `<input list>`: it completed known paths but showed
 * none of them until something had been typed, so the common case — start
 * where I started last time — was hidden behind guessing the first letter.
 * The list is open here, and the filter doubles as the place to type a path
 * the daemon has never seen.
 */
function DirectoryList({
  directories,
  cwd,
  onPick,
}: {
  directories: DirectoryInfo[]
  cwd: string
  onPick: (cwd: string) => void
}): JSX.Element {
  const [query, setQuery] = useState('')
  const needle = query.trim().toLowerCase()

  const matches = useMemo(
    () =>
      directories.filter(
        (dir) =>
          !needle ||
          dir.project.toLowerCase().includes(needle) ||
          dir.cwd.toLowerCase().includes(needle),
      ),
    [directories, needle],
  )

  const typed = query.trim()
  const custom = typed.startsWith('/') || typed.startsWith('~')
  const known = directories.some((dir) => dir.cwd === typed)

  const first = (): void => {
    if (custom && !known) onPick(typed)
    else if (matches[0]) onPick(matches[0].cwd)
  }

  return (
    <>
      <input
        className="picker-search"
        autoFocus
        spellCheck={false}
        value={query}
        placeholder="Filter, or type a path…"
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={(event) => {
          if (event.key !== 'Enter') return
          event.preventDefault()
          first()
        }}
      />
      <div className="picker-list">
        {custom && !known && (
          <button className="picker-item" onClick={() => onPick(typed)}>
            <span className="picker-item-name">Use “{typed}”</span>
          </button>
        )}
        {!needle && (
          <button className={`picker-item ${cwd === '' ? 'is-on' : ''}`} onClick={() => onPick('')}>
            <span className="picker-item-name">Home</span>
            <span className="picker-item-hint">~</span>
          </button>
        )}
        {matches.map((dir) => (
          <button
            key={dir.cwd}
            className={`picker-item ${dir.cwd === cwd ? 'is-on' : ''}`}
            onClick={() => onPick(dir.cwd)}
          >
            <span className="picker-item-name">
              <span className="composer-dot" style={{ background: tintOf((dir.project || dir.cwd).toLowerCase()) }} />
              {dir.project || basename(dir.cwd)}
              {dir.active_count > 0 && <span className="picker-item-count">{dir.active_count} active</span>}
            </span>
            <span className="picker-item-hint">{dir.cwd}</span>
          </button>
        ))}
        {matches.length === 0 && !custom && <p className="picker-empty">No matching directory. Type a path to use one.</p>}
      </div>
    </>
  )
}

/**
 * A chip that says its own value and opens the choices under it.
 *
 * `<details>` for the same reason the sidebar's menu uses one — the open state
 * is the element's, not a piece of React state per chip — and dismissed the
 * same way, because a `<details>` left open only closes on its own summary.
 */
function Picker({
  title,
  trigger,
  align = 'left',
  className = '',
  children,
}: {
  title: string
  trigger: React.ReactNode
  /** Which edge the popover lines up with; 'right' for chips near the edge. */
  align?: 'left' | 'right'
  className?: string
  children: (close: () => void) => React.ReactNode
}): JSX.Element {
  const box = useRef<HTMLDetailsElement | null>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    const dismiss = (event: Event): void => {
      const element = box.current
      if (!element?.open) return
      if (event.type === 'keydown' && (event as KeyboardEvent).key !== 'Escape') return
      if (event.type === 'mousedown' && event.target instanceof Node && element.contains(event.target)) return
      element.open = false
      setOpen(false)
    }
    window.addEventListener('mousedown', dismiss)
    window.addEventListener('keydown', dismiss)
    window.addEventListener('blur', dismiss)
    return () => {
      window.removeEventListener('mousedown', dismiss)
      window.removeEventListener('keydown', dismiss)
      window.removeEventListener('blur', dismiss)
    }
  }, [])

  const close = (): void => {
    if (box.current) box.current.open = false
    setOpen(false)
  }

  return (
    <details
      className={`picker ${className}`.trim()}
      ref={box}
      onToggle={() => setOpen(box.current?.open ?? false)}
    >
      <summary title={title}>
        {trigger}
        <Chevron dir="down" className="picker-caret" />
      </summary>
      {/* Rendered only while open, so the filter field can take focus on the
          way in rather than waiting for a click it has already missed. */}
      {open && <div className={`picker-body ${align === 'right' ? 'to-right' : ''}`}>{children(close)}</div>}
    </details>
  )
}

function basename(path: string): string {
  return path.replace(/\/+$/, '').split('/').pop() ?? ''
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
