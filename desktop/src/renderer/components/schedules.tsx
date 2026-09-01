// Schedules: a saved prompt with something that decides when it runs.
//
// A schedule is a thing in the sidebar, like a session, and its detail fills
// the main panel. There is no dialog anywhere: the list, the detail, the editor
// and the "what does this link mean" question are all the same two surfaces the
// app already has.
//
// See docs/specs/55-scheduled-runs.md.

import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { keys } from '../keys.ts'
import { providersQuery, scheduleLogQuery, scheduleRunsQuery, schedulesQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import { sessionLabel, type CheckResult, type Schedule } from '../../shared/models.ts'
import { schedulePrompt } from '../schedule-prompt.ts'
import { SelectionMenu, type MenuAction } from './selection-menu.tsx'

/** The drag payload: one schedule's id, carried on the event. */
export const SCHEDULE_DRAG = 'application/x-helios-schedule'

// ─── The sidebar list ───────────────────────────────────────────────────────

/**
 * One host's schedules, as a tree.
 *
 * A job dragged onto another runs after it, so the list is the tree and the
 * drag is the edit. The rule that link carries is asked in the main panel, not
 * over the top of the list.
 */
export function ScheduleList({
  hostId,
  query = '',
  quiet = false,
}: {
  hostId: string
  query?: string
  /** A host with nothing on it says nothing, when another host has something. */
  quiet?: boolean
}): JSX.Element | null {
  const client = useQueryClient()
  const { data: all = [], error } = useQuery(schedulesQuery(hostId))
  const selected = useStore((s) => s.scheduleSelection)
  const [over, setOver] = useState('')
  const [menu, setMenu] = useState<{ schedule: Schedule; x: number; y: number } | null>(null)

  const invalidate = (): void => {
    void client.invalidateQueries({ queryKey: keys.schedules(hostId) })
  }

  const actionsFor = (sc: Schedule): MenuAction[] => [
    { label: 'Open', run: () => store.selectSchedule(hostId, sc.id) },
    {
      label: 'Run now',
      title: 'Fire it out of turn, without moving when it fires next',
      run: () => void api(hostId).runSchedule(sc.id).then(invalidate),
    },
    {
      label: sc.enabled ? 'Pause' : 'Resume',
      run: () => void api(hostId).updateSchedule(sc.id, { enabled: !sc.enabled }).then(invalidate),
    },
    { label: 'Edit', run: () => store.editSchedule(hostId, sc.id) },
    {
      label: 'Delete',
      danger: true,
      run: () => {
        if (!confirmDelete(all, sc.id)) return
        void api(hostId)
          .deleteSchedule(sc.id)
          .then(() => {
            invalidate()
            if (selected?.scheduleId === sc.id) store.clearScheduleSelection()
          })
      },
    },
  ]

  // Searched over what a person would remember about one: its name, what it
  // does, and the check behind it.
  const schedules = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (needle === '') return all
    return all.filter((sc) =>
      [sc.name, sc.prompt, sc.cron, sc.check_cmd, sc.check_file, sc.cwd]
        .join(' ')
        .toLowerCase()
        .includes(needle),
    )
  }, [all, query])

  const depths = useMemo(() => depthOf(schedules), [schedules])

  if (error) {
    return (
      <div className="sched-empty">
        Schedules need a newer daemon on this host.
        <span className="dim"> {(error as Error).message}</span>
      </div>
    )
  }

  if (schedules.length === 0) {
    if (quiet) return null
    return (
      <div className="sched-empty">
        {query.trim() === ''
          ? 'Nothing scheduled yet — a saved prompt with a clock, or a check that decides when there is something to do.'
          : 'Nothing matches that.'}
      </div>
    )
  }

  return (
    <div className="sched-rows">
      {schedules.map((sc) => (
        <div
          key={sc.id}
          className={[
            'sched-row',
            selected?.scheduleId === sc.id ? 'active' : '',
            over === sc.id ? 'drop' : '',
            sc.enabled ? '' : 'paused',
          ]
            .filter(Boolean)
            .join(' ')}
          style={{ paddingLeft: 10 + (depths[sc.id] ?? 0) * 14 }}
          draggable
          onDragStart={(event) => {
            // The id rides on the event rather than in state: state is a render
            // behind, and a fast drop would read the render before the drag.
            event.dataTransfer.setData(SCHEDULE_DRAG, sc.id)
            event.dataTransfer.effectAllowed = 'move'
          }}
          onDragOver={(event) => {
            if (!event.dataTransfer.types.includes(SCHEDULE_DRAG)) return
            event.preventDefault()
            setOver(sc.id)
          }}
          onDragLeave={() => setOver((id) => (id === sc.id ? '' : id))}
          onDrop={(event) => {
            const moved = event.dataTransfer.getData(SCHEDULE_DRAG)
            setOver('')
            if (!moved || moved === sc.id) return
            event.preventDefault()
            store.linkSchedule(hostId, moved, sc.id)
          }}
          onClick={() => store.selectSchedule(hostId, sc.id)}
          onContextMenu={(event) => {
            event.preventDefault()
            setMenu({ schedule: sc, x: event.clientX, y: event.clientY })
          }}
        >
          <span className="sched-row-top">
            <span className="sched-kind" title={kindTitle(sc)}>
              {kindGlyph(sc)}
            </span>
            <span className="sched-row-name">{sc.name}</span>
            <span className={`sched-row-state ${statusClass(sc)}`}>{stateWord(sc)}</span>
          </span>
          <span className="sched-row-sub">{subtitle(sc)}</span>
          {over === sc.id && <span className="sched-drop-hint">run it after {sc.name}</span>}
        </div>
      ))}
      {menu && (
        <SelectionMenu
          x={menu.x}
          y={menu.y}
          actions={actionsFor(menu.schedule)}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  )
}

/** A schedule and everything chained under it, parents first. */
function branchOf(schedules: Schedule[], id: string): Schedule[] {
  const byId = new Map(schedules.map((sc) => [sc.id, sc]))
  const branch: Schedule[] = []
  const seen = new Set<string>()
  const root = byId.get(id)
  if (!root) return branch
  branch.push(root)
  seen.add(id)
  // Breadth-first with a seen set: after_id is editable, and a cycle in it must
  // not become a hang.
  for (let at = 0; at < branch.length; at++) {
    for (const sc of schedules) {
      if (sc.after_id !== branch[at]?.id || seen.has(sc.id)) continue
      seen.add(sc.id)
      branch.push(sc)
    }
  }
  return branch
}

/**
 * Asks before a delete, and names what will go with it.
 *
 * A job that follows another has no clock of its own, so it cannot be left
 * behind: the daemon deletes the branch. Saying which jobs those are is the
 * whole point of asking — "delete this schedule?" is a different question from
 * "delete these three".
 */
function confirmDelete(schedules: Schedule[], id: string): boolean {
  const branch = branchOf(schedules, id)
  const name = branch[0]?.name ?? 'this schedule'
  if (branch.length < 2) {
    return window.confirm(`Delete ${name}? Its runs stay, as ordinary sessions.`)
  }
  const followers = branch.slice(1).map((sc) => sc.name)
  return window.confirm(
    `Delete ${name} and the ${followers.length} chained under it — ${followers.join(', ')}?\n\n` +
      'A job that follows another has no clock of its own, so it cannot be kept without it. ' +
      'Their runs stay, as ordinary sessions.',
  )
}

/** How deep in the after-chain each schedule sits, so a grandchild indents twice. */
function depthOf(schedules: Schedule[]): Record<string, number> {
  const parent: Record<string, string> = {}
  for (const sc of schedules) parent[sc.id] = sc.after_id ?? ''
  const depth: Record<string, number> = {}
  for (const sc of schedules) {
    let n = 0
    for (let at = sc.after_id ?? ''; at && n < 16; at = parent[at] ?? '') n++
    depth[sc.id] = n
  }
  return depth
}

/**
 * One host's section of the list: its name, and whatever it has scheduled.
 *
 * A host with nothing on it renders nothing at all — heading included — unless
 * it is the last one and nobody else had any either, which is the only time an
 * empty list is worth saying out loud.
 */
export function ScheduleHost({
  hostId,
  name,
  status,
  showName,
  query,
  quiet,
}: {
  hostId: string
  name: string
  status: string
  showName: boolean
  query: string
  quiet: boolean
}): JSX.Element | null {
  const { data: schedules = [] } = useQuery(schedulesQuery(hostId))
  const list = <ScheduleList hostId={hostId} query={query} quiet={quiet} />
  if (list === null || (schedules.length === 0 && quiet)) return null

  return (
    <div className="host-group">
      {showName && schedules.length > 0 && (
        <div className="host-head">
          <span className="host-title">
            <span className={`host-dot ${status}`} />
            <span className="host-name">{name}</span>
          </span>
        </div>
      )}
      {list}
    </div>
  )
}

// ─── The main panel ─────────────────────────────────────────────────────────

/** Whatever the sidebar's schedule selection asks for: a detail, or the editor. */
export function SchedulePanel(): JSX.Element {
  const selection = useStore((s) => s.scheduleSelection)
  const hosts = useStore((s) => s.hosts)

  if (!selection) {
    return (
      <div className="panel-empty">
        <p>Pick a schedule, or write a new one.</p>
        {hosts[0] && (
          <button className="btn primary" onClick={() => store.newSchedule(hosts[0]?.id ?? '')}>
            + New schedule
          </button>
        )}
      </div>
    )
  }

  if (selection.choosing) {
    return <NewSchedule hostId={selection.hostId} />
  }
  if (selection.linkTo) {
    return (
      <LinkPanel hostId={selection.hostId} childId={selection.scheduleId} parentId={selection.linkTo} />
    )
  }
  if (selection.editing) {
    return <ScheduleEditor hostId={selection.hostId} scheduleId={selection.scheduleId} />
  }
  return <ScheduleDetail hostId={selection.hostId} scheduleId={selection.scheduleId} />
}

/**
 * Two ways to make one, and the first is the one people want.
 *
 * Describing it opens an ordinary session with a prompt: the agent has the
 * `helios` skill, installed during setup, so it knows the CLI it is about to
 * call. The form is there for the times you want to be exact, and for when
 * there is no agent to ask.
 */
function NewSchedule({ hostId: initialHost }: { hostId: string }): JSX.Element {
  const hosts = useStore((s) => s.hosts)
  const [hostId, setHostId] = useState(initialHost)
  const [description, setDescription] = useState('')
  const [cwd, setCwd] = useState('')
  const [provider, setProvider] = useState('')
  const [error, setError] = useState('')

  // Which machine runs it is part of the schedule, not a detail to infer, and
  // which agent writes it is the same choice the new-session dialog offers.
  const { data: providers = [] } = useQuery(providersQuery(hostId))
  const agent = provider || providers[0]?.id || 'claude'

  const describe = useMutation({
    mutationFn: async () => {
      const started = await api(hostId).createSession({
        prompt: schedulePrompt(description, cwd),
        cwd,
        provider: agent,
      })
      // Straight to the session: what it does next is the answer, and it is
      // an ordinary transcript.
      store.select(hostId, started.session_id)
    },
    onError: (err: Error) => setError(err.message),
  })

  return (
    <div className="sched-detail">
      <header className="sched-detail-head">
        <h2>New schedule</h2>
      </header>

      <div className="sched-detail-body sched-form">
        {hosts.length > 1 && (
          <label className="field">
            <span>On</span>
            <select
              value={hostId}
              onChange={(e) => {
                setHostId(e.target.value)
                setProvider('')
              }}
            >
              {hosts.map((host) => (
                <option key={host.id} value={host.id}>
                  {host.name}
                </option>
              ))}
            </select>
          </label>
        )}

        <label className="field">
          <span>Describe it</span>
          <textarea
            rows={4}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder={
              'every 15 minutes, run the go tests in ~/work/helios and if they fail, ' +
              'start an agent to fix them'
            }
          />
          <em className="dim">
            An agent reads this, works out the schedule, and creates it with the CLI. You see
            what it made before it ever fires.
          </em>
        </label>

        <label className="field">
          <span>In</span>
          <input
            value={cwd}
            onChange={(e) => setCwd(e.target.value)}
            placeholder="optional — a directory for the agent to work in"
          />
        </label>

        <label className="field">
          <span>Agent</span>
          <select value={agent} onChange={(e) => setProvider(e.target.value)}>
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>

        {error && <p className="error">{error}</p>}
      </div>

      <footer className="sched-detail-foot">
        <button className="btn" onClick={() => store.editSchedule(hostId)}>
          Set it up manually
        </button>
        <span className="spacer" />
        <button className="btn" onClick={() => store.clearScheduleSelection()}>
          Cancel
        </button>
        <button
          className="btn primary"
          disabled={description.trim() === '' || describe.isPending}
          onClick={() => describe.mutate()}
        >
          {describe.isPending ? 'Starting…' : 'Ask an agent'}
        </button>
      </footer>
    </div>
  )
}

type DetailTab = 'overview' | 'runs' | 'log'

function ScheduleDetail({ hostId, scheduleId }: { hostId: string; scheduleId: string }): JSX.Element {
  const client = useQueryClient()
  const { data: schedules = [] } = useQuery(schedulesQuery(hostId))
  const [tab, setTab] = useState<DetailTab>('overview')
  const [check, setCheck] = useState<CheckResult | null>(null)
  const schedule = schedules.find((sc) => sc.id === scheduleId)

  const invalidate = (): void => {
    void client.invalidateQueries({ queryKey: keys.schedules(hostId) })
  }
  const toggle = useMutation({
    mutationFn: () => api(hostId).updateSchedule(scheduleId, { enabled: !schedule?.enabled }),
    onSuccess: invalidate,
  })
  const runNow = useMutation({
    mutationFn: () => api(hostId).runSchedule(scheduleId),
    onSuccess: invalidate,
  })
  const testCheck = useMutation({
    mutationFn: () => api(hostId).checkSchedule(scheduleId),
    onSuccess: setCheck,
  })
  const remove = useMutation({
    mutationFn: () => api(hostId).deleteSchedule(scheduleId),
    onSuccess: () => {
      invalidate()
      store.clearScheduleSelection()
    },
  })

  if (!schedule) return <div className="panel-empty">That schedule is gone.</div>

  return (
    <div className="sched-detail">
      <header className="sched-detail-head">
        <div>
          <h2>{schedule.name}</h2>
          <span className="dim">{subtitle(schedule)}</span>
        </div>
        <button
          className={`sched-toggle${schedule.enabled ? ' on' : ''}`}
          title={schedule.enabled ? 'Pause' : 'Resume'}
          onClick={() => toggle.mutate()}
        >
          {schedule.enabled ? '● on' : '○ paused'}
        </button>
      </header>

      {/* The app's own tab strip, the one every session panel uses. */}
      <nav className="panel-tabs focused">
        {(['overview', 'runs', 'log'] as DetailTab[]).map((name) => (
          <button key={name} className={tab === name ? 'active' : ''} onClick={() => setTab(name)}>
            {name}
          </button>
        ))}
      </nav>

      <div className="sched-detail-body">
        {tab === 'overview' && <Overview schedule={schedule} check={check} />}
        {tab === 'runs' && <Runs hostId={hostId} scheduleId={scheduleId} />}
        {tab === 'log' && <LogView hostId={hostId} scheduleId={scheduleId} />}
      </div>

      <footer className="sched-detail-foot">
        <span className="dim">{footNote(schedule)}</span>
        <span className="spacer" />
        <button className="btn" onClick={() => runNow.mutate()}>
          Run now
        </button>
        {schedule.kind === 'monitor' && (
          <button
            className="btn"
            onClick={() => {
              setTab('overview')
              testCheck.mutate()
            }}
          >
            Test check
          </button>
        )}
        <button className="btn" onClick={() => store.editSchedule(hostId, scheduleId)}>
          Edit
        </button>
        <button
          className="btn danger"
          onClick={() => {
            if (confirmDelete(schedules, scheduleId)) remove.mutate()
          }}
        >
          Delete
        </button>
      </footer>
    </div>
  )
}

function Overview({
  schedule,
  check,
}: {
  schedule: Schedule
  check: CheckResult | null
}): JSX.Element {
  const target =
    schedule.mode === 'resume'
      ? `into session ${(schedule.target_session ?? '').slice(0, 8)}`
      : `a new session in ${schedule.cwd || 'the home directory'}`

  return (
    <dl className="sched-facts">
      {schedule.kind === 'monitor' && (
        <>
          <dt>Check</dt>
          <dd>
            <code>{schedule.check_cmd || schedule.check_file}</code>
            <br />
            <span className="dim">
              {schedule.check_match
                ? `fires when the output matches ${schedule.check_match}`
                : 'fires when it exits non-zero'}
            </span>
            {schedule.last_check_at && (
              <>
                <br />
                <span className="dim">
                  last check {ago(schedule.last_check_at)} · exit {schedule.last_check_exit ?? '—'}
                </span>
              </>
            )}
          </dd>
        </>
      )}

      <dt>Runs</dt>
      <dd>
        {target}
        <br />
        <span className="dim">
          {schedule.provider || 'the default agent'}
          {schedule.model ? ` · ${schedule.model}` : ''}
          {schedule.permission_mode ? ` · ${schedule.permission_mode}` : ''}
        </span>
      </dd>

      <dt>Prompt</dt>
      <dd>
        <pre className="sched-prompt">{schedule.prompt}</pre>
      </dd>

      {check && (
        <>
          <dt>Test</dt>
          <dd>
            <pre className="sched-log">
              {check.failed
                ? `the check failed — ${check.error ?? ''}`
                : `exit ${check.exit} — ${
                    check.matched ? 'MATCH, this would fire' : 'quiet, this would not fire'
                  }`}
              {check.output ? `\n---\n${check.output}` : ''}
            </pre>
          </dd>
        </>
      )}
    </dl>
  )
}

/** The runs are sessions, so this is the session list with a filter on it. */
function Runs({ hostId, scheduleId }: { hostId: string; scheduleId: string }): JSX.Element {
  const { data: runs = [] } = useQuery(scheduleRunsQuery(hostId, scheduleId))
  if (runs.length === 0) return <p className="dim">No runs yet.</p>

  return (
    <div className="sched-runs">
      {runs.map((run) => (
        <button
          key={run.session_id}
          className="sched-run"
          // Opened in the sessions list, in its automated section, which this
          // unfolds on the way: the panel that shows a session lives there, and
          // a run is an ordinary session.
          onClick={() => store.openScheduleRun(hostId, run.session_id)}
        >
          <span className={`sched-run-dot ${run.status}`} />
          <span className="sched-run-when">{shortTime(run.created_at)}</span>
          <span className="sched-run-label">{sessionLabel(run)}</span>
          <span className="sched-run-status">{run.status}</span>
        </button>
      ))}
    </div>
  )
}

function LogView({ hostId, scheduleId }: { hostId: string; scheduleId: string }): JSX.Element {
  const { data: lines = [] } = useQuery(scheduleLogQuery(hostId, scheduleId))
  return <pre className="sched-log tall">{lines.length > 0 ? lines.join('\n') : '(nothing yet)'}</pre>
}

/**
 * What a dropped link means.
 *
 * Half the decision is which job follows which; the other half is whether a
 * failed parent still releases the child, and defaulting that silently is how a
 * chain surprises someone at 3am.
 */
function LinkPanel({
  hostId,
  childId,
  parentId,
}: {
  hostId: string
  childId: string
  parentId: string
}): JSX.Element {
  const client = useQueryClient()
  const { data: schedules = [] } = useQuery(schedulesQuery(hostId))
  const [when, setWhen] = useState<'success' | 'any'>('success')
  const [error, setError] = useState('')

  const child = schedules.find((sc) => sc.id === childId)
  const parent = schedules.find((sc) => sc.id === parentId)

  const link = useMutation({
    mutationFn: () =>
      api(hostId).updateSchedule(childId, { kind: 'after', after_id: parentId, after_when: when }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.schedules(hostId) })
      store.selectSchedule(hostId, childId)
    },
    onError: (err: Error) => setError(err.message),
  })

  if (!child || !parent) return <div className="panel-empty">That link is no longer possible.</div>

  return (
    <div className="sched-detail">
      <header className="sched-detail-head">
        <h2>Link</h2>
      </header>
      <div className="sched-detail-body">
        <p>
          Run <strong>{child.name}</strong> after <strong>{parent.name}</strong>…
        </p>
        <label className="radio-row">
          <input type="radio" checked={when === 'success'} onChange={() => setWhen('success')} />
          only if it succeeds
        </label>
        <label className="radio-row">
          <input type="radio" checked={when === 'any'} onChange={() => setWhen('any')} />
          either way
        </label>
        <p className="dim">
          A job with a parent has no clock of its own: the parent finishing is what starts it.
          {child.cron ? ` ${child.name}'s own schedule will be cleared.` : ''}
        </p>
        {error && <p className="error">{error}</p>}
      </div>
      <footer className="sched-detail-foot">
        <span className="spacer" />
        <button className="btn" onClick={() => store.selectSchedule(hostId, childId)}>
          Cancel
        </button>
        <button className="btn primary" onClick={() => link.mutate()}>
          Link
        </button>
      </footer>
    </div>
  )
}

