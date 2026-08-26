import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

import { api, bridge } from '../bridge.ts'
import { store, useStore } from '../store.ts'
import { Modal } from './newsession.tsx'
import { ALERT_TYPES } from '../../shared/notifications.ts'
import { BACKDROP_STYLES, MAX_BLUR, MAX_INTENSITY, MIN_INTENSITY, backdropValue } from '../../shared/theme/backdrop.ts'
import { parseColor, type BackdropSpec, type BackdropStyle, type Rgb } from '../../shared/theme/vscode.ts'
import type { HeliosTheme } from '../../shared/theme/resolve.ts'
import type { AppearancePrefs, BackdropState, NotificationPrefs, ThemeSummary } from '../../shared/models.ts'

/**
 * Which types can be silenced, in the order the phone lists them. Keeping the
 * two screens in the same shape means a user who has set this up once on the
 * phone recognises it here. Every type in ALERT_TYPES appears exactly once —
 * asserted below, so adding one to the store cannot silently skip the UI.
 */
const GROUPS: { heading: string; note: string; types: { type: string; label: string; detail: string }[] }[] = [
  {
    heading: 'Action required',
    note: 'These hold the agent until you answer.',
    types: [
      {
        type: 'claude.permission',
        label: 'Permission requests',
        detail: 'Claude is asking to use a tool that needs your approval.',
      },
      { type: 'claude.question', label: 'Questions', detail: 'Claude needs your input to continue.' },
      {
        type: 'claude.elicitation.form',
        label: 'Elicitation — form input',
        detail: 'An MCP server is requesting structured input.',
      },
      {
        type: 'claude.elicitation.url',
        label: 'Elicitation — authentication',
        detail: 'An MCP server needs you to authenticate via a URL.',
      },
      { type: 'claude.trust', label: 'Workspace trust', detail: 'Claude is asking to trust this workspace.' },
    ],
  },
  {
    heading: 'For information',
    note: 'These are news; nothing is waiting on you.',
    types: [
      { type: 'claude.done', label: 'Session completed', detail: 'Claude finished a task.' },
      { type: 'claude.error', label: 'Session error', detail: 'Claude stopped due to an error.' },
    ],
  },
]

const LISTED = GROUPS.flatMap((group) => group.types.map((t) => t.type))
const MISSING = ALERT_TYPES.filter((type) => !LISTED.includes(type))

const MODES: { value: AppearancePrefs['mode']; label: string; detail: string }[] = [
  { value: 'system', label: 'System', detail: 'Follow the OS, switching as it does.' },
  { value: 'light', label: 'Light', detail: 'Always the light theme below.' },
  { value: 'dark', label: 'Dark', detail: 'Always the dark theme below.' },
]

/**
 * One pane at a time, chosen from the left.
 *
 * The dialog had grown to a single column a screen and a half long, where the
 * theme pickers pushed the notification toggles out of sight — and the two have
 * nothing to do with each other.
 */
type SectionId = 'appearance' | 'sessions' | 'notifications'

const SECTIONS: { id: SectionId; label: string }[] = [
  { id: 'appearance', label: 'Appearance' },
  { id: 'sessions', label: 'Sessions' },
  { id: 'notifications', label: 'Notifications' },
]

