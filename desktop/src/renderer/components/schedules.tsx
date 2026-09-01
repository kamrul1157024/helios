// Schedules: a saved prompt with something that decides when it runs.
//
// A dialog rather than a session tab, because a schedule belongs to a host and
// not to a session. The list is a tree — a job dragged onto another runs after
// it — and it doubles as the progress view while a chain is running, because
// "where has it got to" is the question the list already answers.
//
// See docs/specs/55-scheduled-runs.md.

import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { keys } from '../keys.ts'
import { scheduleLogQuery, scheduleRunsQuery, schedulesQuery } from '../queries.ts'
import { store } from '../store.ts'
import type { CheckResult, Schedule } from '../../shared/models.ts'
import { Modal } from './newsession.tsx'

export function SchedulesDialog({
  hostId,
  onClose,
}: {
  hostId: string
  onClose: () => void
}): JSX.Element {
  const { data: schedules = [], isLoading } = useQuery(schedulesQuery(hostId))
  const [editing, setEditing] = useState<Partial<Schedule> | null>(null)
  const [runsFor, setRunsFor] = useState<Schedule | null>(null)
  const [dragging, setDragging] = useState<Schedule | null>(null)
  const [linking, setLinking] = useState<{ child: Schedule; parent: Schedule } | null>(null)

  const depths = useMemo(() => depthOf(schedules), [schedules])

  if (runsFor) {
    return <RunsView hostId={hostId} schedule={runsFor} onBack={() => setRunsFor(null)} onClose={onClose} />
  }

  if (editing) {
    return (
      <ScheduleEditor
        hostId={hostId}
        draft={editing}
        schedules={schedules}
        onDone={() => setEditing(null)}
        onClose={onClose}
      />
    )
  }

  return (
    <Modal title="Schedules" onClose={onClose}>
      {isLoading && <p className="dim">Loading…</p>}
      {!isLoading && schedules.length === 0 && (
        <p className="dim">
          Nothing scheduled yet. A schedule is a saved prompt with a clock — or a check that
          decides when there is something to do.
        </p>
      )}

      <div className="sched-list">
        {schedules.map((sc) => (
          <ScheduleCard
            key={sc.id}
            hostId={hostId}
            schedule={sc}
            depth={depths[sc.id] ?? 0}
            dragging={dragging}
            onDragStart={() => setDragging(sc)}
            onDragEnd={() => setDragging(null)}
            onDropOn={(parent) => {
              if (dragging && dragging.id !== parent.id) setLinking({ child: dragging, parent })
              setDragging(null)
            }}
            onEdit={() => setEditing(sc)}
            onRuns={() => setRunsFor(sc)}
          />
        ))}
      </div>

      <div className="sched-actions">
        <button className="btn primary" onClick={() => setEditing({ kind: 'timer', mode: 'new' })}>
          + New schedule
        </button>
      </div>

      {linking && (
        <LinkDialog
          hostId={hostId}
          child={linking.child}
          parent={linking.parent}
          onClose={() => setLinking(null)}
        />
      )}
    </Modal>
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

function ScheduleCard({
  hostId,
  schedule,
  depth,
  dragging,
  onDragStart,
  onDragEnd,
  onDropOn,
  onEdit,
  onRuns,
}: {
  hostId: string
  schedule: Schedule
  depth: number
  dragging: Schedule | null
  onDragStart: () => void
  onDragEnd: () => void
  onDropOn: (parent: Schedule) => void
  onEdit: () => void
  onRuns: () => void
}): JSX.Element {
  const client = useQueryClient()
  const [over, setOver] = useState(false)
  const invalidate = (): void => {
    void client.invalidateQueries({ queryKey: keys.schedules(hostId) })
  }

  const toggle = useMutation({
    mutationFn: () => api(hostId).updateSchedule(schedule.id, { enabled: !schedule.enabled }),
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: () => api(hostId).deleteSchedule(schedule.id),
    onSuccess: invalidate,
  })
  const runNow = useMutation({
    mutationFn: () => api(hostId).runSchedule(schedule.id),
    onSuccess: invalidate,
  })

  const droppable = dragging !== null && dragging.id !== schedule.id

  return (
    <div
      className={`sched-card${over ? ' drop' : ''}${schedule.enabled ? '' : ' paused'}`}
      style={{ marginLeft: depth * 24 }}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={(event) => {
        if (!droppable) return
        event.preventDefault()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(event) => {
        if (!droppable) return
        event.preventDefault()
        setOver(false)
        onDropOn(schedule)
      }}
    >
      <div className="sched-head">
        <span className="sched-kind" title={kindTitle(schedule)}>
          {kindGlyph(schedule)}
        </span>
        <span className="sched-name">{schedule.name}</span>
        <span className="sched-next">{nextText(schedule)}</span>
        <button
          className={`sched-toggle${schedule.enabled ? ' on' : ''}`}
          title={schedule.enabled ? 'Pause' : 'Resume'}
          onClick={() => toggle.mutate()}
        >
          {schedule.enabled ? '●' : '○'}
        </button>
      </div>

      <div className="sched-what">{describe(schedule)}</div>
      <div className={`sched-last ${statusClass(schedule)}`}>{lastText(schedule)}</div>

      <div className="sched-row-actions">
        {over && <span className="sched-drop-hint">drop to run it after {schedule.name}</span>}
        <button className="btn tiny" onClick={() => runNow.mutate()}>
          Run now
        </button>
        <button className="btn tiny" onClick={onEdit}>
          Edit…
        </button>
        <button className="btn tiny" onClick={onRuns}>
          Runs
        </button>
        <button
          className="btn tiny danger"
          onClick={() => {
            // Deleting a parent detaches its children rather than taking them
            // with it, which is worth saying before it happens.
            const warning = "Delete this schedule? Anything that followed it will be paused."
            if (window.confirm(warning)) remove.mutate()
          }}
        >
          Delete
        </button>
      </div>
    </div>
  )
}

/**
 * The link dialog: dropping asks what the link means.
 *
 * Guessing it is how a chain surprises someone at 3am, so the rule is chosen
 * here rather than defaulted silently.
 */
function LinkDialog({
  hostId,
  child,
  parent,
  onClose,
}: {
  hostId: string
  child: Schedule
  parent: Schedule
  onClose: () => void
}): JSX.Element {
  const client = useQueryClient()
  const [when, setWhen] = useState<'success' | 'any'>('success')
  const [error, setError] = useState('')

  const link = useMutation({
    mutationFn: () =>
      api(hostId).updateSchedule(child.id, { kind: 'after', after_id: parent.id, after_when: when }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.schedules(hostId) })
      onClose()
    },
    onError: (err: Error) => setError(err.message),
  })

  return (
    <Modal title={`Run ${child.name} after ${parent.name}…`} onClose={onClose}>
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
      </p>
      {error && <p className="error">{error}</p>}
      <div className="sched-actions">
        <button className="btn" onClick={onClose}>
          Cancel
        </button>
        <button className="btn primary" onClick={() => link.mutate()}>
          Link
        </button>
      </div>
    </Modal>
  )
}

/** The runs of one schedule: ordinary sessions, so this is the session list. */
function RunsView({
  hostId,
  schedule,
  onBack,
  onClose,
}: {
  hostId: string
  schedule: Schedule
  onBack: () => void
  onClose: () => void
}): JSX.Element {
  const { data: runs = [] } = useQuery(scheduleRunsQuery(hostId, schedule.id))
  const { data: log = [] } = useQuery(scheduleLogQuery(hostId, schedule.id))

  return (
    <Modal title={`Runs · ${schedule.name}`} onClose={onClose}>
      <button className="btn tiny" onClick={onBack}>
        ← All schedules
      </button>

      <div className="sched-runs">
        {runs.length === 0 && <p className="dim">No runs yet.</p>}
        {runs.map((run) => (
          <button
            key={run.session_id}
            className="sched-run"
            onClick={() => {
              store.select(hostId, run.session_id)
              onClose()
            }}
          >
            <span className={`sched-run-dot ${run.status}`} />
            <span className="sched-run-when">{shortTime(run.created_at)}</span>
            <span className="sched-run-label">{run.title ?? run.last_user_message ?? '—'}</span>
            <span className="sched-run-status">{run.status}</span>
          </button>
        ))}
      </div>

      <h3>Log</h3>
      <pre className="sched-log">{log.length > 0 ? log.join('\n') : '(nothing yet)'}</pre>
    </Modal>
  )
}

// ─── The editor ─────────────────────────────────────────────────────────────

function ScheduleEditor({
  hostId,
  draft,
  schedules,
  onDone,
  onClose,
}: {
  hostId: string
  draft: Partial<Schedule>
  schedules: Schedule[]
  onDone: () => void
  onClose: () => void
}): JSX.Element {
  const client = useQueryClient()
  const [form, setForm] = useState<Partial<Schedule>>({
    kind: 'timer',
    mode: 'new',
    cron: '0 9 * * 1-5',
    ...draft,
  })
  const [error, setError] = useState('')
  const [check, setCheck] = useState<CheckResult | null>(null)
  const editing = Boolean(draft.id)

  const set = (fields: Partial<Schedule>): void => setForm((f) => ({ ...f, ...fields }))

  const save = useMutation({
    mutationFn: () =>
      editing
        ? api(hostId).updateSchedule(draft.id as string, form)
        : api(hostId).createSchedule(form),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.schedules(hostId) })
      onDone()
    },
    onError: (err: Error) => setError(err.message),
  })

  const testNow = useMutation({
    mutationFn: () => api(hostId).checkSchedule(draft.id as string),
    onSuccess: setCheck,
    onError: (err: Error) => setError(err.message),
  })

  const monitor = form.kind === 'monitor'
  const once = form.kind === 'once'
  const after = form.kind === 'after'

  return (
    <Modal title={editing ? `Edit ${draft.name}` : 'New schedule'} onClose={onClose}>
      <label className="field">
        <span>Name</span>
        <input
          value={form.name ?? ''}
          onChange={(e) => set({ name: e.target.value })}
          placeholder="morning-triage"
        />
      </label>

      <label className="field">
        <span>Fires</span>
        <select value={form.kind} onChange={(e) => set({ kind: e.target.value as Schedule['kind'] })}>
          <option value="timer">on the clock</option>
          <option value="once">once, at one moment</option>
          <option value="monitor">when a check matches</option>
          <option value="after">after another job</option>
        </select>
      </label>

      {!once && !after && (
        <label className="field">
          <span>{monitor ? 'Check every' : 'When'}</span>
          <input
            value={form.cron ?? ''}
            onChange={(e) => set({ cron: e.target.value })}
            placeholder="0 9 * * 1-5"
          />
        </label>
      )}

      {once && (
        <label className="field">
          <span>At</span>
          <input
            value={form.run_at ?? ''}
            onChange={(e) => set({ run_at: e.target.value })}
            placeholder="2026-03-02T22:00:00Z"
          />
        </label>
      )}

      {after && (
        <>
          <label className="field">
            <span>After</span>
            <select value={form.after_id ?? ''} onChange={(e) => set({ after_id: e.target.value })}>
              <option value="">choose a job…</option>
              {schedules
                .filter((sc) => sc.id !== draft.id)
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
          <label className="field">
            <span>Check</span>
            <input
              value={form.check_cmd ?? ''}
              onChange={(e) => set({ check_cmd: e.target.value, check_file: '' })}
              placeholder="make test 2>&1"
            />
          </label>
          <label className="field">
            <span>or a script</span>
            <input
              value={form.check_file ?? ''}
              onChange={(e) => set({ check_file: e.target.value, check_cmd: '' })}
              placeholder="~/checks/queue_depth.py"
            />
          </label>
          <label className="field">
            <span>Match</span>
            <input
              value={form.check_match ?? ''}
              onChange={(e) => set({ check_match: e.target.value })}
              placeholder="optional — a pattern in the output"
            />
          </label>
          <p className="dim">
            {form.check_match
              ? 'Fires when the output matches, whatever the exit code.'
              : 'Fires when the check exits non-zero.'}
          </p>
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
        <span>Prompt</span>
        <textarea
          rows={6}
          value={form.prompt ?? ''}
          onChange={(e) => set({ prompt: e.target.value })}
          placeholder={monitor ? 'The check found something:\n\n{{output}}\n\nLook into it.' : ''}
        />
      </label>
      {monitor && <p className="dim">{'{{output}}'} is replaced with what the check printed.</p>}

      {check && (
        <pre className="sched-log">
          {check.failed
            ? `check failed — ${check.error ?? ''}`
            : `exit ${check.exit} — ${check.matched ? 'MATCH, this would fire' : 'quiet, this would not fire'}`}
          {check.output ? `\n---\n${check.output}` : ''}
        </pre>
      )}

      {error && <p className="error">{error}</p>}

      <div className="sched-actions">
        {editing && monitor && (
          <button className="btn" onClick={() => testNow.mutate()}>
            Test now
          </button>
        )}
        <button className="btn" onClick={onDone}>
          Cancel
        </button>
        <button className="btn primary" onClick={() => save.mutate()}>
          Save
        </button>
      </div>
    </Modal>
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

/** What it does, in one line — the cron is in the editor, not in the list. */
function describe(sc: Schedule): string {
  const where = sc.mode === 'resume' ? `into session ${short(sc.target_session ?? '')}` : sc.cwd || 'home'
  switch (sc.kind) {
    case 'monitor': {
      const check = sc.check_cmd || sc.check_file || ''
      const rule = sc.check_match ? 'fires on a match' : 'fires on non-zero exit'
      return `${humanCron(sc.cron)} · ${check} · ${rule}`
    }
    case 'after':
      return `${sc.after_when === 'any' ? 'either way' : 'on success'} · ${where}`
    case 'once':
      return `once · ${where}`
    default:
      return `${humanCron(sc.cron)} · ${where}`
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

  if (/^\*\/\d+$/.test(min ?? '') && hour === '*' && everyDay) {
    return `every ${min.slice(2)} minutes`
  }
  if (min === '0' && hour === '*' && everyDay) return 'every hour'
  if (/^\d+$/.test(min ?? '') && /^\*\/(\d+)$/.test(hour ?? '') && everyDay) {
    return `every ${hour.slice(2)} hours`
  }
  if (/^\d+$/.test(min ?? '') && /^\d+$/.test(hour ?? '')) {
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

function nextText(sc: Schedule): string {
  if (sc.done_at) return 'done'
  if (!sc.enabled) return 'paused'
  if (sc.kind === 'after') return sc.last_status === 'running' ? 'running' : 'waiting'
  if (!sc.next_run_at) return '—'

  const until = new Date(sc.next_run_at).getTime() - Date.now()
  if (until < 0) return 'due'
  const minutes = Math.round(until / 60000)
  if (minutes < 1) return `in ${Math.round(until / 1000)}s`
  if (minutes < 60) return `in ${minutes}m`
  if (minutes < 12 * 60) return `in ${Math.floor(minutes / 60)}h ${minutes % 60}m`
  return new Date(sc.next_run_at).toLocaleString(undefined, {
    weekday: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function lastText(sc: Schedule): string {
  switch (sc.last_status) {
    case 'running':
      return '● running'
    case 'missed':
      return '! missed while nobody was home'
    case 'blocked':
      return `⊘ ${sc.last_error ?? 'blocked'}`
    case 'failed': {
      const streak =
        sc.fail_streak > 1 ? `failed ${sc.fail_streak} times running` : 'failed'
      return `✗ ${streak} — ${sc.last_error ?? ''}`
    }
    case 'ok': {
      const fires = sc.fires_today > 1 ? ` · fired ${sc.fires_today}× today` : ''
      return `✓ ran ${ago(sc.last_fired_at)}${fires}`
    }
    default:
      return sc.kind === 'monitor' && sc.last_check_at ? `last check ${ago(sc.last_check_at)}` : '—'
  }
}

function statusClass(sc: Schedule): string {
  switch (sc.last_status) {
    case 'failed':
    case 'missed':
      return 'bad'
    case 'blocked':
      return 'warn'
    case 'ok':
      return 'good'
    default:
      return ''
  }
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

function short(id: string): string {
  return id.slice(0, 8)
}
