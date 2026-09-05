import { useEffect, useState } from 'react'

import type { Notification } from '../../shared/models.ts'
import { kindOf, providerOf } from '../../shared/notifications.ts'

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
        return (
          <PermissionCard payload={payload} provider={providerOf(notif.type)} busy={busy} act={act} />
        )
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

// readableInput lays a tool's input out as one field per block, with any
// multi-line value — a file's contents, a patch — printed as the lines it is
// rather than as one escaped string.
function readableInput(fields: Record<string, unknown> | null): string | null {
  if (!fields) return null
  const entries = Object.entries(fields)
  if (entries.length === 0) return null
  return entries
    .map(([key, value]) =>
      typeof value === 'string' && value.includes('\n')
        ? `${key}:\n${value}`
        : `${key}: ${typeof value === 'string' ? value : JSON.stringify(value)}`,
    )
    .join('\n\n')
}

/**
 * The two ways to say yes to a plan, worded as the CLI words them, and the
 * name each one travels under.
 *
 * The name rather than the label, because the daemon presses the matching row
 * on the CLI's own dialog and that copy is the CLI's to change.
 */
const planRows = [
  {
    choice: 'auto',
    label: 'Yes, and use auto mode',
    detail: 'Claude edits and runs commands without asking, for the rest of this session',
  },
  {
    choice: 'manual',
    label: 'Yes, manually approve edits',
    detail: 'Claude asks before each edit, as it does now',
  },
] as const

/**
 * A plan waiting for approval.
 *
 * On the wire it is a permission like any other, but it is not a yes-or-no
 * question: the answer picks the mode the session continues in, or sends the
 * plan back in words. See docs/specs/57-plan-approval.md.
 */
function PlanCard({
  plan,
  busy,
  act,
}: {
  plan: string
  busy: boolean
  act: (body: Record<string, unknown>) => Promise<void>
}): JSX.Element {
  const [choice, setChoice] = useState<string | null>(null)
  const [feedback, setFeedback] = useState('')
  const words = feedback.trim()

  return (
    <>
      <pre className="code">{plan}</pre>

      {planRows.map((row) => (
        <button
          key={row.choice}
          className={`option ${choice === row.choice ? 'chosen' : ''}`}
          onClick={() => setChoice(row.choice)}
        >
          <span className="option-label">{row.label}</span>
          <span className="option-desc">{row.detail}</span>
        </button>
      ))}

      <label className="field">
        <span>Tell Claude what to change</span>
        <input
          value={feedback}
          placeholder="Send the plan back in your own words"
          onChange={(event) => setFeedback(event.target.value)}
        />
      </label>

      <Actions busy={busy}>
        {/* A plan cannot be approved without saying which way. */}
        <button
          disabled={choice === null}
          onClick={() => void act({ action: 'approve', plan_choice: choice })}
        >
          Approve
        </button>
        {/* Words turn a refusal into a re-plan, so the button says what it
            will do with them. */}
        <button
          className="ghost"
          onClick={() => void act(words === '' ? { action: 'deny' } : { action: 'deny', feedback: words })}
        >
          {words === '' ? 'Deny' : 'Send back'}
        </button>
      </Actions>
    </>
  )
}