export function SettingsDialog({ onClose }: { onClose: () => void }): JSX.Element {
  const [section, setSection] = useState<SectionId>('appearance')
  const [prefs, setPrefs] = useState<NotificationPrefs | null>(null)
  const [appearance, setAppearance] = useState<AppearancePrefs | null>(null)
  const [themes, setThemes] = useState<ThemeSummary[]>([])

  useEffect(() => {
    void bridge.prefs.get().then(setPrefs)
    void bridge.theme.prefs().then(setAppearance)
    void bridge.theme.list().then(setThemes)
  }, [])

  const setTheme = async (next: Partial<AppearancePrefs>): Promise<void> => {
    if (!appearance) return
    // Set locally first: the main process is the source of truth but it
    // answers with the resolved theme, not the preference, and a picker that
    // waits for a round trip feels like it ignored the click.
    setAppearance({ ...appearance, ...next })
    try {
      await bridge.theme.set(next)
    } catch (err) {
      store.fail(err)
      setAppearance(await bridge.theme.prefs())
    }
  }

  const reloadThemes = async (): Promise<void> => {
    try {
      setThemes(await bridge.theme.reload())
    } catch (err) {
      store.fail(err)
    }
  }

  const apply = async (change: Promise<NotificationPrefs>): Promise<void> => {
    try {
      setPrefs(await change)
    } catch (err) {
      store.fail(err)
    }
  }

  return (
    <Modal title="Settings" onClose={onClose}>
      <div className="settings-shell">
        <nav className="settings-nav">
          {SECTIONS.map((entry) => (
            <button
              key={entry.id}
              className={section === entry.id ? 'active' : ''}
              onClick={() => setSection(entry.id)}
            >
              {entry.label}
            </button>
          ))}
        </nav>

        <div className="settings-panel">
          {section === 'appearance' && (
            <>
      <section className="settings-group">
        <h3>
          Appearance
          <Info>
            Themes are VS Code colour themes. Drop any theme JSON into <code>~/.helios/themes</code> to add
            your own.
          </Info>
        </h3>

        <div className="mode-row">
          {MODES.map((mode) => (
            <label key={mode.value} className="check" title={mode.detail}>
              <input
                type="radio"
                name="theme-mode"
                checked={appearance?.mode === mode.value}
                disabled={!appearance}
                onChange={() => void setTheme({ mode: mode.value })}
              />
              {mode.label}
            </label>
          ))}
        </div>

        {/* Both slots stay visible on a pinned mode: they are what 'System'
            will switch between, and hiding the other one makes it look as
            though the choice was lost. */}
        <ThemePicker
          label="Dark theme"
          themes={themes.filter((theme) => theme.mode === 'dark')}
          value={appearance?.darkTheme}
          onPick={(id) => void setTheme({ darkTheme: id })}
        />
        <ThemePicker
          label="Light theme"
          themes={themes.filter((theme) => theme.mode === 'light')}
          value={appearance?.lightTheme}
          onPick={(id) => void setTheme({ lightTheme: id })}
        />
        <ThemePicker
          label="Terminal"
          themes={themes}
          value={appearance?.terminalTheme}
          match="Match UI theme"
          onPick={(id) => void setTheme({ terminalTheme: id })}
        />

        <ProseSize size={appearance?.proseSize} onPick={(size) => void setTheme({ proseSize: size })} />

        <Backdrop />

        <button className="ghost" onClick={() => void reloadThemes()}>
          Reload themes
        </button>
      </section>
            </>
          )}

          {section === 'sessions' && <SessionsPane />}

          {section === 'notifications' && (
            <>
      <h3>
        Notifications
        <Info>
          Requests always appear — on screen and on the tray. These toggles decide whether they also make a
          sound.
        </Info>
      </h3>

      <label className="check">
        <input
          type="checkbox"
          checked={prefs?.sound ?? true}
          disabled={!prefs}
          onChange={(event) => void apply(bridge.prefs.setSound(event.target.checked))}
        />
        Sound
      </label>

      {GROUPS.map((group) => (
        <section key={group.heading} className="settings-group">
          <h3>
            {group.heading}
            <Info>{group.note}</Info>
          </h3>
          {group.types.map(({ type, label, detail }) => (
            <label key={type} className="check">
              <input
                type="checkbox"
                checked={prefs?.alerts[type] ?? true}
                disabled={!prefs || !prefs.sound}
                onChange={(event) => void apply(bridge.prefs.setAlert(type, event.target.checked))}
              />
              <span>{label}</span>
              <Info>{detail}</Info>
            </label>
          ))}
        </section>
      ))}

      {MISSING.length > 0 && <p className="modal-note">Not listed: {MISSING.join(', ')}</p>}

      <div className="modal-actions">
        <button className="ghost" onClick={() => void apply(bridge.prefs.reset())}>
          Reset to defaults
        </button>
      </div>
            </>
          )}
        </div>
      </div>
    </Modal>
  )
}

