import { useState } from 'react'

import { api, statusOf } from '../bridge.ts'
import { store, useStore } from '../store.ts'
import type { Notification } from '../../shared/models.ts'

/**
 * The pending approvals for one session.
 *
 * The daemon dispatches actions by notification type, so the card shapes here
 * mirror internal/provider/claude/actions.go rather than a generic
 * approve/deny — a question that gets {action:'approve'} is simply rejected.
 */
export function ApprovalsPanel({ hostId, sessionId }: { hostId: string; sessionId: string }): JSX.Element {
  const all = useStore((s) => s.notifications)
  const pending = (all[hostId] ?? []).filter((n) => n.source_session === sessionId)

  if (pending.length === 0) {
    return <p className="empty-note">Nothing waiting for you.</p>
  }

  return (
    <div className="approvals">
      {pending.map((notif) => (
        <Card key={notif.id} hostId={hostId} notif={notif} />
      ))}
    </div>
  )
}

function Card({ hostId, notif }: { hostId: string; notif: Notification }): JSX.Element {
  const [busy, setBusy] = useState(false)
  const payload = parsePayload(notif.payload)

  const act = async (body: Record<string, unknown>): Promise<void> => {
    if (busy) return
    setBusy(true)
    try {
      await api(hostId).notificationAction(notif.id, body)
      void store.refreshNotifications(hostId)
    } catch (err) {
      // 410 means someone else — the terminal, the phone — got there first.
      if (statusOf(err) === 410) {
        store.notify('Already answered elsewhere')
        void store.refreshNotifications(hostId)
      } else {
        store.fail(err)
      }
    } finally {
      setBusy(false)
    }
  }

  const body = ((): JSX.Element => {
    switch (notif.type) {
      case 'claude.permission':
        return <PermissionCard payload={payload} busy={busy} act={act} />
      case 'claude.question':
        return <QuestionCard payload={payload} busy={busy} act={act} />
      case 'claude.trust':
        return (
          <Actions busy={busy}>
            <button onClick={() => void act({ action: 'trust' })}>Trust folder</button>
            <button className="ghost" onClick={() => void act({ action: 'deny' })}>
              Deny
            </button>
          </Actions>
        )
      case 'claude.elicitation.url':
        return <UrlCard payload={payload} busy={busy} act={act} />
      case 'claude.elicitation.form':
        return <FormCard payload={payload} busy={busy} act={act} />
      default:
        return (
          <Actions busy={busy}>
            <button className="ghost" onClick={() => void api(hostId).dismissNotification(notif.id)}>
              Dismiss
            </button>
          </Actions>
        )
    }
  })()

  return (
    <section className="card">
      <header>
        <span className="card-type">{label(notif.type)}</span>
        <h3>{notif.title ?? label(notif.type)}</h3>
        {notif.detail && <p className="card-detail">{notif.detail}</p>}
      </header>
      {body}
    </section>
  )
}

function PermissionCard({
  payload,
  busy,
  act,
}: {
  payload: Record<string, unknown>
  busy: boolean
  act: (body: Record<string, unknown>) => Promise<void>
}): JSX.Element {
  const toolInput = payload.tool_input
  const original = typeof toolInput === 'string' ? toolInput : JSON.stringify(toolInput ?? {}, null, 2)
  const suggestions = Array.isArray(payload.permission_suggestions) ? payload.permission_suggestions : []

  const [editing, setEditing] = useState(false)
  const [edited, setEdited] = useState(original)
  const [rule, setRule] = useState<number | null>(null)

  const approve = (): void => {
    const body: Record<string, unknown> = { action: 'approve' }
    if (editing && edited !== original) {
      try {
        body.updated_input = JSON.parse(edited)
      } catch {
        // Not JSON: the common edit is a shell command, and that is the field
        // the tool reads. Matches the mobile client's fallback.
        body.updated_input = { command: edited }
      }
    }
    if (rule !== null) body.apply_permission = rule
    void act(body)
  }

  return (
    <>
      {editing ? (
        <textarea className="code-edit" value={edited} rows={6} onChange={(e) => setEdited(e.target.value)} />
      ) : (
        <pre className="code">{original}</pre>
      )}

      {suggestions.length > 0 && (
        <div className="rules">
          <span className="rules-head">Quick rules</span>
          {suggestions.map((suggestion, index) => (
            <label key={index} className="check">
              <input
                type="checkbox"
                checked={rule === index}
                onChange={() => setRule(rule === index ? null : index)}
              />
              {describeSuggestion(suggestion)}
            </label>
          ))}
        </div>
      )}

      <Actions busy={busy}>
        <button onClick={approve}>Approve</button>
        <button className="ghost" onClick={() => void act({ action: 'deny' })}>
          Deny
        </button>
        <button className="link" onClick={() => setEditing(!editing)}>
          {editing ? 'Cancel editing' : 'Edit before approving'}
        </button>
      </Actions>
    </>
  )
}

interface Question {
  question: string
  header?: string
  multiSelect?: boolean
  options?: { label: string; description?: string }[]
}

