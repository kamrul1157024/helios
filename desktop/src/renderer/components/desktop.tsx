import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api, bridge } from '../bridge.ts'
import { DesktopConnection, type LinkState } from '../desktop-connection.ts'
import {
  ASPECTS,
  DEFAULT_PREFS,
  DPI_LIMITS,
  DPI_PRESETS,
  HEIGHTS,
  LIMITS,
  clampDpi,
  clampSize,
  parsePrefs,
  placement,
  remoteSize,
  type DisplayMode,
  type DisplayPrefs,
  type Size,
} from '../desktop-geometry.ts'
import { screenFor } from '../desktop-state.ts'
import type { BridgeError } from '../errors.ts'
import { loadGuacamole } from '../guacamole.ts'
import { effectsFor, keys } from '../keys.ts'
import { desktopQuery } from '../queries.ts'
import { queryClient } from '../query-client.ts'
import { applyFonts, applyTheme } from '../../shared/theme/apply.ts'
import type { DesktopState, DesktopStep } from '../../shared/models.ts'
import { Close, Copy, Monitor, PopOut, Sliders } from './icons.tsx'

/**
 * A host's remote desktop (spec 66, D7 and D9).
 *
 * What it shows follows `GET /api/desktop`, kept current by `desktop_state`
 * events: an Install button, the steps of an install as they run, the command
 * to run by hand, an error with Retry — and, once the desktop is ready, the
 * screen itself.
 */
export function DesktopView({
  hostId,
  hostName,
  onClose,
  onPopOut,
}: {
  hostId: string
  hostName: string
  onClose: () => void
  /** Absent in a popped-out window, which is already the window of its own. */
  onPopOut?: () => void
}): JSX.Element {
  const { data, error, refetch } = useQuery(desktopQuery(hostId))
  const screen = screenFor(data, error)
  const [prefs, setPrefs] = useDisplayPrefs(hostId)

  return (
    <div className="desktop-view">
      <header className="desktop-bar">
        <Monitor />
        <span className="desktop-title">Desktop</span>
        <span className="desktop-host">{hostName}</span>
        {screen.kind === 'ready' && data?.display && <span className="desktop-meta">display {data.display}</span>}
        <span className="grow" />
        {screen.kind === 'ready' && data && (
          <DisplayMenu hostId={hostId} prefs={prefs} onPrefs={setPrefs} hostDpi={data.dpi} />
        )}
        {onPopOut && (
          <button className="icon-btn" title="Open in a window of its own" aria-label="Pop out" onClick={onPopOut}>
            <PopOut />
          </button>
        )}
        <button className="icon-btn" title="Close the desktop" aria-label="Close the desktop" onClick={onClose}>
          <Close />
        </button>
      </header>

      <div className="desktop-body">
        {screen.kind === 'loading' && (
          <div className="panel-loading">
            <span className="spinner" />
            <span>Asking the host about its desktop…</span>
          </div>
        )}
        {screen.kind === 'absent' && (
          <Notice title="This daemon has no remote desktop">
            Update Helios on the host, then turn on <code>desktop.enabled</code> in its settings.
          </Notice>
        )}
        {screen.kind === 'unreachable' && (
          <Notice title="Could not reach the desktop">
            <p>{screen.text}</p>
            <button className="tonal" onClick={() => void refetch()}>
              Retry
            </button>
          </Notice>
        )}
        {screen.kind === 'unsupported' && (
          <Notice title="Remote desktop needs a Linux host">
            This host runs macOS or Windows. Helios can show the desktop of a Linux machine only.
          </Notice>
        )}
        {screen.kind === 'install' && (
          <InstallPrompt hostId={hostId} missing={screen.missing} command={screen.command} />
        )}
        {screen.kind === 'command' && <InstallCommand command={screen.command} />}
        {screen.kind === 'progress' && (
          <Notice title={screen.title} busy>
            <StepList steps={screen.steps} />
          </Notice>
        )}
        {screen.kind === 'failed' && (
          <Notice title="The desktop did not start">
            <p className="desktop-error">{screen.error}</p>
            <StepList steps={screen.steps} />
            <InstallButton hostId={hostId} label="Retry" />
          </Notice>
        )}
        {screen.kind === 'ready' && data && <DesktopScreen hostId={hostId} state={data} prefs={prefs} />}
      </div>
    </div>
  )
}