/**
 * Everything on this pane belongs to a daemon rather than to this window, and
 * every daemon has its own answer. One host at a time, picked at the top:
 * stacking all of them meant scrolling past three copies of the same four
 * toggles to reach the one machine you meant.
 */
function SessionsPane(): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const [hostId, setHostId] = useState<string | null>(null)
  const selected = hosts.find((host) => host.id === hostId) ?? hosts[0]

  if (!selected) return <p className="modal-note">No host paired.</p>

  return (
    <>
      {hosts.length > 1 && (
        <div className="settings-hostpick">
          <span className="theme-picker-label">Host</span>
          <select value={selected.id} onChange={(event) => setHostId(event.target.value)}>
            {hosts.map((host) => (
              <option key={host.id} value={host.id}>
                {host.name}
              </option>
            ))}
          </select>
        </div>
      )}
      <SessionTitles hostId={selected.id} />
      <MemoryBudget hostId={selected.id} />
    </>
  )
}

/**
 * The explanation behind a setting, on a hovered icon rather than under its
 * label. Spelled out in place, every row cost three lines and the pane read as
 * prose with checkboxes in it; the toggles are what you came to find.
 *
 * Portalled and positioned in script because the panel scrolls, and an absolute
 * bubble anchored inside it is clipped at the edge — which is exactly where a
 * long explanation wants to go.
 */
function Info({ children }: { children: ReactNode }): JSX.Element {
  const anchor = useRef<HTMLSpanElement | null>(null)
  const closing = useRef<number | undefined>(undefined)
  const [at, setAt] = useState<{ top: number; left: number } | null>(null)

  const open = (): void => {
    window.clearTimeout(closing.current)
    const box = anchor.current?.getBoundingClientRect()
    if (!box) return
    setAt({ top: box.bottom + 6, left: Math.min(box.left, window.innerWidth - INFO_WIDTH - 12) })
  }

  // Delayed, so the pointer can cross the gap into the bubble. One explanation
  // holds a link, and a bubble that vanishes on the way to it is a link that
  // cannot be clicked.
  const close = (): void => {
    closing.current = window.setTimeout(() => setAt(null), 120)
  }

  useEffect(() => () => window.clearTimeout(closing.current), [])

  return (
    <span
      className="info"
      ref={anchor}
      tabIndex={0}
      role="button"
      aria-label="What this does"
      onMouseEnter={open}
      onMouseLeave={close}
      onFocus={open}
      onBlur={close}
    >
      i
      {at &&
        createPortal(
          <span
            className="info-bubble"
            style={{ top: at.top, left: at.left, width: INFO_WIDTH }}
            onMouseEnter={open}
            onMouseLeave={close}
          >
            {children}
          </span>,
          document.body,
        )}
    </span>
  )
}

const INFO_WIDTH = 280

/** What the daemon knows about titling, which is not this window's to keep. */
interface TitlePrefs {
  enabled: boolean
  emoji: boolean
  /** Empty means the built-in prompt. Anything else replaces it outright. */
  prompt: string
}

/**
 * Auto-generated session titles.
 *
 * A daemon setting rather than a window one: it is the daemon that names the
 * session, and every client on that host sees the result. So it is read and
 * written per host, and a host that cannot be reached is left out rather than
 * shown a toggle that would fail to save.
 */
/**
 * The slider's travel, as a share of physical memory.
 *
 * It stops short of the whole machine on both ends: a budget under a twentieth
 * cannot hold even one agent, and one at 100% would only start evicting once
 * the machine was already swapping.
 */