function PermissionCard({
  payload,
  provider,
  busy,
  act,
}: {
  payload: Record<string, unknown>
  provider: string
  busy: boolean
  act: (body: Record<string, unknown>) => Promise<void>
}): JSX.Element {
  const toolInput = payload.tool_input
  // The field the tool actually runs, when there is one. A command goes through
  // JSON.stringify as a single line of \n escapes, which is unreadable exactly
  // when it matters most: a heredoc writing a file is the thing you most want
  // to read before approving it.
  const fields = toolInput && typeof toolInput === 'object' ? (toolInput as Record<string, unknown>) : null
  const command = typeof fields?.command === 'string' ? fields.command : null
  const original =
    typeof toolInput === 'string' ? toolInput : (command ?? readableInput(fields) ?? '{}')
  const suggestions = Array.isArray(payload.permission_suggestions) ? payload.permission_suggestions : []

  const [editing, setEditing] = useState(false)
  const [edited, setEdited] = useState(original)
  const [rule, setRule] = useState<number | null>(null)
  const [feedback, setFeedback] = useState('')

  // Codex's own approval dialog offers "No, and tell Codex what to do
  // differently". Helios paints over that dialog, so it has to offer the row
  // too or the only refusal it leaves is a bare no. Claude's plan card carries
  // its own field; its other tools take a rule instead.
  const takesFeedback = provider === 'codex'
  const words = feedback.trim()

  // A plan is prose and its answer is not a yes-or-no. The state above is
  // declared first so the hook order holds whichever card is drawn.
  const plan = typeof fields?.plan === 'string' ? fields.plan : null
  if (payload.tool_name === 'ExitPlanMode' && plan !== null) {
    return <PlanCard plan={plan.trimEnd()} busy={busy} act={act} />
  }

  const approve = (): void => {
    const body: Record<string, unknown> = { action: 'approve' }
    if (editing && edited !== original) {
      if (command !== null) {
        // Editing the command edits that one field: the rest of the input —
        // a description, a timeout — is not the user's to lose.
        body.updated_input = { ...fields, command: edited }
      } else {
        try {
          body.updated_input = JSON.parse(edited)
        } catch {
          // Not JSON: the common edit is a shell command, and that is the field
          // the tool reads. Matches the mobile client's fallback.
          body.updated_input = { command: edited }
        }
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

      {takesFeedback && (
        <label className="field">
          <span>Tell Codex what to do differently</span>
          <input
            value={feedback}
            placeholder="Say what to do instead"
            onChange={(event) => setFeedback(event.target.value)}
          />
        </label>
      )}

      <Actions busy={busy}>
        <button onClick={approve}>Approve</button>
        {/* Words turn a refusal into another attempt, so the button says what
            it will do with them. */}
        <button
          className="ghost"
          onClick={() => void act(words === '' ? { action: 'deny' } : { action: 'deny', feedback: words })}
        >
          {words === '' ? 'Deny' : 'Send back'}
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
  // Question index → option indices. Indices rather than labels: the daemon
  // answers by moving the CLI's own highlight, so position is what it needs.
  // A multi-select question keeps several; the rest replace the one they hold.
  const [chosen, setChosen] = useState<Record<number, number[]>>({})
  // Question index → what was typed instead of picking.
  const [typed, setTyped] = useState<Record<number, string>>({})

  const pick = (question: Question, questionIndex: number, optionIndex: number): void =>
    setChosen((current) => {
      const held = current[questionIndex] ?? []
      if (!question.multiSelect) {
        return { ...current, [questionIndex]: [optionIndex] }
      }
      const next = held.includes(optionIndex)
        ? held.filter((i) => i !== optionIndex)
        : [...held, optionIndex].sort((a, b) => a - b)
      return { ...current, [questionIndex]: next }
    })

  const answered = (index: number): boolean =>
    (chosen[index] ?? []).length > 0 || (typed[index] ?? '').trim() !== ''

  const submit = (): void => {
    const selections = Object.entries(chosen)
      .flatMap(([questionIndex, optionIndexes]) =>
        optionIndexes.map((optionIndex) => ({
          question_index: Number(questionIndex),
          option_index: optionIndex,
        })),
      )
      .sort((a, b) => a.question_index - b.question_index)

    // One text field on the wire for the whole set, so an answer past the
    // first says which question it belongs to.
    const written = questions
      .map((question, index) => [question, (typed[index] ?? '').trim()] as const)
      .filter(([, text]) => text !== '')
      .map(([question, text]) =>
        questions.length === 1 ? text : `${question.header ?? question.question}: ${text}`,
      )

    void act({ action: 'answer', selections, text: written.join('\n') })
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
              className={`option ${(chosen[questionIndex] ?? []).includes(optionIndex) ? 'chosen' : ''}`}
              aria-pressed={(chosen[questionIndex] ?? []).includes(optionIndex)}
              onClick={() => pick(question, questionIndex, optionIndex)}
            >
              <span className="option-label">
                {question.multiSelect
                  ? (chosen[questionIndex] ?? []).includes(optionIndex)
                    ? '☑ '
                    : '☐ '
                  : (chosen[questionIndex] ?? []).includes(optionIndex)
                    ? '◉ '
                    : '○ '}
                {option.label}
              </span>
              {option.description && <span className="option-desc">{option.description}</span>}
            </button>
          ))}
          <label className="field">
            <span>Other</span>
            <input
              value={typed[questionIndex] ?? ''}
              placeholder="Answer in your own words"
              onChange={(event) =>
                setTyped((current) => ({ ...current, [questionIndex]: event.target.value }))
              }
            />
          </label>
        </div>
      ))}

      <Actions busy={busy}>
        {/* Every question, not just one: the daemon walks the CLI through them
            in order and a gap would leave it stranded. */}
        <button disabled={!questions.every((_, index) => answered(index))} onClick={submit}>
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