/**
 * The desktop in a window of its own.
 *
 * No store behind it — that is the main window's — so it takes the two things
 * it needs from the bridge directly: the host's name, and the host's events,
 * which it folds into its own cache with the same mapping the store uses.
 */
export function DesktopWindow({ hostId }: { hostId: string }): JSX.Element {
  const [name, setName] = useState('')

  useEffect(() => {
    void bridge.hosts.list().then((hosts) => setName(hosts.find((host) => host.id === hostId)?.name ?? ''))
    const offEvents = bridge.hosts.onEvent(({ hostId: from, event }) => {
      for (const effect of effectsFor(from, event)) {
        if (effect.kind === 'set') queryClient.setQueryData(effect.queryKey, effect.data)
        else if (effect.kind === 'invalidate') void queryClient.invalidateQueries({ queryKey: effect.queryKey })
      }
    })
    const offTheme = bridge.theme.onChanged(({ theme, glass, fonts }) => {
      applyTheme(document.documentElement, theme, glass)
      applyFonts(document.documentElement, fonts)
    })
    return () => {
      offEvents()
      offTheme()
    }
  }, [hostId])

  useEffect(() => {
    document.title = name ? `Desktop — ${name}` : 'Desktop'
  }, [name])

  return (
    <div className="desktop-window">
      <DesktopView hostId={hostId} hostName={name} onClose={() => window.close()} />
    </div>
  )
}

// ─── The states before the screen ───────────────────────────────────────────

function Notice({ title, busy = false, children }: { title: string; busy?: boolean; children: React.ReactNode }): JSX.Element {
  return (
    <div className="desktop-notice">
      <h3>
        {busy && <span className="spinner tiny" />}
        {title}
      </h3>
      <div className="desktop-notice-body">{children}</div>
    </div>
  )
}

function InstallPrompt({ hostId, missing, command }: { hostId: string; missing: string[]; command: string }): JSX.Element {
  // The state said sudo works and the install found it does not: the daemon
  // checks again at the moment it needs it, and its answer is the one to show.
  const [needsRoot, setNeedsRoot] = useState(false)
  if (needsRoot) return <InstallCommand command={command} />
  return (
    <Notice title="This host has no desktop yet">
      <p>
        Helios can install one now: xrdp, XFCE and guacd. Agents on this host then draw their windows on it, and you
        can watch them here.
      </p>
      {missing.length > 0 && <p className="muted">Missing: {missing.join(', ')}</p>}
      <InstallButton hostId={hostId} label="Install the desktop" onNeedsRoot={() => setNeedsRoot(true)} />
    </Notice>
  )
}

/**
 * Starts the install, or starts it again after a failure — every step finds
 * its own work done and passes at once, so Retry is the same request.
 */