const BUDGET_MIN = 0.05
const BUDGET_MAX = 0.9
const BUDGET_STEP = 0.05
const DEFAULT_BUDGET = 0.25

function gigabytes(bytes: number): string {
  return `${(bytes / 1024 ** 3).toFixed(1)} GB`
}

/**
 * How much memory a daemon's warm sessions may hold before idle ones are let
 * go cold.
 *
 * A share of the machine rather than a number of megabytes: the same install
 * runs on a 16 GB laptop and a 64 GB desktop. The resolved size is shown beside
 * each choice, because "a quarter" means nothing until it says 8 GB.
 */
interface BudgetPrefs {
  enabled: boolean
  fraction: number
}

function MemoryBudget({ hostId }: { hostId: string }): JSX.Element {
  const total = useStore((s) => s.stats[hostId]?.memory_total ?? 0)
  const [prefs, setPrefs] = useState<BudgetPrefs | null>(null)

  useEffect(() => {
    setPrefs(null)
    api(hostId)
      .settings()
      .then((values) => {
        const raw = Number(values['memory.budget_fraction'])
        setPrefs({
          enabled: values['memory.evict'] === 'true',
          // Clamped rather than trusted: the setting predates the slider, so a
          // stored value can sit outside its travel and leave the thumb pinned
          // at an end while the label says something else.
          fraction: Number.isFinite(raw) ? Math.min(BUDGET_MAX, Math.max(BUDGET_MIN, raw)) : DEFAULT_BUDGET,
        })
      })
      .catch(() => {
        // Offline, or a daemon too old to know the setting.
      })
  }, [hostId])

  // Dragging the slider moves the label without writing anything: a drag from a
  // quarter to a half passes through every step in between, and each one would
  // otherwise be a request the daemon has to answer.
  const drag = (fraction: number): void => {
    setPrefs((before) => (before ? { ...before, fraction } : before))
  }

  const change = async (next: Partial<BudgetPrefs>): Promise<void> => {
    const before = prefs
    if (!before) return
    const after = { ...before, ...next }
    setPrefs(after)
    try {
      await api(hostId).updateSettings({
        'memory.evict': String(after.enabled),
        'memory.budget_fraction': String(after.fraction),
      })
    } catch (err) {
      setPrefs(before)
      store.fail(err)
    }
  }

  if (!prefs) {
    return (
      <section className="settings-group">
        <h3>Save memory</h3>
        <p className="modal-note">This host is not reachable.</p>
      </section>
    )
  }

  return (
    <section className="settings-group">
      <h3>
        Save memory
        <Info>
          Each running session holds an agent in memory. Turn this on and Helios stops the ones you have not
          opened for a while once they pass the limit below. The conversation is kept and opening a session
          starts it again; only the terminal tab&apos;s scrollback is lost.
        </Info>
      </h3>

      <label className="check">
        <input
          type="checkbox"
          checked={prefs.enabled}
          onChange={(event) => void change({ enabled: event.target.checked })}
        />
        <span>Save memory</span>
        <Info>
          Stops the agents you have not opened lately. Opening one starts it again, with the conversation
          intact.
        </Info>
      </label>

      <div className={prefs.enabled ? 'budget-slider' : 'budget-slider disabled'}>
        <div className="budget-readout">
          <span>
            Memory limit
            <Info>
              How much the warm sessions on this host may hold between them. Past it, the ones nobody has
              opened for a while are stopped, largest and longest unread first.
            </Info>
          </span>
          <strong>{total > 0 ? gigabytes(total * prefs.fraction) : `${Math.round(prefs.fraction * 100)}%`}</strong>
        </div>
        <input
          type="range"
          min={BUDGET_MIN}
          max={BUDGET_MAX}
          step={BUDGET_STEP}
          value={prefs.fraction}
          disabled={!prefs.enabled}
          onChange={(event) => drag(Number(event.target.value))}
          onPointerUp={(event) => void change({ fraction: Number(event.currentTarget.value) })}
          onKeyUp={(event) => void change({ fraction: Number(event.currentTarget.value) })}
        />
        <div className="budget-scale">
          <span>{total > 0 ? gigabytes(total * BUDGET_MIN) : `${BUDGET_MIN * 100}%`}</span>
          <span>{total > 0 ? `${gigabytes(total)} installed` : ''}</span>
          <span>{total > 0 ? gigabytes(total * BUDGET_MAX) : `${BUDGET_MAX * 100}%`}</span>
        </div>
      </div>
    </section>
  )
}

