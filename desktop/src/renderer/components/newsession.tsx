import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { removeFirst } from '../attachments.ts'
import { directoriesQuery, dirQuery, modelsQuery, providersQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import {
  AttachButton,
  AttachmentChips,
  PasteOffer,
  useAttachments,
  useDropTarget,
} from './attach.tsx'
import { Chevron, Console, Folder, Shield, Spark } from './icons.tsx'
import { completionTarget, completionsIn } from './dirpath.ts'
import { tintOf } from './grouping.ts'
import { type DirectoryInfo, type ModelInfo, type ProviderInfo } from '../../shared/models.ts'

const NO_PROVIDERS: ProviderInfo[] = []
const NO_MODELS: ModelInfo[] = []
const NO_DIRECTORIES: DirectoryInfo[] = []

/** Module scope so the cache memoises the filtered list. */
const onlyReady = (all: ProviderInfo[]): ProviderInfo[] => all.filter((p) => p.ready !== false)

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
  // Held out here rather than in the picker, which unmounts with its popover: a
  // half-typed path must still be there when the chip is opened again.
  const [typed, setTyped] = useState('')
  const files = useAttachments()
  const { dropping, handlers: dropHandlers } = useDropTarget((dropped) => void files.attach(dropped))
  const [starting, setStarting] = useState(false)
  const shell = useRef<HTMLDivElement | null>(null)
  const dismissing = useRef(false)
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
  const { data: directories = NO_DIRECTORIES, isPending: findingDirectories } = useQuery({
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
    if (!hostId || providers.length === 0 || findingDirectories || settled.current === hostId) return
    settled.current = hostId
    const first = providers[0]
    if (first && !providers.some((p) => p.id === provider)) setProvider(first.id)
    setCwd(seeded.current || directories[0]?.cwd || '')
    seeded.current = ''
    // provider and cwd are deliberately not dependencies: this settles them.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostId, providers, directories, findingDirectories])

  const { data: models = NO_MODELS } = useQuery({
    ...modelsQuery(hostId ?? '', provider),
    enabled: !!hostId && provider !== '',
  })

  const selected = providers.find((p) => p.id === provider)
  const modes = selected?.permission_modes ?? []

  /**
   * The first turn, when there are files on it.
   *
   * An upload needs a session to belong to — it lands in
   * ~/.helios/uploads/{id} and the handler 404s without the record
   * (internal/server/uploads.go) — and the id is what the create call is there
   * to return. So an attached prompt cannot be the one the agent launches
   * with: the session is started silent, the files go up against the id it
   * came back with, and the prompt naming them is sent after.
   *
   * Nothing here undoes the session. It is running by the time any of this can
   * fail, and killing an agent over an upload is a worse answer than a prompt
   * that arrives without its attachments.
   */
  const firstTurn = async (sessionId: string, text: string): Promise<void> => {
    let message = text
    try {
      message = await files.store(hostId, sessionId, text)
    } catch (err) {
      const why = err instanceof Error ? err.message : String(err)
      store.notify(`Session started, but the files did not upload: ${why}`, 'error')
    }
    if (!message) return
    try {
      await api(hostId).sendPrompt(sessionId, message)
    } catch (err) {
      store.fail(err)
    }
  }

  const start = async (): Promise<void> => {
    if (!hostId || starting) return
    setStarting(true)
    try {
      const text = prompt.trim()
      const attached = files.files.length > 0
      const result = await api(hostId).createSession({
        provider,
        cwd: cwd || undefined,
        model: model || undefined,
        prompt: attached ? undefined : text || undefined,
        permission_mode: mode || undefined,
      })
      if (attached) await firstTurn(result.session_id, text)
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

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (event.key !== 'Escape') return
      if (shell.current?.querySelector('details.picker[open]')) return
      onClose()
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])

  useEffect(() => {
    const onDown = (): void => {
      dismissing.current = shell.current?.querySelector('details.picker[open]') != null
    }
    window.addEventListener('mousedown', onDown, true)
    return () => window.removeEventListener('mousedown', onDown, true)
  }, [])

  const place = directories.find((dir) => dir.cwd === cwd)
  const where = place?.project || basename(cwd) || 'Home'
  const tint = tintOf(tintKey(place, cwd))
  const host = hosts.find((h) => h.id === hostId)
  const modelName = models.find((m) => m.id === model)?.name ?? 'Default model'

  return (
    <div
      className="modal-backdrop"
      onClick={() => {
        if (dismissing.current) {
          dismissing.current = false
          return
        }
        if (!prompt.trim() && files.files.length === 0) onClose()
      }}
    >
      <div
        className={dropping ? 'composer dropping' : 'composer'}
        ref={shell}
        onClick={(event) => event.stopPropagation()}
        {...dropHandlers}
      >
        <header className="composer-head">
          <Picker
            title="Working directory"
            trigger={
              <>
                <span className="composer-dot" style={{ background: tint }} />
                <span className="composer-chip-name">{where}</span>
              </>
            }
            className="composer-place"
          >
            {(close) => (
              <DirectoryList
                hostId={hostId}
                directories={directories}
                cwd={cwd}
                typed={typed}
                onType={setTyped}
                onPick={(next) => {
                  setCwd(next)
                  close()
                }}
              />
            )}
          </Picker>

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
                  className={`composer-option ${option.id === hostId ? 'is-on' : ''}`}
                  onClick={() => {
                    setHostId(option.id)
                    close()
                  }}
                >
                  <span className="composer-option-name">
                    <span className={`dot ${hostStatus[option.id]?.state ?? 'offline'}`} />
                    {option.name}
                  </span>
                </button>
              ))
            }
          </Picker>
        </header>

        {files.pasted !== null && prompt.includes(files.pasted) && (
          <PasteOffer
            text={files.pasted}
            onFile={() => {
              const text = files.fileThePaste()
              if (text !== null) setPrompt((current) => removeFirst(current, text))
            }}
            onKeep={files.keepThePaste}
          />
        )}

        <AttachmentChips files={files.files} onRemove={files.remove} />

        <textarea
          className="composer-prompt"
          autoFocus
          rows={4}
          value={prompt}
          placeholder="What do you want to work on?"
          onChange={(event) => setPrompt(event.target.value)}
          onPaste={(event) => {
            // A screenshot on the clipboard comes through as a file. Let the
            // default run when there is none, or pasted text is lost.
            if (event.clipboardData.files.length > 0) {
              event.preventDefault()
              void files.attach(event.clipboardData.files)
              return
            }
            files.noticePaste(event.clipboardData.getData('text'))
          }}
          onKeyDown={(event) => {
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
                    className={`composer-option ${option.id === provider ? 'is-on' : ''}`}
                    onClick={() => {
                      setProvider(option.id)
                      close()
                    }}
                  >
                    <span className="composer-option-name">{option.name}</span>
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
                    className={`composer-option ${model === '' ? 'is-on' : ''}`}
                    onClick={() => {
                      setModel('')
                      close()
                    }}
                  >
                    <span className="composer-option-name">Default model</span>
                    <span className="composer-option-hint">Whatever the provider picks</span>
                  </button>
                  {models.map((option) => (
                    <button
                      key={option.id}
                      className={`composer-option ${option.id === model ? 'is-on' : ''}`}
                      onClick={() => {
                        setModel(option.id)
                        close()
                      }}
                    >
                      <span className="composer-option-name">{option.name}</span>
                      {option.description && <span className="composer-option-hint">{option.description}</span>}
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
                      className={`composer-option ${mode === '' ? 'is-on' : ''}`}
                      onClick={() => {
                        setMode('')
                        close()
                      }}
                    >
                      <span className="composer-option-name">Default permissions</span>
                    </button>
                    {modes.map((option) => (
                      <button
                        key={option}
                        className={`composer-option ${option === mode ? 'is-on' : ''}`}
                        onClick={() => {
                          setMode(option)
                          close()
                        }}
                      >
                        <span className="composer-option-name">{option}</span>
                      </button>
                    ))}
                  </>
                )}
              </Picker>
            )}

          </div>

          <div className="composer-actions">
            {/* With the actions rather than with the chips: the chips say what
                the session will be, and the paperclip is something done to the
                prompt, next to the button that sends it. */}
            <AttachButton
              onFiles={(chosen) => void files.attach(chosen)}
              disabled={starting}
              shortcut
            />
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
 * Where to start the session: the directories in use, and the filesystem.
 *
 * Two jobs that read as one control. The recents answer "back to where I was
 * working" and are what an untouched picker shows — they are the only rows that
 * can carry a project name and a count of what is running there. Typing is the
 * other job, "somewhere new", and that is completion against the disk: the
 * recents cannot answer it, because a directory only joins them once a session
 * has already been started in it.
 *
 * Whatever is typed stays committable as it stands, absolute or not. It is the
 * one thing the free-text input this replaced did better than any list, and no
 * amount of completion is worth losing it: a path the daemon knows about and
 * this end does not — a mount, a symlink, a directory made a second ago — has
 * to be reachable by typing it.
 */
function DirectoryList({
  hostId,
  directories,
  cwd,
  typed,
  onType,
  onPick,
}: {
  hostId: string
  directories: DirectoryInfo[]
  cwd: string
  typed: string
  onType: (typed: string) => void
  onPick: (cwd: string) => void
}): JSX.Element {
  const search = useRef<HTMLInputElement | null>(null)
  const wanted = typed.trim()
  const needle = wanted.toLowerCase()
  const { parent, prefix } = completionTarget(wanted)

  const recents = useMemo(
    () =>
      directories.filter(
        (dir) =>
          !needle ||
          dir.project.toLowerCase().includes(needle) ||
          dir.cwd.toLowerCase().includes(needle),
      ),
    [directories, needle],
  )

  // Keyed on the parent, so walking along one path re-reads a directory only
  // when the typing crosses a separator. An unreadable parent — a path that
  // does not exist yet, which is every path halfway through being typed — is
  // not an error worth reporting: there is simply nothing to complete with.
  const { data: listing, isFetching } = useQuery({
    ...dirQuery(hostId, wanted === '' ? '' : parent),
    retry: false,
  })

  const children = useMemo(
    () => completionsIn(listing?.entries ?? [], prefix, new Set(recents.map((dir) => dir.cwd))),
    [listing, prefix, recents],
  )

  const exact = directories.some((dir) => dir.cwd === wanted)
  // Home stays reachable while it is being spelled: it is a row like any other,
  // and hiding it the moment a key is pressed hides what was being reached for.
  const showHome = needle === '' || 'home'.startsWith(needle) || needle === '~'
  const nothing = children.length === 0 && recents.length === 0 && !showHome

  /** Fills in the top completion, as a shell does, and leaves the cursor going. */
  const complete = (): void => {
    const first = children[0]
    if (!first) return
    // The daemon's own absolute path, not the typed prefix with a name stuck on
    // the end: completing `wor` under home has to leave behind something that
    // still means the same directory when it is completed again.
    onType(`${first.path}/`)
    search.current?.focus()
  }

  return (
    <>
      <input
        className="picker-search"
        ref={search}
        autoFocus
        spellCheck={false}
        autoComplete="off"
        value={typed}
        placeholder="Type a path, or search…"
        onChange={(event) => onType(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Tab' && children.length > 0) {
            event.preventDefault()
            complete()
            return
          }
          if (event.key !== 'Enter') return
          event.preventDefault()
          // What is in the box wins over what is under it. Anything else makes
          // the escape hatch conditional on the list agreeing.
          if (wanted !== '') onPick(wanted)
        }}
      />
      <div className="picker-list">
        {wanted !== '' && !exact && (
          <button className="composer-option use-typed" onClick={() => onPick(wanted)}>
            <span className="composer-option-name">Use “{wanted}”</span>
            <span className="composer-option-hint">Start here whether or not it is listed</span>
          </button>
        )}
        {showHome && (
          <button className={`composer-option ${cwd === '' ? 'is-on' : ''}`} onClick={() => onPick('')}>
            <span className="composer-option-name">Home</span>
            <span className="composer-option-hint">~</span>
          </button>
        )}

        {recents.length > 0 && <p className="picker-section">Recent</p>}
        {recents.map((dir) => (
          <button
            key={dir.cwd}
            className={`composer-option ${dir.cwd === cwd ? 'is-on' : ''}`}
            onClick={() => onPick(dir.cwd)}
          >
            <span className="composer-option-name">
              <span className="composer-dot" style={{ background: tintOf(tintKey(dir, dir.cwd)) }} />
              {dir.project || basename(dir.cwd)}
              {dir.active_count > 0 && <span className="composer-option-count">{dir.active_count} active</span>}
            </span>
            <span className="composer-option-hint">{dir.cwd}</span>
          </button>
        ))}

        {children.length > 0 && (
          <p className="picker-section">
            In <span className="picker-section-path">{listing?.path ?? parent}</span>
          </p>
        )}
        {children.map((entry) => (
          <button
            key={entry.path}
            className={`composer-option ${entry.path === cwd ? 'is-on' : ''}`}
            onClick={() => onPick(entry.path)}
          >
            <span className="composer-option-name">
              <Folder />
              {entry.name}
            </span>
            <span className="composer-option-hint">{entry.path}</span>
          </button>
        ))}

        {nothing && isFetching && <p className="picker-empty">Looking…</p>}
        {nothing && !isFetching && (
          <p className="picker-empty">Nothing here by that name — the path above is still yours to use.</p>
        )}
      </div>
    </>
  )
}

function Picker({
  title,
  trigger,
  align = 'left',
  className = '',
  children,
}: {
  title: string
  trigger: React.ReactNode
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
      if (event.type === 'keydown') element.querySelector('summary')?.focus()
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
    box.current?.querySelector('summary')?.focus()
  }

  const onKeyDown = (event: React.KeyboardEvent<HTMLElement>): void => {
    const keys = ['ArrowDown', 'ArrowUp', 'Home', 'End']
    if (!keys.includes(event.key)) return
    const typing = document.activeElement instanceof HTMLInputElement
    if (typing && (event.key === 'Home' || event.key === 'End')) return

    const options = Array.from(
      box.current?.querySelectorAll<HTMLElement>('.picker-body .composer-option') ?? [],
    )
    if (options.length === 0) return
    event.preventDefault()

    const at = options.indexOf(document.activeElement as HTMLElement)
    const to =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? options.length - 1
          : event.key === 'ArrowDown'
            ? (at + 1) % options.length
            : at <= 0
              ? options.length - 1
              : at - 1
    options[to]?.focus()
  }

  return (
    <details
      className={`picker ${className}`.trim()}
      ref={box}
      onKeyDown={onKeyDown}
      onToggle={() => setOpen(box.current?.open ?? false)}
    >
      <summary title={title}>
        {trigger}
        <Chevron dir="down" className="picker-caret" />
      </summary>
      {open && (
        <div className={`picker-body ${align === 'right' ? 'to-right' : ''}`}>{children(close)}</div>
      )}
    </details>
  )
}

function tintKey(dir: DirectoryInfo | undefined, cwd: string): string {
  if (dir) return (dir.project || dir.cwd).toLowerCase()
  return cwd.toLowerCase() || 'home'
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
