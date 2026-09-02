import { useEffect, useMemo, useRef, useState, type DragEvent, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createPortal } from 'react-dom'

import { api, bridge } from '../bridge.ts'
import { keys, mergeSettings, settingValues, type SettingsDocument } from '../keys.ts'
import { useHostSessions } from '../host-data.ts'
import { settingsQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import { Modal } from './newsession.tsx'
import { ALERT_TYPES } from '../../shared/notifications.ts'
import {
  DEFAULT_STATUS_LINE,
  SEGMENTS,
  hiddenSegments,
  moveSegment,
  toggleSegment,
  type SegmentId,
} from '../../shared/status-line.ts'
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
type SectionId = 'appearance' | 'sessions' | 'terminal' | 'notifications'

const SECTIONS: { id: SectionId; label: string }[] = [
  { id: 'appearance', label: 'Appearance' },
  { id: 'sessions', label: 'Sessions' },
  { id: 'terminal', label: 'Terminal' },
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
          {section === 'appearance' && !appearance && <Loading title="Appearance" />}

          {section === 'appearance' && appearance && (
            <>
      <section className="settings-group">
        <h3>
          Appearance
          <Info>
            Themes are VS Code colour themes. Drop any theme JSON into <code>~/.helios/themes</code> to add
            your own.
          </Info>
        </h3>

        <Row
          label="Mode"
          info={MODES.map((mode) => `${mode.label} — ${mode.detail}`).join(' ')}
        >
          <select
            value={appearance?.mode ?? 'system'}
            disabled={!appearance}
            onChange={(event) => void setTheme({ mode: event.target.value as AppearancePrefs['mode'] })}
          >
            {MODES.map((mode) => (
              <option key={mode.value} value={mode.value}>
                {mode.label}
              </option>
            ))}
          </select>
        </Row>

        {/* Both slots stay listed on a pinned mode: they are what 'System'
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
          info="The colours inside the terminal panes. Match UI theme follows whichever of the two above is showing."
          themes={themes}
          value={appearance?.terminalTheme}
          match="Match UI theme"
          onPick={(id) => void setTheme({ terminalTheme: id })}
        />

        <PixelSize
          label="Text size"
          info="In pixels, between 10 and 28. Sets the size of rendered markdown — the transcript and the file previews both. It does not touch the terminal."
          size={appearance?.proseSize}
          min={10}
          max={28}
          onPick={(proseSize) => void setTheme({ proseSize })}
        />

        <Backdrop />

        <button className="ghost" onClick={() => void reloadThemes()}>
          Reload themes
        </button>
      </section>

      {/* Its own group rather than another row among the themes. The segment
          list is many rows tall, and in the middle of a form of single-line
          controls it broke the theme pickers into two halves that looked
          unrelated. */}
      <section className="settings-group">
        <h3>
          Status line
          <Info>The bar along the foot of a session, in place of a header above it.</Info>
        </h3>

        <StatusLineRows
          order={appearance?.statusLine ?? DEFAULT_STATUS_LINE}
          onChange={(statusLine) => void setTheme({ statusLine })}
        />

        <PixelSize
          label="Text size"
          info="In pixels, between 9 and 16. The bar's height follows the text, so a larger size is a taller bar."
          size={appearance?.statusLineSize}
          min={9}
          max={16}
          onPick={(statusLineSize) => void setTheme({ statusLineSize })}
        />
      </section>
            </>
          )}

          {section === 'sessions' && <SessionsPane />}

          {section === 'terminal' && <TerminalPane />}

          {section === 'notifications' && !prefs && <Loading title="Notifications" />}

          {section === 'notifications' && prefs && (
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
function TerminalPane(): JSX.Element {
  const uploads = useStore((s) => s.terminalUploads)

  return (
    <section className="settings-group">
      <h3>
        Dropped and pasted files
        <Info>
          A file dropped on a terminal is on this machine, and the agent reading it is on the
          daemon&apos;s. Uploading it and typing back the path it landed at is the only reading
          that works when those are different machines.
        </Info>
      </h3>

      <label className="check">
        <input
          type="checkbox"
          checked={uploads}
          onChange={(event) => store.setTerminalUploads(event.target.checked)}
        />
        <span>Upload to the daemon and type the path</span>
        <Info>
          Off leaves the terminal to the CLI, which is what you want when the daemon runs on this
          machine and the CLI can read the clipboard itself.
        </Info>
      </label>
    </section>
  )
}

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
      className="info-dot"
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
            className="info-tip"
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

/**
 * What a section shows before its host has answered.
 *
 * Distinct from the unreachable message on purpose: both used to be the same
 * blank, so a daemon that was merely slow read as one that was down.
 */
function Loading({ title }: { title: string }): JSX.Element {
  return (
    <section className="settings-group">
      <h3>{title}</h3>
      <p className="settings-loading">
        <span className="spinner" />
        Reading from the host…
      </p>
    </section>
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
/**
 * The slider's travel, as a share of physical memory.
 *
 * It stops short of the whole machine on both ends: a budget under a twentieth
 * cannot hold even one agent, and one at 100% would only start evicting once
 * the machine was already swapping.
 */
const BUDGET_MIN = 0.05
const BUDGET_MAX = 0.9
const DEFAULT_BUDGET = 0.25

const GIB = 1024 ** 3

/**
 * The slider moves in half-gigabytes when the host has told us how much memory
 * it has.
 *
 * Stepping the fraction instead gave a limit of 8.3 GB on one machine and 6.4
 * on another, which reads as a measurement rather than a decision. The daemon
 * is still sent a fraction — the same install runs on a 16 GB laptop and a 64
 * GB desktop — but the number the user aims at is a round one.
 */
const STEP_GIB = 0.5
const MIN_GIB = 2

/** Drops a trailing zero, so the scale reads 2, 2.5, 3 rather than 2.0, 2.5. */
function gigabytes(bytes: number): string {
  const gib = bytes / GIB
  return `${Number.isInteger(gib) ? gib : gib.toFixed(1)} GB`
}

/** The travel of the slider on a host of this size, in whole steps. */
function budgetRange(total: number): { min: number; max: number } {
  const max = Math.floor((total * BUDGET_MAX) / GIB / STEP_GIB) * STEP_GIB
  // A machine too small for the usual floor still gets a slider that moves,
  // rather than one whose ends have crossed over.
  return max <= MIN_GIB ? { min: STEP_GIB, max: Math.max(STEP_GIB, max) } : { min: MIN_GIB, max }
}

function snapToStep(gib: number, range: { min: number; max: number }): number {
  const snapped = Math.round(gib / STEP_GIB) * STEP_GIB
  return Math.min(range.max, Math.max(range.min, snapped))
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

/**
 * One daemon's settings, shared by every pane that reads them.
 *
 * The memory budget, the auto-titler and the sidebar's sort mode all live in
 * one document, so they are one query rather than one fetch each. It never goes
 * stale on its own: two of those panes hold a draft while a control is under
 * the pointer, and a refetch landing mid-drag would move the thumb.
 */
function useDaemonSettings(hostId: string) {
  return useQuery(settingsQuery(hostId))
}

/**
 * A write to some of those settings, applied here before the daemon answers.
 *
 * Merged rather than replaced, both ways: the daemon upserts only the keys it
 * is sent, so the optimistic copy has to do the same or one pane's save blanks
 * the other's fields until the next fetch.
 */
function useSettingsWrite(hostId: string) {
  const client = useQueryClient()
  const key = keys.settings(hostId)
  return useMutation({
    mutationFn: (written: Record<string, string>) => api(hostId).updateSettings(written),
    onMutate: async (written) => {
      await client.cancelQueries({ queryKey: key })
      const before = client.getQueryData<SettingsDocument>(key)
      client.setQueryData<SettingsDocument>(key, (doc) => mergeSettings(doc, written))
      return { before }
    },
    onError: (err, _written, context) => {
      client.setQueryData(key, context?.before)
      store.fail(err)
    },
  })
}

function MemoryBudget({ hostId }: { hostId: string }): JSX.Element {
  const { stats } = useHostSessions()
  const total = stats[hostId]?.memory_total ?? 0
  const settings = useDaemonSettings(hostId)

  /**
   * What the daemon holds, as this pane reads it.
   *
   * Clamped rather than trusted: the setting predates the slider, so a stored
   * value can sit outside its travel and leave the thumb pinned at an end while
   * the label says something else.
   */
  const stored = useMemo<BudgetPrefs>(() => {
    const values = settingValues(settings.data)
    const raw = Number(values['memory.budget_fraction'])
    return {
      enabled: values['memory.evict'] === 'true',
      fraction: Number.isFinite(raw) ? Math.min(BUDGET_MAX, Math.max(BUDGET_MIN, raw)) : DEFAULT_BUDGET,
    }
  }, [settings.data])

  // Dragging the slider moves the label without writing anything: a drag from a
  // quarter to a half passes through every step in between, and each one would
  // otherwise be a request the daemon has to answer.
  const [dragged, setDragged] = useState<number | null>(null)
  const prefs: BudgetPrefs = { ...stored, fraction: dragged ?? stored.fraction }

  const write = useSettingsWrite(hostId)
  const change = (next: Partial<BudgetPrefs>): void => {
    const after = { ...prefs, ...next }
    setDragged(null)
    write.mutate({
      'memory.evict': String(after.enabled),
      'memory.budget_fraction': String(after.fraction),
    })
  }

  const range = budgetRange(total)
  // Snapped for display as well as for saving, so a fraction stored by the
  // phone or the TUI — neither of which knows the host's size — still lands the
  // thumb on a step rather than between two.
  const gib = prefs ? snapToStep((total * prefs.fraction) / GIB, range) : 0

  if (settings.isPending) return <Loading title="Memory" />
  if (settings.error) {
    return (
      <section className="settings-group">
        <h3>Memory</h3>
        <p className="modal-note">This host is not reachable.</p>
      </section>
    )
  }

  return (
    <section className="settings-group">
      <h3>
        Memory
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
          onChange={(event) => change({ enabled: event.target.checked })}
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
          <strong>{total > 0 ? gigabytes(gib * GIB) : `${Math.round(prefs.fraction * 100)}%`}</strong>
        </div>
        {/* In gigabytes when the host has reported its size, and in the raw
            fraction when it has not: the daemon takes a fraction either way. */}
        {total > 0 ? (
          <input
            type="range"
            min={range.min}
            max={range.max}
            step={STEP_GIB}
            value={gib}
            disabled={!prefs.enabled}
            onChange={(event) => setDragged((Number(event.target.value) * GIB) / total)}
            onPointerUp={(event) => change({ fraction: (Number(event.currentTarget.value) * GIB) / total })}
            onKeyUp={(event) => change({ fraction: (Number(event.currentTarget.value) * GIB) / total })}
          />
        ) : (
          <input
            type="range"
            min={BUDGET_MIN}
            max={BUDGET_MAX}
            step={0.05}
            value={prefs.fraction}
            disabled={!prefs.enabled}
            onChange={(event) => setDragged(Number(event.target.value))}
            onPointerUp={(event) => change({ fraction: Number(event.currentTarget.value) })}
            onKeyUp={(event) => change({ fraction: Number(event.currentTarget.value) })}
          />
        )}
        <div className="budget-scale">
          <span>{total > 0 ? gigabytes(range.min * GIB) : `${BUDGET_MIN * 100}%`}</span>
          <span>{total > 0 ? `${gigabytes(total)} installed` : ''}</span>
          <span>{total > 0 ? gigabytes(range.max * GIB) : `${BUDGET_MAX * 100}%`}</span>
        </div>
      </div>
    </section>
  )
}

function SessionTitles({ hostId }: { hostId: string }): JSX.Element {
  const settings = useDaemonSettings(hostId)
  const values = settingValues(settings.data)
  const prefs: TitlePrefs = {
    enabled: values['autotitle.enabled'] === 'true',
    // Only an explicit false turns the icon off, which is how the daemon reads
    // it (claude/autotitle.go). Off unless turned on: without a Nerd Font every
    // category renders as the same missing-character box.
    emoji: values['autotitle.emoji'] === 'true',
    prompt: values['autotitle.prompt'] ?? '',
  }

  const write = useSettingsWrite(hostId)
  const change = (next: Partial<TitlePrefs>): void => {
    const after = { ...prefs, ...next }
    write.mutate({
      'autotitle.enabled': String(after.enabled),
      'autotitle.emoji': String(after.emoji),
      'autotitle.prompt': after.prompt,
    })
  }

  if (settings.isPending) return <Loading title="Session titles" />
  if (settings.error) {
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
          onChange={(event) => change({ enabled: event.target.checked })}
        />
        <span>Generate titles automatically</span>
        <Info>Off by default. Costs a Haiku call per session, about a tenth of a cent.</Info>
      </label>

      <label className="check">
        <input
          type="checkbox"
          checked={prefs.emoji}
          disabled={!prefs.enabled}
          onChange={(event) => change({ emoji: event.target.checked })}
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
        onSave={(prompt) => change({ prompt })}
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
 * A label, its control, and the explanation on an icon beside the label.
 *
 * Every setting on the Appearance pane is one choice, and laying them out as a
 * column of grids rather than a stack of blocks is what lets the pane be read
 * down the labels.
 */
function Row({
  label,
  info,
  children,
}: {
  label: string
  info?: ReactNode
  children: ReactNode
}): JSX.Element {
  return (
    <div className="setting-row">
      <span className="setting-row-label">
        {label}
        {info && <Info>{info}</Info>}
      </span>
      <div className="setting-row-control">{children}</div>
    </div>
  )
}

/** Where a dragged segment would land, drawn as a line between two rows. */
const DRAG_TYPE = 'application/x-helios-segment'

/**
 * Which segments the session status line draws, and in what order.
 *
 * Shown as one list rather than two: the enabled ones on top in the order they
 * appear in the bar, then the rest under a divider. A segment turned off drops
 * below it and loses its place, which is the honest reading — the stored value
 * is the enabled order, and there is nowhere to remember where an absent
 * segment used to be.
 */
function StatusLineRows({
  order,
  onChange,
}: {
  order: SegmentId[]
  onChange: (order: SegmentId[]) => void
}): JSX.Element {
  const [over, setOver] = useState<number | null>(null)
  const hidden = hiddenSegments(order)
  const labels = new Map(SEGMENTS.map((segment) => [segment.id, segment.label]))

  const drop = (index: number) => (event: DragEvent) => {
    event.preventDefault()
    const id = event.dataTransfer.getData(DRAG_TYPE) as SegmentId
    setOver(null)
    if (id) onChange(moveSegment(order, id, index))
  }

  // Above the midpoint means before this row, below means after it. The same
  // rule the session list uses when a row is dragged onto another.
  const hover = (index: number) => (event: DragEvent) => {
    if (!event.dataTransfer.types.includes(DRAG_TYPE)) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
    const box = event.currentTarget.getBoundingClientRect()
    setOver(event.clientY < box.top + box.height / 2 ? index : index + 1)
  }

  return (
    <Row
      label="Segments"
      info="Drag to reorder; untick to hide. Turning everything off hides the bar."
    >
      <div className="seg-list" onDragLeave={() => setOver(null)}>
        {order.map((id, index) => (
          <label
            key={id}
            className={`seg-row${over === index ? ' over' : ''}${over === index + 1 && index === order.length - 1 ? ' over-last' : ''}`}
            draggable
            onDragStart={(event) => {
              event.dataTransfer.effectAllowed = 'move'
              event.dataTransfer.setData(DRAG_TYPE, id)
            }}
            onDragEnd={() => setOver(null)}
            onDragOver={hover(index)}
            onDrop={drop(index)}
          >
            <span className="seg-grip" aria-hidden>
              ⠿
            </span>
            <input type="checkbox" checked onChange={() => onChange(toggleSegment(order, id))} />
            <span className="seg-label">{labels.get(id)}</span>
          </label>
        ))}

        {hidden.length > 0 && <span className="seg-divider">Not shown</span>}

        {hidden.map((id) => (
          <label key={id} className="seg-row off">
            <span className="seg-grip" aria-hidden />
            <input type="checkbox" checked={false} onChange={() => onChange(toggleSegment(order, id))} />
            <span className="seg-label">{labels.get(id)}</span>
          </label>
        ))}
      </div>
    </Row>
  )
}

/**
 * One theme, chosen from a list.
 *
 * A grid of chips before, one per installed theme, which was the tallest thing
 * in the dialog by a wide margin and grew with every theme dropped into
 * ~/.helios/themes. The swatch that justified the chips is kept beside the
 * closed dropdown, and picking applies immediately — so arrowing through the
 * list previews each theme on the whole window, which no swatch could.
 */
function ThemePicker({
  label,
  info,
  themes,
  value,
  match,
  onPick,
}: {
  label: string
  info?: ReactNode
  themes: ThemeSummary[]
  value: string | undefined
  /** Present on the terminal picker, whose first option is to follow the UI. */
  match?: string
  onPick: (id: string) => void
}): JSX.Element {
  const current = themes.find((theme) => theme.id === value)
  return (
    <Row label={label} info={info}>
      {value === 'match' || !current ? (
        <span className="theme-swatch inherit" />
      ) : (
        <span className="theme-swatch">
          {current.swatch.map((colour, index) => (
            <i key={index} style={{ background: colour }} />
          ))}
        </span>
      )}
      <select value={value ?? ''} onChange={(event) => onPick(event.target.value)}>
        {/* Nothing is chosen until the preferences land, and a select with no
            matching option would otherwise show the first theme as though it
            were the active one. */}
        {value === undefined && <option value="">Loading…</option>}
        {match && <option value="match">{match}</option>}
        {themes.map((theme) => (
          <option key={theme.id} value={theme.id}>
            {theme.name}
          </option>
        ))}
      </select>
    </Row>
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
    <>
      <Row
        label="Backdrop"
        info={
          <>
            What sits behind the glass. {CHIP_HINTS[state.style]}. Saved in {state.themeName} rather than in
            this window, so the choice travels with the theme whose colours draw it.
          </>
        }
      >
        <span
          className={state.style === 'desktop' ? 'backdrop-swatch desktop' : 'backdrop-swatch'}
          style={
            state.style === 'desktop'
              ? undefined
              : { background: swatchOf(theme, state.style, intensity, palette, state.image) }
          }
        />
        <select
          value={state.style}
          // Choosing the image style means choosing an image: an option that
          // selected an empty one and left the user to find a second control
          // would be an option that appears to do nothing.
          onChange={(event) => {
            const style = event.target.value as BackdropStyle
            if (style === 'image') void chooseImage()
            else void save({ style })
          }}
        >
          {styles.map((style) => (
            <option key={style} value={style}>
              {BACKDROP_LABELS[style]}
            </option>
          ))}
        </select>
        {/* Offered again once an image is showing: the option that opened the
            file dialog is the one already selected, so re-picking it fires no
            change event. */}
        {state.style === 'image' && (
          <button className="ghost" onClick={() => void chooseImage()}>
            Change…
          </button>
        )}
      </Row>
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
        <Row label="Colours" info="Wash draws the first two and Mesh all four, so switching between them keeps whatever was chosen here.">
          <div className="backdrop-colours">
            {palette.map((colour, index) => (
              <input
                key={index}
                type="color"
                value={colour}
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
        </Row>
      )}
    </>
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
  info,
  min,
  max,
  step,
  value,
  onDrag,
  onSettle,
}: {
  label: string
  info?: ReactNode
  min: number
  max: number
  step: number
  value: number
  onDrag: (value: number) => void
  onSettle: () => void
}): JSX.Element {
  return (
    <Row label={label} info={info}>
      <input
        className="wide-range"
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
    </Row>
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

/**
 * A size in pixels, typed rather than dragged.
 *
 * The draft is held while the field is focused and only clamped on the way out,
 * so typing "1" on the way to "12" is not answered by the field correcting
 * itself to the minimum under the cursor.
 */
function PixelSize({
  label,
  info,
  size,
  min,
  max,
  onPick,
}: {
  label: string
  info: string
  size: number | undefined
  min: number
  max: number
  onPick: (size: number) => void
}): JSX.Element {
  const [draft, setDraft] = useState('')
  const shown = draft || String(size ?? '')

  const commit = (): void => {
    setDraft('')
    const next = Number(shown)
    if (Number.isFinite(next) && next !== size) onPick(Math.min(Math.max(Math.round(next), min), max))
  }

  return (
    <Row label={label} info={info}>
      <input
        className="prose-size"
        type="number"
        min={min}
        max={max}
        step={1}
        value={shown}
        disabled={size === undefined}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === 'Enter') event.currentTarget.blur()
        }}
      />
      <span className="setting-row-unit">px</span>
    </Row>
  )
}