function SessionTitles({ hostId }: { hostId: string }): JSX.Element {
  const [prefs, setPrefs] = useState<TitlePrefs | null>(null)

  useEffect(() => {
    setPrefs(null)
    void api(hostId)
      .settings()
      .then((body) => {
        const values = (body as { settings?: Record<string, string> }).settings ?? {}
        setPrefs({
          enabled: values['autotitle.enabled'] === 'true',
          // Only an explicit false turns the icon off, which is how the daemon
          // reads it (claude/autotitle.go). Off unless turned on: without a Nerd
          // Font every category renders as the same missing-character box.
          emoji: values['autotitle.emoji'] === 'true',
          prompt: values['autotitle.prompt'] ?? '',
        })
      })
      .catch(() => {
        // Offline. Nothing to show for it, and nowhere to save to.
      })
  }, [hostId])

  const change = async (next: Partial<TitlePrefs>): Promise<void> => {
    const before = prefs
    if (!before) return
    const after = { ...before, ...next }
    setPrefs(after)
    try {
      await api(hostId).updateSettings({
        'autotitle.enabled': String(after.enabled),
        'autotitle.emoji': String(after.emoji),
        'autotitle.prompt': after.prompt,
      })
    } catch (err) {
      setPrefs(before)
      store.fail(err)
    }
  }

  if (!prefs) {
    return (
      <section className="settings-group">
        <h3>Session titles</h3>
        <p className="modal-note">This host is not reachable.</p>
      </section>
    )
  }

  return (
    <section className="settings-group">
      <h3>
        Session titles
        <Info>
          The daemon names a session from its first exchange, using Haiku. It leaves greetings and test
          messages alone, and never renames a session you have titled yourself.
        </Info>
      </h3>

      <label className="check">
        <input
          type="checkbox"
          checked={prefs.enabled}
          onChange={(event) => void change({ enabled: event.target.checked })}
        />
        <span>Generate titles automatically</span>
        <Info>Off by default. Costs a Haiku call per session, about a tenth of a cent.</Info>
      </label>

      <label className="check">
        <input
          type="checkbox"
          checked={prefs.emoji}
          disabled={!prefs.enabled}
          onChange={(event) => void change({ emoji: event.target.checked })}
        />
        <span>Icon prefix</span>
        <Info>
          A glyph per category —  [FIX] rather than [FIX]. Off by default: the glyphs come from a patched{' '}
          <a href="https://www.nerdfonts.com" target="_blank" rel="noreferrer noopener">
            Nerd Font
          </a>
          , and without one installed every category shows the same empty box. The phone has no Nerd Font, so
          titles made here appear boxed there.
        </Info>
      </label>

      <CustomPrompt
        value={prefs.prompt}
        disabled={!prefs.enabled}
        onSave={(prompt) => void change({ prompt })}
      />
    </section>
  )
}

/**
 * The system prompt the titler runs with.
 *
 * Saved on blur rather than per keystroke: this is a round trip to the daemon,
 * and a prompt is written a sentence at a time. Empty restores the built-in.
 */