function InstallButton({
  hostId,
  label,
  onNeedsRoot,
}: {
  hostId: string
  label: string
  onNeedsRoot?: () => void
}): JSX.Element {
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')

  const install = async (): Promise<void> => {
    setBusy(true)
    setFailure('')
    try {
      await api(hostId).installDesktop()
    } catch (err) {
      const code = (err as BridgeError).code
      // Already running is what was asked for; the state says so.
      if (code === 'needs_root') onNeedsRoot?.()
      else if (code !== 'busy') setFailure(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
      void queryClient.invalidateQueries({ queryKey: keys.desktop(hostId) })
    }
  }

  return (
    <div className="desktop-actions">
      <button className="filled" disabled={busy} onClick={() => void install()}>
        {label}
      </button>
      {failure && <span className="desktop-error">{failure}</span>}
    </div>
  )
}

function InstallCommand({ command }: { command: string }): JSX.Element {
  const [copied, setCopied] = useState(false)
  return (
    <Notice title="Helios needs root once to install the desktop">
      <p>Run this in a terminal on the host, then come back:</p>
      <div className="desktop-command">
        <code>{command}</code>
        <button
          className="icon-btn"
          title="Copy the command"
          aria-label="Copy the command"
          onClick={() => {
            void navigator.clipboard.writeText(command).then(() => setCopied(true))
          }}
        >
          <Copy />
        </button>
        {copied && <span className="muted">Copied</span>}
      </div>
    </Notice>
  )
}

const STEP_MARKS: Record<Exclude<DesktopStep['state'], 'running'>, string> = {
  pending: '○',
  done: '✓',
  skipped: '–',
  failed: '✕',
}

function StepList({ steps }: { steps: DesktopStep[] }): JSX.Element | null {
  if (steps.length === 0) return null
  return (
    <ol className="desktop-steps">
      {steps.map((step) => (
        <li key={step.id} className={`desktop-step ${step.state}`} data-step={step.id}>
          <span className="desktop-step-mark">
            {step.state === 'running' ? <span className="spinner tiny" /> : STEP_MARKS[step.state]}
          </span>
          <span className="desktop-step-label">{step.label}</span>
          {step.line && step.state !== 'done' && <code className="desktop-step-line">{step.line}</code>}
        </li>
      ))}
    </ol>
  )
}

// ─── The screen ─────────────────────────────────────────────────────────────

/** How long the view's size must hold still before the remote screen follows it. */
const RESIZE_DEBOUNCE = 250

/** What the overlay says while the line is anything but connected. */
function linkWords(link: LinkState): string {
  switch (link.kind) {
    case 'connecting':
      return 'Connecting…'
    case 'reconnecting':
      return `Connection lost — reconnecting (attempt ${link.attempt})…`
    case 'not-ready':
      return 'The desktop is not ready — waiting for it…'
    case 'failed':
      return link.text
    case 'connected':
      return ''
  }
}

function DesktopScreen({ hostId, state, prefs }: { hostId: string; state: DesktopState; prefs: DisplayPrefs }): JSX.Element {
  const viewport = useRef<HTMLDivElement | null>(null)
  const stage = useRef<HTMLDivElement | null>(null)
  const connection = useRef<DesktopConnection | null>(null)
  const [view, setView] = useState<Size | null>(null)
  const [remote, setRemote] = useState<Size | null>(null)
  const [link, setLink] = useState<LinkState>({ kind: 'connecting' })

  const pixelRatio = window.devicePixelRatio || 1
  const wanted = view ? remoteSize(prefs, view, pixelRatio) : null
  // Read on each dial rather than captured once: a redial after a drop should
  // ask for the size the view is now, not the size it was when it first opened.
  const wantedRef = useRef(wanted)
  wantedRef.current = wanted
  // The DPI the session already has, so the handshake does not change it. A
  // change afterwards is live (D9) and needs no new connection.
  const dpiRef = useRef(state.dpi)
  dpiRef.current = state.dpi

  useLayoutEffect(() => {
    const element = viewport.current
    if (!element) return
    // The outer size, scrollbars included: an exact screen that overflows
    // brings scrollbars, and measuring inside them would shrink the view,
    // resize a fit-height screen, take the scrollbars away and start over.
    const observer = new ResizeObserver(() => setView({ width: element.offsetWidth, height: element.offsetHeight }))
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  const measured = view !== null
  useEffect(() => {
    if (!measured) return
    let cancelled = false
    let opened: DesktopConnection | null = null
    loadGuacamole().then(
      (G) => {
        if (cancelled || !stage.current || !viewport.current) return
        opened = new DesktopConnection(G, stage.current, viewport.current, {
          hostId,
          dpi: dpiRef.current || DPI_PRESETS[0].dpi,
          size: () => wantedRef.current ?? DEFAULT_PREFS.custom,
          onLink: setLink,
          onScreen: setRemote,
        })
        connection.current = opened
        opened.start()
      },
      (err: unknown) => setLink({ kind: 'failed', text: err instanceof Error ? err.message : String(err) }),
    )
    return () => {
      cancelled = true
      opened?.dispose()
      connection.current = null
    }
  }, [hostId, measured])

  // A desktop that stopped being ready has a state worth reading again.
  useEffect(() => {
    if (link.kind === 'not-ready') void queryClient.invalidateQueries({ queryKey: keys.desktop(hostId) })
  }, [hostId, link.kind])

  // Live resize over the open tunnel, once the view has settled: a window drag
  // is a hundred sizes, and each one is an RDP display update.
  const wantedKey = wanted ? `${wanted.width}x${wanted.height}` : ''
  useEffect(() => {
    if (!wanted || link.kind !== 'connected') return
    if (remote && remote.width === wanted.width && remote.height === wanted.height) return
    const timer = window.setTimeout(() => connection.current?.resize(wanted), RESIZE_DEBOUNCE)
    return () => window.clearTimeout(timer)
  }, [wantedKey, link.kind])

  const shown = remote ?? wanted
  const place = view && shown ? placement(view, shown, prefs.exact, pixelRatio) : null
  useEffect(() => {
    if (place) connection.current?.setScale(place.scale)
  }, [place?.scale, link.kind])

  return (
    <div
      ref={viewport}
      className={`desktop-viewport ${prefs.exact ? 'exact' : ''}`}
      tabIndex={0}
      aria-label="Remote desktop. Click to type into it."
      data-link={link.kind}
      data-remote={remote ? `${remote.width}x${remote.height}` : ''}
    >
      <div
        ref={stage}
        className="desktop-stage"
        style={place ? { left: place.left, top: place.top, width: place.width, height: place.height } : undefined}
      />
      {link.kind !== 'connected' && (
        <div className={`desktop-overlay ${link.kind}`} role="status">
          {link.kind !== 'failed' && <span className="spinner tiny" />}
          <span>{linkWords(link)}</span>
          {link.kind === 'failed' && (
            <button className="tonal" onClick={() => connection.current?.retry()}>
              Retry
            </button>
          )}
        </div>
      )}
    </div>
  )
}

// ─── Display settings (D9) ──────────────────────────────────────────────────

const PREFS_KEY = 'helios.desktop.display.'

/** The mode, aspect and size, per host. The DPI is not here: it lives on the host. */
function useDisplayPrefs(hostId: string): [DisplayPrefs, (next: DisplayPrefs) => void] {
  const [prefs, setPrefs] = useState<DisplayPrefs>(() => readPrefs(hostId))
  useEffect(() => setPrefs(readPrefs(hostId)), [hostId])
  const save = (next: DisplayPrefs): void => {
    setPrefs(next)
    localStorage.setItem(PREFS_KEY + hostId, JSON.stringify(next))
  }
  return [prefs, save]
}

function readPrefs(hostId: string): DisplayPrefs {
  try {
    return parsePrefs(JSON.parse(localStorage.getItem(PREFS_KEY + hostId) ?? 'null'))
  } catch {
    return DEFAULT_PREFS
  }
}

const MODES: { mode: DisplayMode; label: string }[] = [
  { mode: 'fit', label: 'Fit' },
  { mode: 'aspect', label: 'Fixed aspect' },
  { mode: 'custom', label: 'Custom' },
]

function DisplayMenu({
  hostId,
  prefs,
  onPrefs,
  hostDpi,
}: {
  hostId: string
  prefs: DisplayPrefs
  onPrefs: (next: DisplayPrefs) => void
  hostDpi: number
}): JSX.Element {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!open) return
    const dismiss = (event: Event): void => {
      if (event instanceof KeyboardEvent && event.key !== 'Escape') return
      if (event.type === 'mousedown' && event.target instanceof Node && box.current?.contains(event.target)) return
      setOpen(false)
    }
    window.addEventListener('mousedown', dismiss)
    window.addEventListener('keydown', dismiss)
    return () => {
      window.removeEventListener('mousedown', dismiss)
      window.removeEventListener('keydown', dismiss)
    }
  }, [open])

  const set = (patch: Partial<DisplayPrefs>): void => onPrefs({ ...prefs, ...patch })

  return (
    <div className="desktop-menu" ref={box}>
      <button
        className="icon-btn"
        title="Screen size and text size"
        aria-label="Display settings"
        aria-expanded={open}
        onClick={() => setOpen((was) => !was)}
      >
        <Sliders />
      </button>
      {open && (
        <div className="desktop-menu-body" role="dialog" aria-label="Display settings">
          <section>
            <h4>Screen size</h4>
            <Segments
              label="Mode"
              options={MODES.map((one) => ({ value: one.mode, label: one.label }))}
              value={prefs.mode}
              onChange={(mode) => set({ mode })}
            />
            {prefs.mode === 'aspect' && (
              <>
                <Segments
                  label="Aspect ratio"
                  options={ASPECTS.map((aspect) => ({ value: aspect, label: aspect }))}
                  value={prefs.aspect}
                  onChange={(aspect) => set({ aspect })}
                />
                <Segments
                  label="Height"
                  options={[
                    ...HEIGHTS.map((height) => ({ value: String(height), label: String(height) })),
                    { value: 'fit', label: 'Fit height' },
                  ]}
                  value={String(prefs.height)}
                  onChange={(height) => set({ height: height === 'fit' ? 'fit' : Number(height) })}
                />
              </>
            )}
            {prefs.mode === 'custom' && <CustomSize size={prefs.custom} onApply={(custom) => set({ custom })} />}
          </section>
          <section>
            <h4>Pixels</h4>
            <Segments
              label="Pixels"
              options={[
                { value: 'scale', label: 'Scale to fit' },
                { value: 'exact', label: '1:1' },
              ]}
              value={prefs.exact ? 'exact' : 'scale'}
              onChange={(value) => set({ exact: value === 'exact' })}
            />
          </section>
          <DpiControl hostId={hostId} dpi={hostDpi} />
        </div>
      )}
    </div>
  )
}

