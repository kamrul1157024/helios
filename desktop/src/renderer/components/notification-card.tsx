import { useEffect, useState } from 'react'

import type { Notification } from '../../shared/models.ts'
import { kindOf } from '../../shared/notifications.ts'

/**
 * One notification, rendered with the controls its type needs.
 *
 * Shared by the approvals panel and the HUD window, and deliberately ignorant
 * of both: the action bodies are type-specific — the daemon rejects an
 * `{action:'approve'}` sent to a question — so there must be exactly one place
 * that knows how to answer each type. Where the answer is sent, and what to
 * refresh afterwards, is the caller's business.
 */
export function NotificationCard({
  notif,
  onAct,
  onDismiss,
}: {
  notif: Notification
  onAct: (body: Record<string, unknown>) => Promise<void>
  onDismiss: () => void
}): JSX.Element {
  const [busy, setBusy] = useState(false)
  const payload = parsePayload(notif.payload)

  const act = async (body: Record<string, unknown>): Promise<void> => {
    if (busy) return
    setBusy(true)
    try {
      await onAct(body)
    } finally {
      setBusy(false)
    }
  }

  const body = ((): JSX.Element => {
    switch (kindOf(notif.type)) {
      case 'permission':
        return <PermissionCard payload={payload} busy={busy} act={act} />
      case 'question':
        return <QuestionCard payload={payload} busy={busy} act={act} />
      case 'trust':
        return (
          <Actions busy={busy}>
            <button onClick={() => void act({ action: 'trust' })}>Trust folder</button>
            <button className="ghost" onClick={() => void act({ action: 'deny' })}>
              Deny
            </button>
          </Actions>
        )
      case 'elicitation.url':
        return <UrlCard payload={payload} busy={busy} act={act} />
      case 'elicitation.form':
        return <FormCard payload={payload} busy={busy} act={act} />
      case 'error':
        return <ErrorCard payload={payload} busy={busy} act={act} />
      default:
        return (
          <Actions busy={busy}>
            <button className="ghost" onClick={onDismiss}>
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

/**
 * A turn that died on an API error.
 *
 * Retry sends "continue", which is what a user types in the terminal after an
 * API error: the CLI picks the turn up where it stopped. A rate limit with a
 * known reset time disables the button until the window lifts — retrying
 * before then just burns another failure.
 */
function ErrorCard({
  payload,
  busy,
  act,
}: {
  payload: Record<string, unknown>
  busy: boolean
  act: (body: Record<string, unknown>) => Promise<void>
}): JSX.Element {
  const text = typeof payload.error === 'string' ? payload.error : ''
  const resetAt = typeof payload.reset_at === 'string' ? Date.parse(payload.reset_at) : NaN
  const [now, setNow] = useState(() => Date.now())

  const blocked = !Number.isNaN(resetAt) && resetAt > now

  useEffect(() => {
    // Only a rate limit with a known reset time has anything to tick.
    if (!blocked) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [blocked])

  return (
    <>
      {text && <pre className="code">{text}</pre>}
      <Actions busy={busy}>
        <button disabled={blocked} onClick={() => void act({ action: 'retry' })}>
          {blocked ? remainingLabel(resetAt - now) : 'Retry'}
        </button>
        <button className="ghost" onClick={() => void act({ action: 'dismiss' })}>
          Dismiss
        </button>
      </Actions>
    </>
  )
}

/** Coarse on purpose: a second-by-second countdown on a multi-hour window is noise. */
function remainingLabel(ms: number): string {
  const seconds = Math.max(0, Math.ceil(ms / 1000))
  if (seconds >= 3600) return `Retry in ${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
  if (seconds >= 60) return `Retry in ${Math.floor(seconds / 60)}m`
  return `Retry in ${seconds}s`
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

export function label(type: string): string {
  switch (kindOf(type)) {
    case 'permission':
      return 'Permission request'
    case 'question':
      return 'Question'
    case 'elicitation.form':
      return 'Input requested'
    case 'elicitation.url':
      return 'Authentication required'
    case 'trust':
      return 'Workspace trust'
    case 'error':
      return 'Session error'
    case 'done':
      return 'Session completed'
    default:
      return type
  }
}