function CustomPrompt({
  value,
  disabled,
  onSave,
}: {
  value: string
  disabled: boolean
  onSave: (prompt: string) => void
}): JSX.Element {
  const [draft, setDraft] = useState(value)

  // The saved value wins when it changes underneath — another client editing
  // the same daemon, or the host list reloading.
  useEffect(() => setDraft(value), [value])

  return (
    <div className="title-prompt">
      <span className="theme-picker-label">
        Custom prompt
        <Info>
          Replaces the built-in instructions entirely, so the format, the categories and the rule that skips
          greetings all become yours to state. Leave it empty to use the built-in one.
        </Info>
      </span>
      <textarea
        rows={5}
        disabled={disabled}
        value={draft}
        placeholder="Using the built-in prompt."
        spellCheck={false}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => {
          if (draft !== value) onSave(draft)
        }}
      />
    </div>
  )
}

/**
 * A theme list with a swatch each, because a name is not much to choose from —
 * "Gruvbox Dark Medium" and "Gruvbox Dark Hard" are the same words and quite
 * different rooms to sit in.
 */
function ThemePicker({
  label,
  themes,
  value,
  match,
  onPick,
}: {
  label: string
  themes: ThemeSummary[]
  value: string | undefined
  /** Present on the terminal picker, whose first option is to follow the UI. */
  match?: string
  onPick: (id: string) => void
}): JSX.Element {
  return (
    <div className="theme-picker">
      <span className="theme-picker-label">{label}</span>
      <div className="theme-grid">
        {match && (
          <button
            className={value === 'match' ? 'theme-chip selected' : 'theme-chip'}
            onClick={() => onPick('match')}
          >
            <span className="theme-swatch inherit" />
            {match}
          </button>
        )}
        {themes.map((theme) => (
          <button
            key={theme.id}
            className={value === theme.id ? 'theme-chip selected' : 'theme-chip'}
            onClick={() => onPick(theme.id)}
            title={theme.id}
          >
            <span className="theme-swatch">
              {theme.swatch.map((colour, index) => (
                <i key={index} style={{ background: colour }} />
              ))}
            </span>
            {theme.name}
          </button>
        ))}
      </div>
    </div>
  )
}

/**
 * The size rendered markdown is read at, in px — the transcript and the file
 * preview both. Committed on blur or Enter rather than on every keystroke: a
 * half-typed "1" would otherwise repaint the app at the smallest size allowed.
 */
const BACKDROP_LABELS: Record<BackdropStyle, string> = {
  desktop: 'Desktop',
  mesh: 'Mesh',
  corner: 'Corner',
  wash: 'Wash',
  aurora: 'Aurora',
  image: 'Image',
}

/**
 * What sits behind the glass, for whichever theme is showing.
 *
 * Saved into the theme file rather than into this window's preferences, so the
 * choice travels with the theme it was made against — the gradients are drawn
 * from that theme's own palette.
 */
