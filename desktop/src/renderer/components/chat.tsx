import { useEffect, useMemo, useRef, useState } from 'react'

import { api, statusOf } from '../bridge.ts'
import { multiEditDiff, unifiedDiff } from '../diff.ts'
import { DiffView } from './diff-view.tsx'
import {
  extractFilePaths,
  highlightCode,
  languageForPath,
  renderMarkdown,
  resolveFilePath,
} from '../markdown.ts'
import { store, useStore } from '../store.ts'
import {
  BUSY_STATUSES,
  canResume,
  needsRecovery,
  type Session,
  type TranscriptMessage,
} from '../../shared/models.ts'

const PAGE = 200

export function ChatPanel({
  hostId,
  session,
  active = true,
}: {
  hostId: string
  session: Session
  /** False while another tab is showing: a hidden panel must not poll. */
  active?: boolean
}): JSX.Element {
  const [messages, setMessages] = useState<TranscriptMessage[]>([])
  // Which session the messages belong to. Switching sessions would otherwise
  // show the previous transcript until the new one arrives, and "No transcript
  // yet." for a session whose transcript is merely still loading.
  const [loadedFor, setLoadedFor] = useState('')
  const [hasMore, setHasMore] = useState(false)
  const [total, setTotal] = useState(0)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const scroller = useRef<HTMLDivElement | null>(null)
  const composer = useRef<HTMLTextAreaElement | null>(null)
  const pinnedToBottom = useRef(true)
  const promptDraft = useStore((s) => s.promptDraft)

  const status = session.status
  const busy = BUSY_STATUSES.has(status)
  const terminated = canResume(session)
  const cold = needsRecovery(session)

  useEffect(() => {
    // last_event_at moves with every hook the agent fires, so an unwatched
    // transcript would refetch itself all day. Reading it again on the way
    // back costs one request instead.
    if (!active) return
    let cancelled = false
    const load = async (): Promise<void> => {
      try {
        const page = await api(hostId).transcript(session.session_id, PAGE, 0)
        if (cancelled) return
        setMessages(page.messages)
        setTotal(page.total)
        setHasMore(page.has_more)
        setLoadedFor(session.session_id)
      } catch (err) {
        if (!cancelled) store.fail(err)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
    // Reloads as the agent works: last_event_at moves on every hook, and the
    // transcript is a file the daemon re-reads rather than a stream.
  }, [hostId, session.session_id, session.last_event_at, status, active])

  // Lines picked in the Files panel arrive here rather than being sent: what to
  // ask about them is still to be typed.
  useEffect(() => {
    if (!promptDraft || promptDraft.hostId !== hostId || promptDraft.sessionId !== session.session_id) {
      return
    }
    setDraft((current) => (current ? `${current}\n${promptDraft.text}` : promptDraft.text))
    store.clearPromptDraft()
    composer.current?.focus()
    // seq, not text: sending the same lines twice has to append twice.
  }, [promptDraft?.seq])

  useEffect(() => {
    if (pinnedToBottom.current && scroller.current) {
      scroller.current.scrollTop = scroller.current.scrollHeight
    }
  }, [messages])

  const loadOlder = async (): Promise<void> => {
    try {
      const page = await api(hostId).transcript(session.session_id, PAGE, messages.length)
      setMessages((current) => [...page.messages, ...current])
      setHasMore(page.has_more)
    } catch (err) {
      store.fail(err)
    }
  }

  const send = async (): Promise<void> => {
    const text = draft.trim()
    if (!text || sending) return
    setSending(true)
    try {
      const result = await api(hostId).sendPrompt(session.session_id, text)
      setDraft('')
      if (result.queued) store.notify('Queued — the agent is mid-turn')
      void store.refreshSessions(hostId)
    } catch (err) {
      // 409 is an answer, not a fault: the session is busy without a queue, or
      // it ended between this render and the click. Refreshing swaps the
      // composer for the resume banner, so the second attempt is not the same
      // dead end as the first.
      if (statusOf(err) === 409) {
        store.notify(
          status === 'terminated'
            ? 'Session has ended — resume to continue'
            : 'Session is busy and cannot queue prompts',
          'error',
        )
        void store.refreshSessions(hostId)
      } else {
        store.fail(err)
      }
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="chat">
      <div
        className="chat-scroll"
        ref={scroller}
        onScroll={(event) => {
          const el = event.currentTarget
          pinnedToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
        }}
      >
        {loadedFor !== session.session_id ? (
          <div className="panel-loading">
            <span className="spinner" />
            <span>Loading transcript…</span>
          </div>
        ) : (
          <>
            {hasMore && (
              <button className="link load-more" onClick={() => void loadOlder()}>
                Load older ({total - messages.length} more)
              </button>
            )}
            {messages.length === 0 && <p className="empty-note">No transcript yet.</p>}
            {messages.map((message, index) => (
              <Message
                key={`${message.timestamp}-${index}`}
                message={message}
                hostId={hostId}
                cwd={session.cwd}
              />
            ))}
            {busy && <div className="typing">agent is working…</div>}
          </>
        )}
      </div>

      {/* No composer for a terminated session: the daemon refuses its prompts,
          so offering the box only trades a typed prompt for a 409. */}
      {terminated ? (
        <div className="composer ended">
          <span className="ended-note">Session terminated — resume to continue</span>
          <button
            className="filled"
            onClick={() => void store.resumeSession(hostId, session.session_id)}
          >
            Resume
          </button>
        </div>
      ) : (
        <div className="composer">
          <div className="composer-input">
            <textarea
              ref={composer}
              value={draft}
              rows={3}
              placeholder={
                cold
                  ? 'Send a prompt — the session wakes first'
                  : 'Send a prompt (↵ to send, ⇧↵ for a new line)'
              }
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key !== 'Enter') return
                // An IME uses Enter to accept a candidate; sending there would
                // post half a word and swallow the rest.
                if (event.nativeEvent.isComposing) return
                if (event.shiftKey) return
                event.preventDefault()
                void send()
              }}
            />
            <button
              className="filled send-btn"
              disabled={!draft.trim() || sending}
              title={cold ? 'Wake and send' : 'Send (↵)'}
              aria-label={cold ? 'Wake and send' : 'Send'}
              onClick={() => void send()}
            >
              {sending ? <span className="spinner" /> : '↑'}
            </button>
          </div>
          {busy && (
            <div className="composer-actions">
              <button className="ghost" onClick={() => void api(hostId).stop(session.session_id)}>
                Stop
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

interface MessageProps {
  message: TranscriptMessage
  hostId: string
  cwd: string
}

/**
 * One transcript entry. The roles are the daemon's
 * (internal/transcript/reader.go): user, assistant, tool_use, tool_result.
 */
function Message({ message, hostId, cwd }: MessageProps): JSX.Element | null {
  switch (message.role) {
    case 'tool_use':
      return <ToolUse message={message} hostId={hostId} cwd={cwd} />
    case 'tool_result':
      return <ToolResult message={message} />
    case 'assistant':
      return <Assistant message={message} hostId={hostId} cwd={cwd} />
    case 'user':
      return (
        <div className="msg user">
          <div className="msg-body">{message.content ?? ''}</div>
        </div>
      )
    default: {
      const text = message.content ?? message.summary ?? ''
      if (!text.trim()) return null
      return (
        <div className={`msg ${message.role}`}>
          <div className="msg-role">{message.role}</div>
          <div className="msg-body">{text}</div>
        </div>
      )
    }
  }
}

/** The agent's prose: markdown, then a chip per file path it mentioned. */
function Assistant({ message, hostId, cwd }: MessageProps): JSX.Element | null {
  const text = message.content ?? ''
  const html = useMemo(() => renderMarkdown(text), [text])
  const paths = useMemo(() => extractFilePaths(text), [text])

  if (!text.trim()) return null

  return (
    <div className="msg assistant">
      <div className="msg-head">
        <span className="msg-role">agent</span>
        <button
          className="icon-btn tiny"
          title="Copy message"
          onClick={() => void navigator.clipboard.writeText(text)}
        >
          ⧉
        </button>
      </div>
      <div className="msg-body md" dangerouslySetInnerHTML={{ __html: html }} />
      {paths.length > 0 && (
        <div className="file-chips">
          {paths.map((path) => (
            <FileChip key={path} hostId={hostId} cwd={cwd} path={path} />
          ))}
        </div>
      )}
    </div>
  )
}

/** A mentioned path, opened in the Files panel — as tapping one does on mobile. */
function FileChip({
  hostId,
  cwd,
  path,
  label,
}: {
  hostId: string
  cwd: string
  path: string
  label?: string
}): JSX.Element {
  const resolved = resolveFilePath(path, cwd)
  const name = label ?? path.split('/').filter(Boolean).pop() ?? path
  const isDir = !name.includes('.')
  return (
    <button
      className="file-chip"
      title={`Open ${resolved}`}
      onClick={() => store.openFile(hostId, resolved)}
    >
      <span className="file-chip-icon">{isDir ? '▸' : '⌸'}</span>
      {name}
    </button>
  )
}

const TOOL_ICONS: Record<string, string> = {
  Read: '⌸',
  Write: '✎',
  Edit: '✎',
  MultiEdit: '✎',
  NotebookEdit: '✎',
  Bash: '❯',
  BashOutput: '❯',
  Glob: '✳',
  Grep: '⌕',
  Agent: '◈',
  Task: '◈',
  WebFetch: '⇩',
  WebSearch: '⌕',
  TodoWrite: '☑',
}

/** Fields the expanded view renders as code rather than as a `key: value` line. */
const CODE_FIELDS: { key: string; label?: string }[] = [
  { key: 'content' },
  { key: 'new_content', label: 'new' },
  { key: 'new_string', label: 'new' },
  { key: 'old_string', label: 'old' },
]

/** Tools whose call changes a file, and whose diff is the point of the row. */
const WRITING_TOOLS = new Set(['Edit', 'MultiEdit', 'Write'])

/**
 * A tool call: one line collapsed, the input expanded. The expansion is
 * tool-aware because a Bash command and an Edit's replacement text want
 * different treatment, and a raw JSON dump serves neither.
 */
function ToolUse({ message, hostId, cwd }: MessageProps): JSX.Element {
  const tool = message.tool ?? 'tool'
  const input = (message.metadata ?? {}) as Record<string, unknown>
  const filePath = typeof input.file_path === 'string' ? input.file_path : null
  // A write is the part of a session worth reading, and a collapsed row names
  // the tool without saying what it did to the file.
  const [open, setOpen] = useState(WRITING_TOOLS.has(tool))

  return (
    <div className="msg tool">
      <button className="tool-head" onClick={() => setOpen(!open)}>
        <span className="tool-icon">{TOOL_ICONS[tool] ?? '⚙'}</span>
        <span className="tool-name">{tool}</span>
        <span className="tool-summary">{message.summary ?? ''}</span>
        <span className="chevron">{open ? '▾' : '▸'}</span>
      </button>
      {open && (
        <div className="tool-detail">
          <ToolInput tool={tool} input={input} />
          {filePath && (
            <div className="file-chips">
              <FileChip hostId={hostId} cwd={cwd} path={filePath} label={filePath.split('/').pop()} />
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function ToolInput({ tool, input }: { tool: string; input: Record<string, unknown> }): JSX.Element {
  const entries = Object.entries(input)
  if (entries.length === 0) return <p className="tool-empty">No input recorded.</p>

  if (tool === 'Bash' || tool === 'BashOutput') {
    const command = str(input.command) || str(input.cmd)
    return (
      <>
        {command && <CodeBlock code={command} language="bash" />}
        <KeyValues entries={entries.filter(([key]) => key !== 'command' && key !== 'cmd')} />
      </>
    )
  }

  // What changed, as a patch. Two code blocks — the text searched for and the
  // text written — leave the reader diffing them by eye.
  const diff = diffFor(tool, input)
  if (diff) {
    const coded = new Set([...CODE_FIELDS.map((f) => f.key), 'edits'])
    return (
      <>
        <KeyValues entries={entries.filter(([key]) => !coded.has(key))} />
        <div className="tool-diff">
          <DiffView diff={diff} />
        </div>
      </>
    )
  }

  if (tool === 'Read' || tool === 'Write' || tool === 'Edit' || tool === 'MultiEdit') {
    const language = languageForPath(str(input.file_path))
    const coded = new Set(CODE_FIELDS.map((f) => f.key))
    return (
      <>
        <KeyValues entries={entries.filter(([key]) => !coded.has(key))} />
        {CODE_FIELDS.filter((field) => str(input[field.key])).map((field) => (
          <div key={field.key} className="tool-code">
            {field.label && <span className="tool-code-label">{field.label}</span>}
            <CodeBlock code={str(input[field.key])} language={language} />
          </div>
        ))}
      </>
    )
  }

  return <KeyValues entries={entries} />
}

/** The patch a tool call implies, or "" for calls that changed no file. */
function diffFor(tool: string, input: Record<string, unknown>): string {
  if (tool === 'MultiEdit') return multiEditDiff(input.edits)
  if (tool === 'Edit') {
    const before = str(input.old_string)
    const after = str(input.new_string)
    return before || after ? unifiedDiff(before, after) : ''
  }
  if (tool === 'Write') {
    const content = str(input.content) || str(input.new_content)
    return content ? unifiedDiff('', content) : ''
  }
  return ''
}

function KeyValues({ entries }: { entries: [string, unknown][] }): JSX.Element | null {
  if (entries.length === 0) return null
  return (
    <dl className="tool-fields">
      {entries.map(([key, value]) => (
        <div key={key}>
          <dt>{key}</dt>
          <dd>{truncate(stringify(value), 500)}</dd>
        </div>
      ))}
    </dl>
  )
}

function CodeBlock({ code, language }: { code: string; language?: string | null }): JSX.Element {
  const html = useMemo(() => highlightCode(code, language), [code, language])
  return (
    <pre className="code-block">
      <code className="hljs" dangerouslySetInnerHTML={{ __html: html }} />
    </pre>
  )
}

/** The result of a call, which is a single line: it worked, or it did not. */
function ToolResult({ message }: { message: TranscriptMessage }): JSX.Element {
  const failed = message.success === false
  return (
    <div className={`msg tool-result ${failed ? 'failed' : ''}`}>
      <span className="result-icon">{failed ? '✕' : '✓'}</span>
      <span>
        {message.tool ? <code>{message.tool}</code> : 'Tool'} {failed ? 'failed' : 'done'}
      </span>
    </div>
  )
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function stringify(value: unknown): string {
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value) ?? String(value)
  } catch {
    return String(value)
  }
}

function truncate(text: string, limit: number): string {
  return text.length > limit ? `${text.slice(0, limit)}…` : text
}
