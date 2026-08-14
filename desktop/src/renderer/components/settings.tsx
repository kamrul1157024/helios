import { useEffect, useState } from 'react'

import { bridge } from '../bridge.ts'
import { store } from '../store.ts'
import { Modal } from './newsession.tsx'
import { ALERT_TYPES } from '../../shared/notifications.ts'
import type { AppearancePrefs, NotificationPrefs, ThemeSummary } from '../../shared/models.ts'

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

export function SettingsDialog({ onClose }: { onClose: () => void }): JSX.Element {
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

        <button className="ghost" onClick={() => void reloadThemes()}>
          Reload themes
        </button>
      </section>

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
    </Modal>
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