function Segments<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string
  options: { value: T; label: string }[]
  value: string
  onChange: (value: T) => void
}): JSX.Element {
  return (
    <div className="seg-toggle" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          className={option.value === value ? 'on' : ''}
          aria-pressed={option.value === value}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}

function CustomSize({ size, onApply }: { size: Size; onApply: (size: Size) => void }): JSX.Element {
  const [width, setWidth] = useState(String(size.width))
  const [height, setHeight] = useState(String(size.height))
  const apply = (): void => {
    const next = clampSize({ width: Number(width) || size.width, height: Number(height) || size.height })
    setWidth(String(next.width))
    setHeight(String(next.height))
    onApply(next)
  }
  return (
    // Out-of-range values are clamped on Apply rather than refused by the
    // browser, so the bounds on the fields are hints, not validation.
    <form
      className="desktop-custom"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        apply()
      }}
    >
      <input
        type="number"
        aria-label="Width"
        min={LIMITS.minWidth}
        max={LIMITS.maxWidth}
        value={width}
        onChange={(event) => setWidth(event.target.value)}
      />
      <span>×</span>
      <input
        type="number"
        aria-label="Height"
        min={LIMITS.minHeight}
        max={LIMITS.maxHeight}
        value={height}
        onChange={(event) => setHeight(event.target.value)}
      />
      <button type="submit" className="tonal">
        Apply
      </button>
    </form>
  )
}