function Backdrop(): JSX.Element | null {
  const [state, setState] = useState<BackdropState | null>(null)
  const [theme, setActive] = useState<HeliosTheme>(() => bridge.theme.boot().theme)
  /** Held while a slider is under the pointer; saving each step would rewrite
      the theme file on every frame of a drag. */
  const [dragging, setDragging] = useState<{ intensity?: number; blur?: number }>({})
  /** The same, for the colour wells: an OS colour panel streams changes as the
      user moves around it, and only the one they settle on is worth saving. */
  const [picking, setPicking] = useState<string[] | null>(null)
  const settle = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => clearTimeout(settle.current ?? undefined), [])

  // The active theme changes with the pickers above and with the OS switching
  // between light and dark, and each is a different file to edit.
  useEffect(() => {
    void bridge.theme.backdrop().then(setState)
    return bridge.theme.onChanged((payload) => {
      setActive(payload.theme)
      void bridge.theme.backdrop().then(setState)
    })
  }, [])

  /**
   * Writes the whole block, not the field that changed.
   *
   * Saving replaces `helios.backdrop` in the theme file outright, so a partial
   * spec is how the blur disappears the next time someone moves the intensity.
   * Dropping `stops` is therefore also how the reset works.
   */
  const save = async (patch: Partial<BackdropSpec> & { stops?: BackdropSpec['stops'] }): Promise<void> => {
    if (!state) return
    const spec: BackdropSpec = {
      style: state.style,
      intensity: state.intensity,
      blur: state.blur,
      // Carried like everything else in the block. Left out, moving a slider
      // writes an image style naming no image, which resolves to the mesh
      // gradient: the picture is replaced for the crime of being blurred.
      ...(state.image ? { image: state.image } : {}),
      ...(state.custom ? { stops: state.palette.map((color) => ({ color })) } : {}),
      ...patch,
    }
    setState({ ...state, ...patch, stops: undefined } as BackdropState)
    try {
      setState(await bridge.theme.setBackdrop(spec))
      setPicking(null)
    } catch (err) {
      store.fail(err)
      setState(await bridge.theme.backdrop())
    }
  }

  const commit = (field: 'intensity' | 'blur'): void => {
    const next = dragging[field]
    if (next === undefined || !state) return
    setDragging((held) => ({ ...held, [field]: undefined }))
    if (next !== state[field]) void save({ [field]: next })
  }

  /**
   * A colour edit, held back until the user stops moving.
   *
   * The wells follow every change so the swatches stay live, but each save
   * rewrites the theme file and repaints every window, which is not something
   * to do on each step through a colour wheel.
   */
  const pick = (index: number, colour: string): void => {
    if (!state) return
    const next = [...(picking ?? state.palette)]
    next[index] = colour
    setPicking(next)
    clearTimeout(settle.current ?? undefined)
    settle.current = setTimeout(() => void save({ stops: next.map((color) => ({ color })) }), 400)
  }

  const chooseImage = async (): Promise<void> => {
    try {
      setState(await bridge.theme.pickBackdropImage())
    } catch (err) {
      store.fail(err)
    }
  }

  // An opaque theme has nothing to show a backdrop through, and a picker that
  // changes nothing visible is worse than no picker.
  if (!state?.glass) return null

  const styles: BackdropStyle[] = [
    ...(state.desktopSupported ? (['desktop'] as BackdropStyle[]) : []),
    ...BACKDROP_STYLES,
    'image',
  ]
  const intensity = dragging.intensity ?? state.intensity
  const blur = dragging.blur ?? state.blur
  const palette = picking ?? state.palette

  return (
    <div className="theme-picker">
      <span className="theme-picker-label">Backdrop</span>
      <div className="theme-grid">
        {styles.map((style) => (
          <button
            key={style}
            className={state.style === style ? 'theme-chip selected' : 'theme-chip'}
            // Choosing the image style means choosing an image: a chip that
            // selected an empty one and left the user to find a second control
            // would be a chip that appears to do nothing.
            onClick={() => (style === 'image' ? void chooseImage() : void save({ style }))}
            title={CHIP_HINTS[style]}
          >
            <span
              className={style === 'desktop' ? 'backdrop-swatch desktop' : 'backdrop-swatch'}
              style={
                style === 'desktop' ? undefined : { background: swatchOf(theme, style, intensity, palette, state.image) }
              }
            />
            {BACKDROP_LABELS[style]}
          </button>
        ))}
      </div>
      {state.style !== 'desktop' && (
        <Slider
          label={state.style === 'image' ? 'Dim' : 'Colour'}
          min={MIN_INTENSITY}
          max={MAX_INTENSITY}
          step={0.05}
          value={intensity}
          onDrag={(next) => setDragging((held) => ({ ...held, intensity: next }))}
          onSettle={() => commit('intensity')}
        />
      )}
      {/* Offered for the desktop too: the frosting is a property of the glass,
          and a window showing a wallpaper wants it at least as much as one
          showing a gradient. */}
      <Slider
        label="Blur"
        min={0}
        max={MAX_BLUR}
        step={2}
        value={blur}
        onDrag={(next) => setDragging((held) => ({ ...held, blur: next }))}
        onSettle={() => commit('blur')}
      />
      {state.style !== 'desktop' && state.style !== 'image' && (
        <div className="backdrop-colours">
          {palette.map((colour, index) => (
            <input
              key={index}
              type="color"
              value={colour}
              // Four wells whichever style is showing: Wash draws two of them
              // and Mesh all four, and switching between the two should not
              // throw away a colour that was chosen.
              title={`Backdrop colour ${index + 1}`}
              onChange={(event) => pick(index, event.target.value)}
            />
          ))}
          {state.custom && (
            <button
              className="ghost"
              onClick={() => void save({ style: state.style, intensity: state.intensity })}
            >
              Use theme colours
            </button>
          )}
        </div>
      )}
      <span className="modal-note">Saved in {state.themeName}.</span>
    </div>
  )
}

