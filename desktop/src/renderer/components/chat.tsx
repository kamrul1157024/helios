import { useEffect, useLayoutEffect, useMemo, useRef, useState, type RefObject } from 'react'

import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, statusOf } from '../bridge.ts'
import { keys } from '../keys.ts'
import { appendDelta, fileContentQuery, transcriptMessages, transcriptQuery } from '../queries.ts'
import { removeFirst } from '../attachments.ts'
import { AttachButton, AttachmentChips, PasteOffer, useAttachments, useDropTarget } from './attach.tsx'
import { multiEditDiff, unifiedDiff } from '../diff.ts'
import { hunkHeader, lineOf } from './edit-offsets.ts'
import { DiffView } from './diff-view.tsx'
import { foldedCommand, followsItsCall, headline, oneLine, resultOf } from './tool-calls.ts'
import { groupRuns, runSucceeded, summariseTurn } from './tool-runs.ts'
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
import { sessionKey, store, useStore } from '../store.ts'
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
  /**
   * The last few calls, by their place in the list.
   *
   * A card reads this once, when it mounts. Recomputing it as the transcript
   * grows would close a card somebody is reading, so what it decides is only
   * ever the state a new card starts in.
   */
  const renderMessage = (index: number): JSX.Element | null => {
    const message = messages[index]
    // A result that followed its own call is drawn on that call's row, so it
    // does not get a line of its own here.
    if (!message || followsItsCall(messages, index)) return null
    return (
      <Message
        key={`${message.timestamp}-${index}`}
        message={message}
        result={resultOf(messages, index)}
        recent={recentCalls.has(index)}
        folded={folded}
        hostId={hostId}
        cwd={session.cwd}
      />
    )
  }

  // Set by the button beside the tab, and remembered for this session.
  const folded = useStore((s) => s.foldModes[sessionKey(hostId, session.session_id)]) === 'folded'
  const recentCalls = useMemo(() => {
    const calls = messages.reduce<number[]>((held, message, index) => {
      if (message.role === 'tool_use') held.push(index)
      return held
    }, [])
    return new Set(calls.slice(-RECENT_CALLS))
  }, [messages])
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
  // Runs of shell commands, drawn as one row each. The one still going is left
  // as it is: that is the one being watched.
  const items = useMemo(() => groupRuns(messages, busy), [messages, busy])
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

  // One line until there is more than one line to show. Measured rather than
  // counted: the box's width decides where the text wraps, and a newline is
  // not the only thing that starts a row. The cap is in the stylesheet, so
  // past it the textarea scrolls instead of eating the transcript.
  useEffect(() => {
    const el = composer.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${el.scrollHeight}px`
  }, [draft])

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
      const message = await files.store(hostId, text)

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
    // The whole panel takes a drop, not just the box at the foot of it: a file
    // dragged at a conversation is meant for the conversation, and aiming for
    // a 40px strip is a hit test the reader should not have to pass.
    <div className={dropping ? 'chat dropping' : 'chat'} {...dropHandlers}>
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
            {items.map((item) =>
              item.kind === 'turn' ? (
                <TurnRow
                  key={`turn-${item.indices[0]}`}
                  messages={messages}
                  indices={item.indices}
                  hostId={hostId}
                  cwd={session.cwd}
                />
              ) : (
                renderMessage(item.index)
              ),
            )}
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
        <div className="composer">
          {files.pasted !== null && draft.includes(files.pasted) && (
            <PasteOffer text={files.pasted} onFile={fileThePaste} onKeep={files.keepThePaste} />
          )}

          <AttachmentChips files={files.files} onRemove={files.remove} />

          <div className="composer-input">
            <textarea
              ref={composer}
              value={draft}
              rows={1}
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
  /** How the call went, for a tool_use whose result came next. */
  result?: boolean
  /** Near the end of the transcript, where a write opens itself. */
  recent?: boolean
  /** The session is being read folded, so nothing opens itself. */
  folded?: boolean
}

/**
 * One transcript entry. The roles are the daemon's
 * (internal/transcript/reader.go): user, assistant, tool_use, tool_result.
 */
function Message({ message, hostId, cwd, result, recent, folded }: MessageProps): JSX.Element | null {
  switch (message.role) {
    case 'tool_use':
      return (
        <ToolUse
          message={message}
          hostId={hostId}
          cwd={cwd}
          result={result}
          recent={recent}
          folded={folded}
        />
      )
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
 * How many tool calls back a write is still worth opening on its own.
 *
 * Counted in calls rather than messages: a write is followed by its own
 * result, then a line of the agent's prose, then the next call — so a window
 * measured in messages closes on the patch the reader is still looking at.
 *
 * The diff an agent has just written is what the reader came for; the twenty
 * before it are history, and a transcript that opens all of them is a page of
 * patches with the conversation lost between them.
 */
const RECENT_CALLS = 3

/**
 * How many lines the clamp is hiding, or 0 when it is hiding none.
 *
 * Measured rather than counted: what a line is depends on the width of the
 * panel and the font in it, neither of which this knows. Re-measured on resize
 * for the same reason.
 */
function useHiddenLines(text: string, open: boolean): [RefObject<HTMLSpanElement>, number] {
  const ref = useRef<HTMLSpanElement>(null)
  const [hidden, setHidden] = useState(0)

  useLayoutEffect(() => {
    const el = ref.current
    if (!el || open) {
      setHidden(0)
      return
    }
    const measure = (): void => {
      const line = parseFloat(getComputedStyle(el).lineHeight) || 1
      setHidden(Math.max(0, Math.round((el.scrollHeight - el.clientHeight) / line)))
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [text, open])

  return [ref, hidden]
}

/**
 * A turn's tool calls as one row, opening to the rows it stands for.
 *
 * Folded whatever the session's mode is: a grouped turn is history by
 * definition — the one still running is never grouped — so there is nothing
 * here the reader has asked to see yet.
 */
function TurnRow({
  messages,
  indices,
  hostId,
  cwd,
}: {
  messages: TranscriptMessage[]
  indices: number[]
  hostId: string
  cwd: string
}): JSX.Element {
  const [open, setOpen] = useState(false)
  const ok = runSucceeded(messages, indices)
  // Fold all reaches a turn as it reaches a card: "open every tool call" that
  // left the groups shut would have opened nothing a reader could see.
  const foldAll = useStore((s) => s.foldAll)
  const answered = useRef(foldAll.seq)
  useEffect(() => {
    if (foldAll.seq === answered.current) return
    answered.current = foldAll.seq
    setOpen(foldAll.open)
  }, [foldAll.seq, foldAll.open])

  return (
    <div className="msg tool-turn">
      <div
        className={open ? 'tool-head open' : 'tool-head'}
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return
          event.preventDefault()
          setOpen(!open)
        }}
      >
        <span className="tool-icon">◈</span>
        <span className="tool-name">
          {indices.length} {indices.length === 1 ? 'step' : 'steps'}
        </span>
        <span className="tool-summary">{summariseTurn(messages, indices)}</span>
        <span className="grow" />
        <span className={ok ? 'tool-verdict' : 'tool-verdict failed'}>{ok ? '✓' : '✕'}</span>
        <Chevron className="chevron" open={open} />
      </div>
      {open && (
        <div className="turn-members">
          {indices.map((index) => {
            const message = messages[index]
            if (!message) return null
            return (
              <Message
                key={`${message.timestamp}-${index}`}
                message={message}
                result={resultOf(messages, index)}
                recent={false}
                folded
                hostId={hostId}
                cwd={cwd}
              />
            )
          })}
        </div>
      )}
    </div>
  )
}

/**
 * A tool call: what it ran, and what it ran on.
 *
 * The command is shown in full and wrapped rather than cut at the width of the
 * panel — reading which of two similar commands this was is the whole use of
 * the row. Only the overflow past a few lines folds, and it says how much it is
 * holding back, as the terminal does.
 */
function ToolUse({
  message,
  hostId,
  cwd,
  result,
  recent = true,
  folded = false,
}: MessageProps): JSX.Element {
  const tool = message.tool ?? 'tool'
  const input = (message.metadata ?? {}) as Record<string, unknown>
  const filePath = typeof input.file_path === 'string' ? input.file_path : null
  // A write is the part of a session worth reading, and a collapsed row names
  // the tool without saying what it did to the file.
  // Only what the agent has just done opens itself, and not even that while
  // the session is being read folded — that is a standing instruction, not a
  // press that expires. A card mounted open stays open as the transcript grows
  // past it: shutting one under a reader mid-diff is worse than the rule.
  const [open, setOpen] = useState(!folded && WRITING_TOOLS.has(tool) && recent)
  // Fold all, from the strip above. Keyed on the counter so a card opened by
  // hand since the last press is reached by the next one.
  const foldAll = useStore((s) => s.foldAll)
  // The press this card has already answered. Seeded with whatever the counter
  // stands at when the card mounts: a card that arrives after a Fold all is
  // new work, and folding it because of a press that happened before it
  // existed is how every later write came to arrive shut.
  const answered = useRef(foldAll.seq)
  useEffect(() => {
    if (foldAll.seq === answered.current) return
    answered.current = foldAll.seq
    setOpen(foldAll.open)
  }, [foldAll.seq, foldAll.open])
  const text = headline(tool, input, message.summary)
  const [summaryRef, clamped] = useHiddenLines(text, open)
  // A described row hides the command itself, not the rest of a line of it, so
  // the count is the command's length rather than what the clamp cut.
  const command = foldedCommand(tool, input)
  const commandLines = command ? command.trim().split('\n').length : 0
  const hidden = open ? 0 : commandLines > 1 ? commandLines : clamped

  return (
    <div className="msg tool-call">
      <div
        className={open ? 'tool-head open' : 'tool-head'}
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return
          event.preventDefault()
          setOpen(!open)
        }}
      >
        <span className="tool-icon">{TOOL_ICONS[tool] ?? '⚙'}</span>
        <span className="tool-name">{tool}</span>
        <span className="tool-summary" ref={summaryRef}>
          {text}
        </span>
        {/* Beside the name, not at the end of the row: it opens that file, and
            an arrow an inch away from what it acts on reads as belonging to
            the row's own controls. */}
        {filePath && (
          <button
            className="tool-open"
            title={`Open ${resolveFilePath(filePath, cwd)}`}
            aria-label={`Open ${filePath.split('/').pop() ?? filePath}`}
            onClick={(event) => {
              // The row behind this expands; opening the file is not that.
              event.stopPropagation()
              store.openFile(hostId, resolveFilePath(filePath, cwd))
            }}
          >
            ↗
          </button>
        )}
        <span className="grow" />
        {hidden > 0 && !open && (
          <span className="tool-more">
            +{hidden} {hidden === 1 ? 'line' : 'lines'}
          </span>
        )}
        {result !== undefined && (
          <span
            className={result ? 'tool-verdict' : 'tool-verdict failed'}
            title={result ? 'The call succeeded' : 'The call failed'}
          >
            {result ? '✓' : '✕'}
          </span>
        )}
        <Chevron className="chevron" open={open} />
      </div>

      {open && (
        <div className="tool-detail">
          <ToolInput tool={tool} input={input} hostId={hostId} cwd={cwd} />
        </div>
      )}
    </div>
  )
}

function ToolInput({
  tool,
  input,
  hostId,
  cwd,
}: {
  tool: string
  input: Record<string, unknown>
  hostId: string
  cwd: string
}): JSX.Element {
  // file_path is the row's own text, and the arrow on the row opens it, so a
  // field repeating the path is the same string twice with a label on one.
  const entries = Object.entries(input).filter(([key]) => key !== 'file_path')
  if (entries.length === 0) return <p className="tool-empty">No input recorded.</p>

  // The command, but only when the row above is showing a description instead
  // of it. Without one the row is already the command, and opening it unclamps
  // what was cut rather than printing it twice.
  if (tool === 'Bash' || tool === 'BashOutput') {
    const command = foldedCommand(tool, input)
    const rest = entries.filter(
      ([key]) => key !== 'command' && key !== 'cmd' && !(command && key === 'description'),
    )
    if (!command && rest.length === 0) return <p className="tool-empty">Nothing else was passed.</p>
    return (
      <>
        {command && <CodeBlock code={command} language="bash" />}
        <KeyValues entries={rest} />
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
        <ToolDiff diff={diff} input={input} hostId={hostId} cwd={cwd} />
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

/**
 * The patch, numbered against the file it was written to where that can be
 * settled.
 *
 * The call carries no offsets, so the numbers come from finding the written
 * text in the file itself — one cached read per path, shared with the Files
 * panel. When the file has moved on, or the text sits in it twice, the patch is
 * drawn unnumbered rather than numbered from a guess.
 */
function ToolDiff({
  diff,
  input,
  hostId,
  cwd,
}: {
  diff: string
  input: Record<string, unknown>
  hostId: string
  cwd: string
}): JSX.Element {
  // How much of a patch this reader wants inline, from Settings. Enough to see
  // what the change was; the rest is one press away, and the whole file is one
  // press further through the arrow on the row.
  const maxLines = useStore((s) => s.diffLines)
  const path = resolveFilePath(str(input.file_path), cwd)
  const written = str(input.new_string)
  // A Write is the whole file, so it starts where files start. Only an Edit
  // has to be found. A MultiEdit's edits sit at several places at once, and one
  // header cannot describe them.
  const whole = Boolean(str(input.content) || str(input.new_content))
  const file = useQuery({
    ...fileContentQuery(hostId, path),
    enabled: path !== '' && written !== '' && !whole,
  })

  const numbered = useMemo(() => {
    if (whole) return `${hunkHeader(diff, 1)}
${diff}`
    const content = file.data && file.data.encoding !== 'base64' ? file.data.content : ''
    const header = hunkHeader(diff, content ? lineOf(content, written) : null)
    return header ? `${header}
${diff}` : diff
  }, [diff, file.data, written, whole])

  return (
    <div className="tool-diff">
      {/* Unified, not the default split: a tool call's diff sits inline in the
          transcript, which is far too narrow for two columns. */}
      <DiffView
        diff={numbered}
        language={languageForPath(str(input.file_path))}
        layout="unified"
        maxLines={maxLines}
      />
    </div>
  )
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
