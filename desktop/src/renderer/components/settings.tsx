import { useEffect, useRef, useState } from 'react'

import { api, bridge } from '../bridge.ts'
import { store, useStore } from '../store.ts'
import { Modal } from './newsession.tsx'
import { ALERT_TYPES } from '../../shared/notifications.ts'
import { BACKDROP_STYLES, MAX_INTENSITY, MIN_INTENSITY, backdropValue } from '../../shared/theme/backdrop.ts'
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
type SectionId = 'appearance' | 'titles' | 'notifications'

const SECTIONS: { id: SectionId; label: string }[] = [
  { id: 'appearance', label: 'Appearance' },
  { id: 'titles', label: 'Session titles' },
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
        <h3>Appearance</h3>
        <p className="modal-note">
          Themes are VS Code colour themes. Drop any theme JSON into <code>~/.helios/themes</code> to add your
          own.
        </p>

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

          {section === 'titles' && <SessionTitles />}

          {section === 'notifications' && (
            <>
      <h3>Notifications</h3>
      <p className="modal-note">
        Requests always appear — on screen and on the tray. These toggles decide whether they also make a sound.
      </p>

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
          <h3>{group.heading}</h3>
          <p className="modal-note">{group.note}</p>
          {group.types.map(({ type, label, detail }) => (
            <label key={type} className="check">
              <input
                type="checkbox"
                checked={prefs?.alerts[type] ?? true}
                disabled={!prefs || !prefs.sound}
                onChange={(event) => void apply(bridge.prefs.setAlert(type, event.target.checked))}
              />
              <span>
                {label}
                <small>{detail}</small>
              </span>
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
function SessionTitles(): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const [prefs, setPrefs] = useState<Record<string, TitlePrefs>>({})

  useEffect(() => {
    for (const host of hosts) {
      void api(host.id)
        .settings()
        .then((body) => {
          const values = (body as { settings?: Record<string, string> }).settings ?? {}
          setPrefs((current) => ({
            ...current,
            [host.id]: {
              enabled: values['autotitle.enabled'] === 'true',
              // Only an explicit false turns the icon off, which is how the
              // daemon reads it (claude/autotitle.go).
              // Off unless turned on: without a Nerd Font every category
              // renders as the same missing-character box.
              emoji: values['autotitle.emoji'] === 'true',
              prompt: values['autotitle.prompt'] ?? '',
            },
          }))
        })
        .catch(() => {
          // Offline. Nothing to show for it, and nowhere to save to.
        })
    }
  }, [hosts])

  const change = async (hostId: string, next: Partial<TitlePrefs>): Promise<void> => {
    const before = prefs[hostId]
    if (!before) return
    const after = { ...before, ...next }
    setPrefs((all) => ({ ...all, [hostId]: after }))
    try {
      await api(hostId).updateSettings({
        'autotitle.enabled': String(after.enabled),
        'autotitle.emoji': String(after.emoji),
        'autotitle.prompt': after.prompt,
      })
    } catch (err) {
      setPrefs((all) => ({ ...all, [hostId]: before }))
      store.fail(err)
    }
  }

  const reachable = hosts.filter((host) => prefs[host.id])

  return (
    <section className="settings-group">
      <h3>Session titles</h3>
      <p className="modal-note">
        The daemon names a session from its first exchange, using Haiku. It leaves greetings and test messages
        alone, and never renames a session you have titled yourself.
      </p>

      {reachable.length === 0 ? (
        <p className="modal-note">No host reachable to read this from.</p>
      ) : (
        reachable.map((host) => {
          const value = prefs[host.id] as TitlePrefs
          return (
            <div key={host.id} className="settings-host">
              {/* Named only when there is a choice of host to be confused about. */}
              {reachable.length > 1 && <span className="theme-picker-label">{host.name}</span>}

              <label className="check">
                <input
                  type="checkbox"
                  checked={value.enabled}
                  onChange={(event) => void change(host.id, { enabled: event.target.checked })}
                />
                <span>
                  Generate titles automatically
                  <small>Off by default. Costs a Haiku call per session, about a tenth of a cent.</small>
                </span>
              </label>

              <label className="check">
                <input
                  type="checkbox"
                  checked={value.emoji}
                  disabled={!value.enabled}
                  onChange={(event) => void change(host.id, { emoji: event.target.checked })}
                />
                <span>
                  Icon prefix
                  <small>
                    A glyph per category —  [FIX] rather than [FIX]. Off by default: the glyphs come from a
                    patched <a href="https://www.nerdfonts.com" target="_blank" rel="noreferrer noopener">Nerd
                    Font</a>, and without one installed every category shows the same empty box. The phone has no
                    Nerd Font, so titles made here appear boxed there.
                  </small>
                </span>
              </label>

              <CustomPrompt
                value={value.prompt}
                disabled={!value.enabled}
                onSave={(prompt) => void change(host.id, { prompt })}
              />
            </div>
          )
        })
      )}
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
      <span className="theme-picker-label">Custom prompt</span>
      <p className="modal-note">
        Replaces the built-in instructions entirely, so the format, the categories and the rule that skips
        greetings all become yours to state. Leave it empty to use the built-in one.
      </p>
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
  /** Held while the slider is under the pointer; saving each step would rewrite
      the theme file on every frame of a drag. */
  const [dragging, setDragging] = useState<number | null>(null)
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

  const save = async (spec: BackdropSpec): Promise<void> => {
    if (!state) return
    setState({ ...state, style: spec.style ?? state.style, intensity: spec.intensity ?? state.intensity })
    try {
      setState(await bridge.theme.setBackdrop(spec))
      setPicking(null)
    } catch (err) {
      store.fail(err)
      setState(await bridge.theme.backdrop())
    }
  }

  const commit = (): void => {
    if (dragging === null || !state) return
    const next = dragging
    setDragging(null)
    if (next !== state.intensity) void save({ style: state.style, intensity: next })
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
    settle.current = setTimeout(() => {
      void save({ style: state.style, intensity: state.intensity, stops: next.map((color) => ({ color })) })
    }, 400)
  }

  // An opaque theme has nothing to show a backdrop through, and a picker that
  // changes nothing visible is worse than no picker.
  if (!state?.glass) return null

  const styles: BackdropStyle[] = [
    ...(state.desktopSupported ? (['desktop'] as BackdropStyle[]) : []),
    ...BACKDROP_STYLES,
  ]
  const intensity = dragging ?? state.intensity
  const palette = picking ?? state.palette

  return (
    <div className="theme-picker">
      <span className="theme-picker-label">Backdrop</span>
      <div className="theme-grid">
        {styles.map((style) => (
          <button
            key={style}
            className={state.style === style ? 'theme-chip selected' : 'theme-chip'}
            onClick={() =>
              void save({
                style,
                intensity,
                ...(state.custom ? { stops: palette.map((color) => ({ color })) } : {}),
              })
            }
            title={style === 'desktop' ? 'Show the desktop through the window' : `Paint a ${style} gradient`}
          >
            <span
              className={style === 'desktop' ? 'backdrop-swatch desktop' : 'backdrop-swatch'}
              style={style === 'desktop' ? undefined : { background: previewOf(theme, style, intensity, palette) }}
            />
            {BACKDROP_LABELS[style]}
          </button>
        ))}
      </div>
      {state.style !== 'desktop' && (
        <input
          className="backdrop-intensity"
          type="range"
          min={MIN_INTENSITY}
          max={MAX_INTENSITY}
          step={0.05}
          value={intensity}
          onChange={(event) => setDragging(Number(event.target.value))}
          // The swatches follow the thumb; the window and the file wait for it
          // to be let go.
          onPointerUp={() => commit()}
          onKeyUp={() => commit()}
          onBlur={() => commit()}
        />
      )}
      {state.style !== 'desktop' && (
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

/**
 * The same value the theme would resolve to, drawn small.
 *
 * Built by the generator the window itself uses, from the palette the resolver
 * derived, so a swatch cannot disagree with the result of clicking it.
 */
function previewOf(theme: HeliosTheme, style: BackdropStyle, intensity: number, palette: string[]): string {
  const stops = palette.map(parseColor).filter((c): c is Rgb => c !== null)
  const base = parseColor(theme.vars['--surface'] ?? '') ?? (parseColor('#101014') as Rgb)
  return backdropValue({ style, intensity }, base, stops)
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