function QuestionCard({
  payload,
  busy,
  act,
}: {
  payload: Record<string, unknown>
  busy: boolean
  act: (body: Record<string, unknown>) => Promise<void>
}): JSX.Element {
  const questions = (Array.isArray(payload.questions) ? payload.questions : []) as Question[]
  // Question index → option index. Indices rather than labels: the daemon
  // answers by moving the CLI's own highlight, so position is what it needs.
  const [chosen, setChosen] = useState<Record<number, number>>({})

  const submit = (): void => {
    const selections = Object.entries(chosen)
      .map(([questionIndex, optionIndex]) => ({
        question_index: Number(questionIndex),
        option_index: optionIndex,
      }))
      .sort((a, b) => a.question_index - b.question_index)
    void act({ action: 'answer', selections })
  }

  return (
    <>
      {questions.map((question, questionIndex) => (
        <div key={questionIndex} className="question">
          {question.header && <span className="question-head">{question.header}</span>}
          <p>{question.question}</p>
          {(question.options ?? []).map((option, optionIndex) => (
            <button
              key={optionIndex}
              className={`option ${chosen[questionIndex] === optionIndex ? 'chosen' : ''}`}
              onClick={() => setChosen((current) => ({ ...current, [questionIndex]: optionIndex }))}
            >
              <span className="option-label">{option.label}</span>
              {option.description && <span className="option-desc">{option.description}</span>}
            </button>
          ))}
          {/* Answering drives the CLI's own list, which takes one highlighted
              option per question. Picking several needs the terminal. */}
          {question.multiSelect && (
            <small className="option-desc">Pick one here, or answer in the terminal to choose several.</small>
          )}
        </div>
      ))}

      <Actions busy={busy}>
        {/* Every question, not just one: the daemon walks the CLI through them
            in order and a gap would leave it stranded. */}
        <button disabled={Object.keys(chosen).length !== questions.length} onClick={submit}>
          Submit
        </button>
        <button className="ghost" onClick={() => void act({ action: 'skip' })}>
          Skip
        </button>
      </Actions>
    </>
  )
}

function UrlCard({
  payload,
  busy,
  act,
}: {
  payload: Record<string, unknown>
  busy: boolean
  act: (body: Record<string, unknown>) => Promise<void>
}): JSX.Element {
  const url = typeof payload.url === 'string' ? payload.url : ''
  return (
    <>
      {url && (
        <a className="ext-link" href={url} target="_blank" rel="noreferrer">
          {url}
        </a>
      )}
      <Actions busy={busy}>
        <button onClick={() => void act({ action: 'accept' })}>Done</button>
        <button className="ghost" onClick={() => void act({ action: 'decline' })}>
          Decline
        </button>
      </Actions>
    </>
  )
}

function FormCard({
  payload,
  busy,
  act,
}: {
  payload: Record<string, unknown>
  busy: boolean
  act: (body: Record<string, unknown>) => Promise<void>
}): JSX.Element {
  const schema = (payload.requested_schema ?? {}) as {
    properties?: Record<string, { title?: string; description?: string; type?: string }>
    required?: string[]
  }
  const fields = Object.entries(schema.properties ?? {})
  const [values, setValues] = useState<Record<string, string>>({})

  return (
    <>
      {fields.map(([name, field]) => (
        <label key={name} className="field">
          <span>{field.title ?? name}</span>
          <input
            value={values[name] ?? ''}
            onChange={(event) => setValues({ ...values, [name]: event.target.value })}
          />
          {field.description && <small>{field.description}</small>}
        </label>
      ))}
      {fields.length === 0 && <p className="empty-note">No fields requested.</p>}

      <Actions busy={busy}>
        <button onClick={() => void act({ action: 'accept', content: values })}>Submit</button>
        <button className="ghost" onClick={() => void act({ action: 'decline' })}>
          Decline
        </button>
      </Actions>
    </>
  )
}

function Actions({ busy, children }: { busy: boolean; children: React.ReactNode }): JSX.Element {
  return <div className={`card-actions ${busy ? 'busy' : ''}`}>{children}</div>
}

function parsePayload(raw?: string): Record<string, unknown> {
  if (!raw) return {}
  try {
    const parsed: unknown = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : {}
  } catch {
    return {}
  }
}

/** Suggestions come from the CLI verbatim, so render whatever shape arrives. */
function describeSuggestion(suggestion: unknown): string {
  if (typeof suggestion === 'string') return suggestion
  const record = suggestion as Record<string, unknown>
  const rules = record.rules
  if (Array.isArray(rules)) {
    return rules
      .map((rule) => {
        const r = rule as Record<string, unknown>
        return [r.toolName, r.ruleContent].filter(Boolean).join(' ')
      })
      .join(', ')
  }
  const mode = typeof record.mode === 'string' ? record.mode : ''
  const destination = typeof record.destination === 'string' ? record.destination : ''
  return [mode, destination].filter(Boolean).join(' → ') || JSON.stringify(suggestion)
}

function label(type: string): string {
  switch (type) {
    case 'claude.permission':
      return 'Permission request'
    case 'claude.question':
      return 'Question'
    case 'claude.elicitation.form':
      return 'Input requested'
    case 'claude.elicitation.url':
      return 'Authentication required'
    case 'claude.trust':
      return 'Workspace trust'
    default:
      return type
  }
}