/**
 * The session's text size, set on the host and shown from the host.
 *
 * Not kept locally: it belongs to the desktop session, so a second viewer sees
 * the same size, and the value drawn here is whatever the host last reported.
 */
function DpiControl({ hostId, dpi }: { hostId: string; dpi: number }): JSX.Element {
  const [custom, setCustom] = useState(String(dpi || DPI_PRESETS[0].dpi))
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')

  useEffect(() => setCustom(String(dpi || DPI_PRESETS[0].dpi)), [dpi])

  const apply = async (value: number): Promise<void> => {
    const next = clampDpi(value)
    setBusy(true)
    setFailure('')
    try {
      const answer = await api(hostId).setDesktopDisplay(next)
      queryClient.setQueryData<DesktopState>(keys.desktop(hostId), (held) => (held ? { ...held, dpi: answer.dpi } : held))
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section>
      <h4>Text size</h4>
      <Segments
        label="DPI"
        options={DPI_PRESETS.map((preset) => ({ value: String(preset.dpi), label: preset.label }))}
        value={String(dpi)}
        onChange={(value) => void apply(Number(value))}
      />
      <form
        className="desktop-custom"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          void apply(Number(custom) || dpi)
        }}
      >
        <input
          type="number"
          aria-label="Custom DPI"
          min={DPI_LIMITS.min}
          max={DPI_LIMITS.max}
          value={custom}
          onChange={(event) => setCustom(event.target.value)}
        />
        <span className="muted">DPI</span>
        <button type="submit" className="tonal" disabled={busy}>
          Set
        </button>
      </form>
      {failure && <p className="desktop-error">{failure}</p>}
    </section>
  )
}
