import { useEffect, useMemo, useRef, useState } from 'react'

import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'

import { api, statusOf } from '../bridge.ts'
import { keys } from '../keys.ts'
import { appendDelta, transcriptMessages, transcriptQuery } from '../queries.ts'
import { removeFirst } from '../attachments.ts'
import { AttachButton, AttachmentChips, PasteOffer, useAttachments, useDropTarget } from './attach.tsx'
import { multiEditDiff, unifiedDiff } from '../diff.ts'
import { DiffView } from './diff-view.tsx'
import { Chevron } from './icons.tsx'
import { SelectionMenu, useTextSelection } from './selection-menu.tsx'
import {
  extractFilePaths,
  highlightCode,
  languageForPath,
  renderMarkdown,
  resolveFilePath,
} from '../markdown.ts'
import { useMermaid } from '../mermaid.ts'
import { store, useStore } from '../store.ts'
import {
  BUSY_STATUSES,
  canResume,
  needsRecovery,
  type Session,
  type TranscriptMessage,
  type TranscriptPage,
} from '../../shared/models.ts'

const PAGE = 50

/** What the cache holds under the transcript key. */
interface TranscriptPages {
  pages: TranscriptPage[]
  pageParams: unknown[]
}

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
  const client = useQueryClient()
  const transcript = useInfiniteQuery({ ...transcriptQuery(hostId, session.session_id), enabled: active })
  const messages = useMemo(() => transcriptMessages(transcript.data), [transcript.data])
  // The newest page answers for the whole conversation: its total is the count,
  // and the epoch is which parse the held seq numbers count against.
  const newestPage = transcript.data?.pages[0]
  const total = newestPage?.total ?? 0
  const epoch = newestPage?.epoch ?? ''
  // Switching sessions must not show the previous transcript, nor "No
  // transcript yet." for one that is merely still loading.
  const loaded = transcript.isSuccess
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const files = useAttachments()
  const { dropping, handlers: dropHandlers } = useDropTarget((dropped) => void files.attach(dropped))
  const retries = useRef<ReturnType<typeof setTimeout>[]>([])
  const scroller = useRef<HTMLDivElement | null>(null)
  const composer = useRef<HTMLTextAreaElement | null>(null)
  const pinnedToBottom = useRef(true)
  // The messages a delta has to follow on from, without the delta effect
  // re-running every time one arrives.
  const messagesRef = useRef<TranscriptMessage[]>([])
  const loadingOlder = useRef(false)
  // Scroll height captured before older messages are prepended.
  const anchor = useRef<number | null>(null)
  const promptDraft = useStore((s) => s.promptDraft)
  const [selection, clearSelection] = useTextSelection(scroller)

  const status = session.status
  const busy = BUSY_STATUSES.has(status)
  const terminated = canResume(session)
  const cold = needsRecovery(session)

  /**
   * Only what the agent has added since.
   *
   * last_event_at moves on every hook it fires, which for a busy session is
   * several times a turn — refetching the pages each time would rebuild the
   * transcript and lose the reader's place for the sake of one new message. So
   * the delta is appended into the newest page rather than fetched as one.
   *
   * There is no transcript event on the wire: the session record moving is what
   * says there is more to read.
   */
  useEffect(() => {
    if (!active || !loaded) return
    // No epoch means what is held is the empty answer the daemon serves for a
    // session whose agent has not written its log yet — which is every session
    // for the first second of its life. There is no delta to ask from: the
    // pages have to be asked again, and the session record moving is the sign
    // that the file has since appeared. Without this the panel keeps that empty
    // page for ever, because the transcript never goes stale and nothing
    // invalidates it.
    if (!epoch) {
      void client.invalidateQueries({ queryKey: keys.transcript(hostId, session.session_id) })
      return
    }
    // Read the mark from what the cache holds for *this* session, not from the
    // ref. The ref is written in an effect that runs after this one and it
    // outlives a change of session, so on a switch it still holds the previous
    // conversation: ask from its last seq and the daemon replies with messages
    // this session already has, which then print twice.
    const heldNow = client.getQueryData<TranscriptPages>(keys.transcript(hostId, session.session_id))
    const currently = transcriptMessages(heldNow)
    const newest = currently[currently.length - 1]?.seq ?? -1
    let cancelled = false
    const load = async (): Promise<void> => {
      try {
        const page = await api(hostId).transcriptSince(session.session_id, newest, epoch, PAGE)
        if (cancelled || page.messages.length === 0) return
        if (page.epoch_changed) {
          // The transcript is no longer the one those seq numbers counted
          // against — forked, or replaced. What is held has to go, and the
          // query refetches from scratch under the same key.
          await client.resetQueries({ queryKey: keys.transcript(hostId, session.session_id) })
          return
        }
        client.setQueryData(keys.transcript(hostId, session.session_id), (held: TranscriptPages | undefined) =>
          appendDelta(held, page),
        )
      } catch (err) {
        if (!cancelled) store.fail(err)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [hostId, session.session_id, session.last_event_at, status, active, loaded, epoch, client])

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
    messagesRef.current = messages
    const el = scroller.current
    if (!el) return
    // Older messages arriving above the reader must not move what they are
    // reading: the view stays where it was by the height that was inserted.
    if (anchor.current !== null) {
      el.scrollTop += el.scrollHeight - anchor.current
      anchor.current = null
      return
    }
    if (pinnedToBottom.current) el.scrollTop = el.scrollHeight
  }, [messages])

  useEffect(() => () => retries.current.forEach(clearTimeout), [])

  const loadOlder = async (): Promise<void> => {
    if (loadingOlder.current || !transcript.hasNextPage) return
    loadingOlder.current = true
    // Captured before the older page is prepended, so the effect below can put
    // the reader back where they were.
    anchor.current = scroller.current?.scrollHeight ?? null
    try {
      await transcript.fetchNextPage()
    } catch (err) {
      anchor.current = null
      store.fail(err)
    } finally {
      loadingOlder.current = false
    }
  }

  /** Moves the block the user just pasted out of the composer and into a file. */
  const fileThePaste = (): void => {
    const text = files.fileThePaste()
    if (text === null) return
    setDraft((current) => removeFirst(current, text))
  }

  const send = async (): Promise<void> => {
    const text = draft.trim()
    if ((!text && files.files.length === 0) || sending) return
    setSending(true)
    try {
      // Upload first: a prompt naming a path the daemon never stored is worse
      // than no prompt, and the agent would go looking for it.
      const message = await files.store(hostId, session.session_id, text)

      const result = await api(hostId).sendPrompt(session.session_id, message)
      setDraft('')
      files.clear()
      if (result.queued) store.notify('Queued — the agent is mid-turn')
      void store.invalidateSessionsFor(hostId)
      // The agent writes the prompt to its transcript a moment after accepting
      // it, and the reads triggered by the status change land before that. A
      // turn that then does nothing hook-worthy moves last_event_at no further,
      // so without these the message the user just sent stays invisible until
      // the panel is reopened.
      retries.current.forEach(clearTimeout)
      // Asked of the query rather than of a counter: a refetch of the pages is
      // what a re-read means now.
      retries.current = [5_000, 10_000].map((delay) =>
        setTimeout(() => void transcript.refetch(), delay),
      )
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
        void store.invalidateSessionsFor(hostId)
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
          // Near the top is a request for what came before it.
          if (el.scrollTop < 200) void loadOlder()
        }}
      >
        {!loaded ? (
          <div className="panel-loading">
            <span className="spinner" />
            <span>Loading transcript…</span>
          </div>
        ) : (
          <>
            {transcript.hasNextPage && (
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

      {/* The transcript has no file behind it to point at, so a selection
          travels as the text itself. */}
      {selection && (
        <SelectionMenu
          anchor="above"
          x={selection.x}
          y={selection.y}
          actions={[
            {
              label: 'Copy',
              run: () => {
                void navigator.clipboard.writeText(selection.text)
                store.notify('Copied selection')
              },
            },
            {
              label: 'Send as prompt',
              run: () => store.appendPrompt(hostId, session.session_id, quote(selection.text)),
            },
          ]}
          onClose={clearSelection}
        />
      )}

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
        <div className={dropping ? 'composer dropping' : 'composer'} {...dropHandlers}>
          {files.pasted !== null && draft.includes(files.pasted) && (
            <PasteOffer text={files.pasted} onFile={fileThePaste} onKeep={files.keepThePaste} />
          )}

          <AttachmentChips files={files.files} onRemove={files.remove} />

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
              onPaste={(event) => {
                // A screenshot on the clipboard comes through as a file. Let
                // the default run when there is none, or pasted text is lost.
                if (event.clipboardData.files.length > 0) {
                  event.preventDefault()
                  void files.attach(event.clipboardData.files)
                  return
                }
                files.noticePaste(event.clipboardData.getData('text'))
              }}
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
            <div className="composer-bar">
              <AttachButton onFiles={(chosen) => void files.attach(chosen)} disabled={sending} />
              {busy && (
                <button
                  className="icon-btn stop-btn"
                  title="Stop the agent — the turn ends where it is"
                  aria-label="Stop the agent"
                  onClick={() => void api(hostId).stop(session.session_id)}
                >
                  ■
                </button>
              )}
              <button
                className="filled send-btn"
                disabled={(!draft.trim() && files.files.length === 0) || sending}
                title={cold ? 'Wake and send' : 'Send (↵)'}
                aria-label={cold ? 'Wake and send' : 'Send'}
                onClick={() => void send()}
              >
                {sending ? <span className="spinner" /> : '↑'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

/** Quoted, so the composer keeps the lines apart from what is typed about them. */
function quote(text: string): string {
  return `${text
    .trim()
    .split('\n')
    .map((line) => `> ${line}`)
    .join('\n')}\n`
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
  const body = useRef<HTMLDivElement | null>(null)
  useMermaid(body, html)

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
      <div className="msg-body md" ref={body} dangerouslySetInnerHTML={{ __html: html }} />
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
    <span className="file-chip">
      {/* Search, not open: the transcript's path is the checkout the agent ran
          in, and the Files panel is often rooted somewhere else by then. */}
      <button
        className="file-chip-open"
        title={`Find ${name} in the Files panel`}
        onClick={() => store.findFile(hostId, resolved)}
      >
        <span className="file-chip-icon">{isDir ? <Chevron dir="right" /> : '⌕'}</span>
        {name}
      </button>
      <button className="file-chip-act" title={`Open ${resolved}`} onClick={() => store.openFile(hostId, resolved)}>
        ↗
      </button>
      <button
        className="file-chip-act"
        title="Copy path"
        onClick={() => {
          void navigator.clipboard.writeText(resolved)
          store.notify('Copied path')
        }}
      >
        ⧉
      </button>
    </span>
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
 * A summary is one line by definition, but the thing it summarises often is
 * not — a heredoc, a multi-line command. Collapsing the whitespace here rather
 * than leaving it to `white-space: nowrap` keeps the ellipsis honest: the
 * browser would otherwise measure the untouched string and decide the row
 * needs a width no sidebar has.
 */
function oneLine(text: string | undefined): string {
  return (text ?? '').replace(/\s+/g, ' ').trim()
}

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
    <div className="msg tool-call">
      {/* The head is a row rather than one button: the file chip is three
          buttons of its own, and a button cannot hold a button. */}
      <div className="tool-head">
        <button className="tool-toggle" onClick={() => setOpen(!open)}>
          <span className="tool-icon">{TOOL_ICONS[tool] ?? '⚙'}</span>
          <span className="tool-name">{tool}</span>
          <span className="tool-summary">{oneLine(message.summary)}</span>
        </button>
        {/* On the head, not at the foot of the expansion: the file is what the
            row is about, and reaching it should not cost an expand first. */}
        {filePath && <FileChip hostId={hostId} cwd={cwd} path={filePath} label={filePath.split('/').pop()} />}
        <button
          className="tool-chevron"
          title={open ? 'Collapse' : 'Expand'}
          onClick={() => setOpen(!open)}
        >
          <Chevron className="chevron" open={open} />
        </button>
      </div>
      {open && (
        <div className="tool-detail">
          <ToolInput tool={tool} input={input} />
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
          {/* Unified, not the default split: a tool call's diff sits inline in
              the transcript, which is far too narrow for two columns. */}
          <DiffView diff={diff} layout="unified" />
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