// ─── The editor ─────────────────────────────────────────────────────────────

function ScheduleEditor({
  hostId,
  scheduleId,
}: {
  hostId: string
  scheduleId: string
}): JSX.Element {
  const client = useQueryClient()
  const { data: schedules = [] } = useQuery(schedulesQuery(hostId))
  const existing = schedules.find((sc) => sc.id === scheduleId)

  const [form, setForm] = useState<Partial<Schedule>>(
    () => existing ?? { kind: 'timer', mode: 'new', cron: '0 9 * * 1-5' },
  )
  const [error, setError] = useState('')
  const [check, setCheck] = useState<CheckResult | null>(null)
  const [source, setSource] = useState<'command' | 'file'>(existing?.check_file ? 'file' : 'command')
  const { data: providers = [] } = useQuery(providersQuery(hostId))

  // The list arrives after the first render, so a deep-linked edit fills in
  // once it does. Keyed on the id, never on the object, or every refetch would
  // throw away what is being typed.
  const existingID = existing?.id
  useEffect(() => {
    if (existing) setForm(existing)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [existingID])

  const set = (fields: Partial<Schedule>): void => setForm((f) => ({ ...f, ...fields }))

  const save = useMutation({
    mutationFn: () =>
      scheduleId ? api(hostId).updateSchedule(scheduleId, form) : api(hostId).createSchedule(form),
    onSuccess: (saved) => {
      void client.invalidateQueries({ queryKey: keys.schedules(hostId) })
      store.selectSchedule(hostId, saved.id)
    },
    onError: (err: Error) => setError(err.message),
  })
  const testCheck = useMutation({
    mutationFn: () => api(hostId).checkSchedule(scheduleId),
    onSuccess: setCheck,
    onError: (err: Error) => setError(err.message),
  })

  const monitor = form.kind === 'monitor'

  return (
    <div className="sched-detail">
      <header className="sched-detail-head">
        <h2>{scheduleId ? `Edit ${existing?.name ?? ''}` : 'New schedule'}</h2>
      </header>

      <div className="sched-detail-body sched-form">
        <label className="field">
          <span>Name</span>
          <input
            value={form.name ?? ''}
            onChange={(e) => set({ name: e.target.value })}
            placeholder="build-watch"
          />
        </label>

        <fieldset className="field">
          <span>Fires</span>
          <div className="radio-set">
            {(
              [
                ['timer', 'on a clock'],
                ['once', 'once, at one moment'],
                ['monitor', 'when a check matches'],
                ['after', 'after another job'],
              ] as [Schedule['kind'], string][]
            ).map(([kind, label]) => (
              <label key={kind} className="radio-row">
                <input type="radio" checked={form.kind === kind} onChange={() => set({ kind })} />
                {label}
              </label>
            ))}
          </div>
        </fieldset>

        {(form.kind === 'timer' || monitor) && (
          <label className="field">
            <span>{monitor ? 'Check every' : 'When'}</span>
            <input
              value={form.cron ?? ''}
              onChange={(e) => set({ cron: e.target.value })}
              placeholder="0 9 * * 1-5"
            />
            <em className="dim">
              {humanCron(form.cron) || 'five fields: minute hour day month weekday'}
            </em>
          </label>
        )}

        {form.kind === 'once' && (
          <label className="field">
            <span>At</span>
            <input
              value={form.run_at ?? ''}
              onChange={(e) => set({ run_at: e.target.value })}
              placeholder="2026-03-02T22:00:00Z"
            />
          </label>
        )}

        {form.kind === 'after' && (
          <>
            <label className="field">
              <span>After</span>
              <select
                value={form.after_id ?? ''}
                onChange={(e) => set({ after_id: e.target.value })}
              >
                <option value="">choose a job…</option>
                {schedules
                  .filter((sc) => sc.id !== scheduleId)
                  .map((sc) => (
                    <option key={sc.id} value={sc.id}>
                      {sc.name}
                    </option>
                  ))}
              </select>
            </label>
            <label className="field">
              <span>Runs</span>
              <select
                value={form.after_when ?? 'success'}
                onChange={(e) => set({ after_when: e.target.value as 'success' | 'any' })}
              >
                <option value="success">only if it succeeds</option>
                <option value="any">either way</option>
              </select>
            </label>
          </>
        )}

        {monitor && (
          <>
            <fieldset className="field">
              <span>Check</span>
              <div className="radio-set">
                <label className="radio-row">
                  <input
                    type="radio"
                    checked={source === 'command'}
                    onChange={() => {
                      setSource('command')
                      set({ check_file: '' })
                    }}
                  />
                  a command
                </label>
                <label className="radio-row">
                  <input
                    type="radio"
                    checked={source === 'file'}
                    onChange={() => {
                      setSource('file')
                      set({ check_cmd: '' })
                    }}
                  />
                  a script on this machine
                </label>
              </div>
            </fieldset>

            <label className="field">
              <span />
              {source === 'command' ? (
                <input
                  value={form.check_cmd ?? ''}
                  onChange={(e) => set({ check_cmd: e.target.value })}
                  placeholder="make test 2>&1"
                />
              ) : (
                <input
                  value={form.check_file ?? ''}
                  onChange={(e) => set({ check_file: e.target.value })}
                  placeholder="~/checks/queue_depth.py"
                />
              )}
            </label>

            <label className="field">
              <span>Match</span>
              <input
                value={form.check_match ?? ''}
                onChange={(e) => set({ check_match: e.target.value })}
                placeholder="optional — a pattern in the output"
              />
              <em className="dim">
                {form.check_match
                  ? 'Fires when the output matches, whatever the exit code.'
                  : 'Fires when the check exits non-zero.'}
              </em>
            </label>
          </>
        )}

        <label className="field">
          <span>Where</span>
          <input
            value={form.cwd ?? ''}
            onChange={(e) => set({ cwd: e.target.value })}
            placeholder="optional — leave empty for work that is not about a directory"
          />
        </label>

        <label className="field">
          <span>Agent</span>
          <select value={form.provider ?? ''} onChange={(e) => set({ provider: e.target.value })}>
            {/* Empty is a real choice: it means whatever the daemon defaults
                to, which is what a schedule written before a second provider
                was installed should keep meaning. */}
            <option value="">the default</option>
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>

        <label className="field">
          <span>Prompt</span>
          <textarea
            rows={8}
            value={form.prompt ?? ''}
            onChange={(e) => set({ prompt: e.target.value })}
            placeholder={
              monitor ? 'The check found something:\n\n{{output}}\n\nLook into it.' : 'What to do'
            }
          />
          {monitor && (
            <em className="dim">{'{{output}}'} is replaced with what the check printed.</em>
          )}
        </label>

        {check && (
          <pre className="sched-log">
            {check.failed
              ? `the check failed — ${check.error ?? ''}`
              : `exit ${check.exit} — ${
                  check.matched ? 'MATCH, this would fire' : 'quiet, this would not fire'
                }`}
            {check.output ? `\n---\n${check.output}` : ''}
          </pre>
        )}
        {error && <p className="error">{error}</p>}
      </div>

      <footer className="sched-detail-foot">
        {scheduleId && monitor && (
          <button className="btn" onClick={() => testCheck.mutate()}>
            Test check
          </button>
        )}
        <span className="spacer" />
        <button
          className="btn"
          onClick={() =>
            scheduleId ? store.selectSchedule(hostId, scheduleId) : store.clearScheduleSelection()
          }
        >
          Cancel
        </button>
        <button className="btn primary" onClick={() => save.mutate()}>
          Save
        </button>
      </footer>
    </div>
  )
}

// ─── Words ──────────────────────────────────────────────────────────────────

function kindGlyph(sc: Schedule): string {
  switch (sc.kind) {
    case 'monitor':
      return '◉'
    case 'once':
      return '⧗'
    case 'after':
      return '⇢'
    default:
      return '⏰'
  }
}

function kindTitle(sc: Schedule): string {
  switch (sc.kind) {
    case 'monitor':
      return 'Watches: fires when its check matches'
    case 'once':
      return 'Runs once'
    case 'after':
      return 'Runs after another job'
    default:
      return 'Runs on a clock'
  }
}

/** The one line under the name: what it does, in words rather than in cron. */
function subtitle(sc: Schedule): string {
  const where =
    sc.mode === 'resume' ? `into ${(sc.target_session ?? '').slice(0, 8)}` : sc.cwd || 'home'
  switch (sc.kind) {
    case 'monitor':
      return `${humanCron(sc.cron)} · ${sc.check_cmd || sc.check_file || ''}`
    case 'once':
      return `once · ${where}`
    case 'after':
      return `${sc.after_when === 'any' ? 'either way' : 'on success'} · ${where}`
    default:
      return `${humanCron(sc.cron)} · ${where}`
  }
}

/** The right-hand word on a row: where this schedule stands right now. */
function stateWord(sc: Schedule): string {
  if (sc.last_status === 'running') return 'running'
  if (sc.last_status === 'missed') return 'missed'
  if (sc.last_status === 'blocked') return 'blocked'
  if (sc.done_at) return 'done'
  if (!sc.enabled) return 'paused'
  if (sc.kind === 'after') return 'waiting'
  if (sc.last_status === 'failed') return sc.fail_streak > 1 ? `failed ×${sc.fail_streak}` : 'failed'
  return untilText(sc.next_run_at)
}

function footNote(sc: Schedule): string {
  const parts: string[] = []
  if (sc.next_run_at && sc.enabled && !sc.done_at) {
    parts.push(`${sc.kind === 'monitor' ? 'next check' : 'next run'} ${untilText(sc.next_run_at)}`)
  }
  if (sc.last_fired_at) parts.push(`last ran ${ago(sc.last_fired_at)}`)
  if (sc.fires_today > 0) parts.push(`fired ${sc.fires_today}× today`)
  if (sc.last_status === 'failed' && sc.fail_streak > 1) {
    parts.push(`failing since ${ago(sc.failing_since)}`)
  }
  if (sc.last_error) parts.push(sc.last_error)
  return parts.join(' · ')
}

function statusClass(sc: Schedule): string {
  switch (sc.last_status) {
    case 'failed':
    case 'missed':
      return 'bad'
    case 'blocked':
      return 'warn'
    case 'running':
      return 'busy'
    default:
      return ''
  }
}

/**
 * A cron expression in words.
 *
 * Nobody reads `0 9 * * 1-5` at a glance, and a list that cannot be skimmed is
 * one where a wrong schedule hides in plain sight. Only the shapes people
 * actually write are spelled out; anything else keeps its expression.
 */
export function humanCron(expr?: string): string {
  if (!expr) return ''
  const [min, hour, dom, month, dow] = expr.trim().split(/\s+/)
  if (!min || !hour || !dow) return expr

  const everyDay = dom === '*' && month === '*' && dow === '*'
  const at = (h: string, m: string): string => `${h.padStart(2, '0')}:${m.padStart(2, '0')}`

  if (/^\*\/\d+$/.test(min) && hour === '*' && everyDay) {
    const n = Number(min.slice(2))
    return n === 1 ? 'every minute' : `every ${n} minutes`
  }
  if (min === '0' && hour === '*' && everyDay) return 'every hour'
  if (/^\d+$/.test(min) && /^\*\/\d+$/.test(hour) && everyDay) {
    const n = Number(hour.slice(2))
    return n === 1 ? 'every hour' : `every ${n} hours`
  }
  if (/^\d+$/.test(min) && /^\d+$/.test(hour)) {
    const time = at(hour, min)
    if (everyDay) return `every day at ${time}`
    if (dom === '*' && month === '*' && dow === '1-5') return `weekdays at ${time}`
    if (dom === '*' && month === '*' && /^\d$/.test(dow)) return `${dayName(Number(dow))}s at ${time}`
  }
  return expr
}

function dayName(n: number): string {
  return ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'][n] ?? ''
}

function untilText(stamp?: string): string {
  if (!stamp) return '—'
  const until = new Date(stamp).getTime() - Date.now()
  if (until < 0) return 'due'
  const minutes = Math.round(until / 60000)
  if (minutes < 1) return `in ${Math.round(until / 1000)}s`
  if (minutes < 60) return `in ${minutes}m`
  if (minutes < 12 * 60) return `in ${Math.floor(minutes / 60)}h ${minutes % 60}m`
  return new Date(stamp).toLocaleString(undefined, {
    weekday: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function ago(stamp?: string): string {
  if (!stamp) return 'never'
  const seconds = Math.max(0, (Date.now() - new Date(stamp).getTime()) / 1000)
  if (seconds < 60) return 'just now'
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h ago`
  return `${Math.round(seconds / 86400)}d ago`
}

function shortTime(stamp: string): string {
  return new Date(stamp).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}