const CHIP_HINTS: Record<BackdropStyle, string> = {
  desktop: 'Show the desktop through the window',
  mesh: 'Paint a mesh of four soft blobs',
  corner: 'Paint two opposing corner glows',
  wash: 'Paint a single wash from the top',
  aurora: 'Paint diagonal bands',
  image: 'Choose a picture to sit behind the glass',
}

/** A labelled range that reports while it moves and again when it is let go. */
function Slider({
  label,
  min,
  max,
  step,
  value,
  onDrag,
  onSettle,
}: {
  label: string
  min: number
  max: number
  step: number
  value: number
  onDrag: (value: number) => void
  onSettle: () => void
}): JSX.Element {
  return (
    <label className="backdrop-slider">
      <span>{label}</span>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(event) => onDrag(Number(event.target.value))}
        // The swatches follow the thumb; the window and the file wait for it to
        // be let go.
        onPointerUp={onSettle}
        onKeyUp={onSettle}
        onBlur={onSettle}
      />
    </label>
  )
}

/**
 * The same value the theme would resolve to, drawn small.
 *
 * Built by the generator the window itself uses, from the palette the resolver
 * derived, so a swatch cannot disagree with the result of clicking it.
 */
function swatchOf(
  theme: HeliosTheme,
  style: BackdropStyle,
  intensity: number,
  palette: string[],
  image: string | null,
): string {
  if (style !== 'image') return previewOf(theme, style, intensity, palette)
  // Nothing imported yet: the chip is an invitation rather than a preview.
  if (!image) return `repeating-linear-gradient(45deg, ${theme.vars['--surface-high']} 0 5px, ${theme.vars['--surface-highest']} 5px 10px)`
  return previewOf(theme, style, intensity, palette, image)
}

function previewOf(
  theme: HeliosTheme,
  style: BackdropStyle,
  intensity: number,
  palette: string[],
  image?: string,
): string {
  const stops = palette.map(parseColor).filter((c): c is Rgb => c !== null)
  const base = parseColor(theme.vars['--surface'] ?? '') ?? (parseColor('#101014') as Rgb)
  return backdropValue({ style, intensity, ...(image ? { image } : {}) }, base, stops)
}

function ProseSize({ size, onPick }: { size: number | undefined; onPick: (size: number) => void }): JSX.Element {
  const [draft, setDraft] = useState('')
  const shown = draft || String(size ?? '')

  const commit = (): void => {
    setDraft('')
    const next = Number(shown)
    if (Number.isFinite(next) && next !== size) onPick(Math.min(Math.max(Math.round(next), 10), 28))
  }

  return (
    <div className="theme-picker">
      <span className="theme-picker-label">Text size</span>
      <input
        className="prose-size"
        type="number"
        min={10}
        max={28}
        step={1}
        value={shown}
        disabled={size === undefined}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === 'Enter') event.currentTarget.blur()
        }}
      />
      <span className="modal-note">px — chat and file previews.</span>
    </div>
  )
}
