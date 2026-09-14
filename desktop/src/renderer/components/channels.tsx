import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { clearDraft, loadDraft, saveDraft } from '../drafts.ts'
import { statusOf } from '../errors.ts'
import { keys } from '../keys.ts'
import { channelMessagesQuery, channelThreadQuery, channelsQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import { renderMarkdown } from '../markdown.ts'
import { AUTHOR_USER, authorColour, authorInitials } from './author-colour.ts'
import { decorateMentions } from './mentions.ts'
import { SelectionMenu, type MenuAction } from './selection-menu.tsx'
import type { Channel, ChannelMessage } from '../../shared/models.ts'

/**
 * Channels: several sessions and the person, with one conversation running
 * through them. See docs/specs/60-group-chat.md.
 *
 * The list is grouped by host, as the session list is, because a channel
 * belongs to the daemon that holds it — its members are that daemon's sessions
 * and its messages never leave it.
 */

/** What the sidebar shows: one host's channels, with the closed ones put away. */
export function ChannelList({ hostId, name, showName }: {
  hostId: string
  name: string
  showName: boolean
}): JSX.Element | null {
  const { data: channels = [], error } = useQuery(channelsQuery(hostId))
  const selected = useStore((s) => s.channelSelection)
  const [menu, setMenu] = useState<{ channel: Channel; x: number; y: number } | null>(null)
  // Shut to begin with: the point of closing a conversation is not to be shown
  // it. Opened by hand when somebody wants to read one back.
  const [showClosed, setShowClosed] = useState(false)
  // Renamed on the row itself, as a group header is. Not through a dialog:
  // window.prompt throws in Electron, so an item that opened one would do
  // nothing at all.
  const [renaming, setRenaming] = useState<string | null>(null)

  const open = channels.filter((channel) => !channel.archived)
  const closed = channels.filter((channel) => channel.archived)

  // A daemon older than channels answers 404, and a host that cannot hold one
  // contributes nothing to the list rather than an error. The same rule the
  // session groups already follow, for the same reason: one out-of-date
  // machine in the sidebar must not break the feature on the others.
  if (statusOf(error) === 404) return null
  if (channels.length === 0) return null

  const row = (channel: Channel): JSX.Element => (
    <div
      key={channel.id}
      className={[
        'channel-row',
        channel.archived ? 'closed' : '',
        selected?.hostId === hostId && selected.channelId === channel.id ? 'active' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      onClick={() => store.selectChannel(hostId, channel.id)}
      onContextMenu={(event) => {
        event.preventDefault()
        setMenu({ channel, x: event.clientX, y: event.clientY })
      }}
    >
      <span className="channel-row-top">
        {renaming === channel.id ? (
          <ChannelNameField
            channel={channel}
            onDone={(name) => {
              setRenaming(null)
              if (name && name !== channel.name) void store.renameChannel(hostId, channel.id, name)
            }}
          />
        ) : (
          <span className={`channel-name${channel.name ? ' named' : ''}`}>
            {channelLabel(channel)}
          </span>
        )}
        {channel.mentions > 0 && !channel.archived && (
          <span className="badge mention" title="Somebody addressed you">
            @{channel.mentions}
          </span>
        )}
        {channel.unread > 0 && !channel.archived && <span className="badge">{channel.unread}</span>}
      </span>
      <span className="channel-row-sub">{memberSummary(channel)}</span>
    </div>
  )

  return (
    <div className="host-group">
      {showName && (
        <div className="host-head">
          <span className="host-title">
            <span className="host-name">{name}</span>
          </span>
        </div>
      )}
      {open.map(row)}

      {closed.length > 0 && (
        <>
          <button className="channel-closed-head" onClick={() => setShowClosed(!showClosed)}>
            <span className="channel-closed-mark">{showClosed ? '▾' : '▸'}</span>
            Closed ({closed.length})
          </button>
          {showClosed && closed.map(row)}
        </>
      )}

      {menu && (
        <SelectionMenu
          x={menu.x}
          y={menu.y}
          actions={channelActions(hostId, menu.channel, () => setRenaming(menu.channel.id))}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  )
}

/**
 * What can be done to a channel from its row.
 *
 * General gets none of it: every session is in it and it is the one channel
 * that is always there, so neither closing nor deleting it is somebody's to do.
 */
function channelActions(hostId: string, channel: Channel, rename: () => void): MenuAction[] {
  if (channel.id === GENERAL) {
    return [{ label: 'Everyone is in this one, always', disabled: true }]
  }

  return [
    {
      label: 'Rename',
      // Worth saying before the click rather than after: an unnamed channel is
      // found again by who is in it, and naming it ends that.
      title: channel.name
        ? undefined
        : 'Naming it means asking for these sessions again starts a new channel',
      run: rename,
    },
    {
      label: channel.archived ? 'Reopen' : 'Close',
      title: channel.archived
        ? 'Messages are delivered again'
        : 'It stays readable, but takes no more messages',
      run: () => void store.setChannelArchived(hostId, channel.id, !channel.archived),
    },
    {
      label: 'Delete',
      danger: true,
      run: () => {
        if (confirm(`Delete ${channelLabel(channel)}? Everything said in it goes too.`)) {
          void store.deleteChannel(hostId, channel.id)
        }
      },
    },
  ]
}

/**
 * The name, editable in place.
 *
 * Seeded with the name it has rather than what the row shows: an unnamed
 * channel is shown by its members, and offering that as the text to edit would
 * invite somebody to accept a name they never chose.
 */
function ChannelNameField({
  channel,
  onDone,
}: {
  channel: Channel
  onDone: (name: string | null) => void
}): JSX.Element {
  const [draft, setDraft] = useState(channel.name)

  return (
    <input
      autoFocus
      className="channel-name-field"
      value={draft}
      placeholder="Name this channel"
      aria-label="Channel name"
      onChange={(event) => setDraft(event.target.value)}
      onClick={(event) => event.stopPropagation()}
      onBlur={() => onDone(draft.trim() || null)}
      onKeyDown={(event) => {
        if (event.key === 'Enter') {
          event.preventDefault()
          onDone(draft.trim() || null)
        } else if (event.key === 'Escape') {
          event.preventDefault()
          onDone(null)
        }
      }}
    />
  )
}

/** The handles a channel answers to, for drawing chips in its messages. */
function useHandles(channel: Channel | undefined): Set<string> {
  const slugs = channel?.slugs
  return useMemo(() => new Set(Object.values(slugs ?? {}).concat('user')), [slugs])
}

/** internal/store/channels.go — the channel every session is in. */
const GENERAL = 'general'

/**
 * A channel's name as a person reads it.
 *
 * An unnamed channel is its members, so it is shown by them rather than by the
 * id nobody chose — `ch_8f21a0` on a row says nothing about the conversation.
 *
 * A name carries a `#`, the mark every chat tool uses for a channel. The member
 * list does not: `#Alpha, Beta +2` reads as a name somebody chose.
 */
export function channelLabel(channel: Channel): string {
  if (channel.name) return `#${channel.name}`
  const titles = channel.members.map((id) => channel.titles[id] ?? id)
  if (titles.length === 0) return channel.id
  if (titles.length <= 2) return titles.join(', ')
  return `${titles.slice(0, 2).join(', ')} +${titles.length - 2}`
}

function memberSummary(channel: Channel): string {
  const count = channel.members.length
  if (channel.archived) return `closed · ${count} ${count === 1 ? 'session' : 'sessions'}`
  // General's members are every session on the daemon, so it is the count that
  // says something, not the list — and it says nothing about being invited.
  if (channel.id === GENERAL) {
    return `everyone · ${count} ${count === 1 ? 'session' : 'sessions'}, and you`
  }
  return `${count} ${count === 1 ? 'session' : 'sessions'}, and you`
}

/** The conversation, and the box to add to it. */
export function ChannelPanel(): JSX.Element {
  const selection = useStore((s) => s.channelSelection)
  const hosts = useStore((s) => s.hosts)

  if (!selection) {
    return (
      <div className="panel-empty">
        <p>Pick a channel, or start one from a few sessions.</p>
      </div>
    )
  }
  const host = hosts.find((one) => one.id === selection.hostId)
  return (
    <div className="channel-with-thread">
      <ChannelConversation
        key={`${selection.hostId}:${selection.channelId}`}
        hostId={selection.hostId}
        hostName={host?.name ?? selection.hostId}
        channelId={selection.channelId}
      />
      <ChannelAside />
    </div>
  )
}

/** Whichever panel is open beside the conversation: a thread, or the members. */
function ChannelAside(): JSX.Element | null {
  const selection = useStore((s) => s.channelSelection)
  const thread = useStore((s) => s.threadSelection)
  const membersOpen = useStore((s) => s.membersOpen)
  if (!selection) return null

  if (membersOpen) {
    return (
      <MembersPanel
        key={`${selection.hostId}:${selection.channelId}:members`}
        hostId={selection.hostId}
        channelId={selection.channelId}
      />
    )
  }

  if (!thread) return null
  if (thread.hostId !== selection.hostId || thread.channelId !== selection.channelId) return null
  return (
    <ThreadPanel
      key={`${thread.hostId}:${thread.channelId}:${thread.root}`}
      hostId={thread.hostId}
      channelId={thread.channelId}
      root={thread.root}
    />
  )
}

function ChannelConversation({
  hostId,
  channelId,
}: {
  hostId: string
  hostName: string
  channelId: string
}): JSX.Element {
  const { data: channels = [] } = useQuery(channelsQuery(hostId))
  const { data: messages = [] } = useQuery(channelMessagesQuery(hostId, channelId))
  const channel = channels.find((one) => one.id === channelId)
  const handles = useHandles(channel)
  const scroller = useRef<HTMLDivElement | null>(null)

  // Newest last, and the reader wants the end of it.
  useEffect(() => {
    const el = scroller.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages.length])

  return (
    <div className="channel">
      {/* A count and a button, not every member spelled out. Four sessions
          with 48-character titles wrapped the header onto three lines and
          pushed the conversation down the screen. */}
      <header className="channel-head">
        <span className="channel-head-name">{channel ? channelLabel(channel) : channelId}</span>
        <span className="grow" />
        <button
          className="channel-members-toggle"
          aria-label="Show who is in this channel"
          onClick={() => store.toggleMembers()}
        >
          {memberCount(channel)}
        </button>
      </header>

      {/* General behaves differently from the channel above it in the list, and
          the difference is invisible until somebody posts and nothing happens. */}
      {channelId === GENERAL && (
        <p className="channel-note">
          The notice board. Every session is in it, and posting here interrupts nobody —
          agents read it when they look.
        </p>
      )}

      <div className="channel-scroll" ref={scroller}>
        {messages.length === 0 && <p className="empty-note">Nothing said yet.</p>}
        {messages.map((message, at) => (
          <ChannelMessageRow
            key={message.id}
            message={message}
            opens={messages[at - 1]?.author !== message.author}
            handles={handles}
            onOpenThread={() => store.openThread(hostId, channelId, message.id)}
          />
        ))}
      </div>

      {/* Closed is read-only, so the box goes rather than being disabled: a
          composer that takes text and then refuses it is worse than none. */}
      {channel?.archived && (
        <div className="channel-closed-note">
          <span>This channel is closed. It takes no more messages.</span>
          <button className="ghost" onClick={() => void store.setChannelArchived(hostId, channelId, false)}>
            Reopen
          </button>
        </div>
      )}

      {!channel?.archived && (
        <ChannelComposer
          hostId={hostId}
          channelId={channelId}
          members={channel?.members ?? []}
          titles={channel?.titles ?? {}}
          slugs={channel?.slugs ?? {}}
          placeholder="Message the channel (↵ to send, ⇧↵ for a new line)"
          autoFocus
        />
      )}
    </div>
  )
}

/**
 * The thread hanging off one message, beside the conversation rather than
 * inside it.
 *
 * Its own composer and its own draft: what you were part-way through saying to
 * the channel is not what you were part-way through saying in here.
 */
function ThreadPanel({
  hostId,
  channelId,
  root,
}: {
  hostId: string
  channelId: string
  root: string
}): JSX.Element {
  const { data: channels = [] } = useQuery(channelsQuery(hostId))
  const { data: messages = [] } = useQuery(channelThreadQuery(hostId, channelId, root))
  const channel = channels.find((one) => one.id === channelId)
  const handles = useHandles(channel)
  const scroller = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const el = scroller.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages.length])

  return (
    <aside className="channel-thread">
      <header className="channel-head">
        <span className="channel-head-name">Thread</span>
        <span className="grow" />
        <button className="ghost" aria-label="Close the thread" onClick={() => store.closeThread()}>
          ✕
        </button>
      </header>

      <div className="channel-scroll" ref={scroller}>
        {messages.map((message, at) => (
          <ChannelMessageRow
            key={message.id}
            message={message}
            opens={messages[at - 1]?.author !== message.author}
            handles={handles}
          />
        ))}
      </div>

      {!channel?.archived && (
        <ChannelComposer
          hostId={hostId}
          channelId={channelId}
          threadRoot={root}
          members={channel?.members ?? []}
          titles={channel?.titles ?? {}}
          slugs={channel?.slugs ?? {}}
          placeholder="Reply in the thread (↵ to send)"
          autoFocus
        />
      )}
    </aside>
  )
}

/**
 * Who is in the channel, in the panel the thread uses.
 *
 * A list rather than a row of chips in the header: the handle belongs beside
 * the name it stands for, and a session's title is far too long to put four of
 * them on one line.
 */
function MembersPanel({ hostId, channelId }: { hostId: string; channelId: string }): JSX.Element {
  const { data: channels = [] } = useQuery(channelsQuery(hostId))
  const channel = channels.find((one) => one.id === channelId)

  return (
    <aside className="channel-thread">
      <header className="channel-head">
        <span className="channel-head-name">{memberCount(channel)}</span>
        <span className="grow" />
        <button className="ghost" aria-label="Close the member list" onClick={() => store.toggleMembers()}>
          ✕
        </button>
      </header>

      <div className="member-list">
        {channel?.members.map((id) => (
          <button
            key={id}
            className="member-chip"
            title="Open this session"
            onClick={() => store.select(hostId, id)}
          >
            <span className="channel-avatar" style={{ color: authorColour(`session:${id}`) }}>
              {authorInitials(channel.titles[id] ?? id)}
            </span>
            <span className="member-of">
              <span className="member-name">{channel.titles[id] ?? id}</span>
              <span className="member-handle">@{channel.slugs?.[id] ?? id.slice(0, 8)}</span>
            </span>
          </button>
        ))}
        <span className="member-chip you">
          <span className="channel-avatar">you</span>
          <span className="member-of">
            <span className="member-name">user</span>
            <span className="member-handle">@user</span>
          </span>
        </span>
      </div>
    </aside>
  )
}

/**
 * The box, and the @ menu it opens.
 *
 * One component for the channel and for a thread, because the only difference
 * between them is where the message lands — and the draft key, which has to
 * differ or a half-typed reply would appear in the channel's box.
 */
function ChannelComposer({
  hostId,
  channelId,
  threadRoot = '',
  members,
  titles,
  slugs,
  placeholder,
  autoFocus = false,
}: {
  hostId: string
  channelId: string
  threadRoot?: string
  members: string[]
  titles: Record<string, string>
  slugs: Record<string, string>
  placeholder: string
  autoFocus?: boolean
}): JSX.Element {
  const client = useQueryClient()
  const draftKey = threadRoot
    ? `thread:${hostId}:${channelId}:${threadRoot}`
    : `channel:${hostId}:${channelId}`
  const [draft, setDraft] = useState(() => loadDraft(draftKey))
  const [sending, setSending] = useState(false)
  const [picking, setPicking] = useState<string | null>(null)
  const box = useRef<HTMLTextAreaElement | null>(null)
  // State is not true until React re-renders, so two Enters in one tick both
  // read it as false and post the message twice. A ref changes immediately.
  const inFlight = useRef(false)

  useEffect(() => {
    saveDraft(draftKey, draft)
  }, [draftKey, draft])

  useEffect(() => {
    if (autoFocus) box.current?.focus()
  }, [autoFocus, draftKey])

  const post = async (): Promise<void> => {
    const text = draft.trim()
    if (!text || inFlight.current) return
    inFlight.current = true
    setSending(true)
    try {
      await api(hostId).postToChannel(channelId, text, false, threadRoot)
      setDraft('')
      clearDraft(draftKey)
      await client.invalidateQueries({ queryKey: keys.channelMessages(hostId, channelId) })
      if (threadRoot) {
        await client.invalidateQueries({
          queryKey: keys.channelThread(hostId, channelId, threadRoot),
        })
      }
      await client.invalidateQueries({ queryKey: keys.channels(hostId) })
    } catch (err) {
      store.fail(err)
    } finally {
      inFlight.current = false
      setSending(false)
    }
  }

  // The handle is what the daemon resolves, so the menu inserts that rather
  // than the title: what reads well and what addresses somebody are different
  // strings, and only one of them wakes an agent.
  const offered = members
    .map((id) => ({ id, handle: slugs[id] ?? id.slice(0, 8), title: titles[id] ?? id }))
    .filter(({ handle, title }) => {
      const needle = (picking ?? '').toLowerCase()
      return !needle || handle.includes(needle) || title.toLowerCase().includes(needle)
    })

  const insert = (handle: string): void => {
    setDraft((text) => text.replace(/@([A-Za-z0-9_-]*)$/, `@${handle} `))
    setPicking(null)
    box.current?.focus()
  }

  return (
    <div className="composer">
      {picking !== null && offered.length > 0 && (
        <div className="mention-menu">
          {offered.map(({ id, handle, title }) => (
            <button key={id} className="mention-option" onMouseDown={() => insert(handle)}>
              <span className="mention-handle" style={{ color: authorColour(`session:${id}`) }}>
                @{handle}
              </span>
              <span className="mention-title">{title}</span>
            </button>
          ))}
        </div>
      )}

      <div className="composer-input">
        <textarea
          ref={box}
          value={draft}
          rows={1}
          placeholder={placeholder}
          onChange={(event) => {
            const text = event.target.value
            setDraft(text)
            // Open on the @ and narrow as it is typed; any space ends it.
            const at = /@([A-Za-z0-9_-]*)$/.exec(text)
            setPicking(at ? (at[1] ?? '') : null)
          }}
          onBlur={() => setPicking(null)}
          onKeyDown={(event) => {
            if (event.key === 'Escape' && picking !== null) {
              event.preventDefault()
              setPicking(null)
              return
            }
            if (event.key === 'Tab' && picking !== null && offered[0]) {
              event.preventDefault()
              insert(offered[0].handle)
              return
            }
            if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return
            event.preventDefault()
            if (picking !== null && offered[0]) {
              insert(offered[0].handle)
              return
            }
            void post()
          }}
        />
        <div className="composer-bar">
          <button
            className="filled send-btn"
            disabled={!draft.trim() || sending}
            aria-label={threadRoot ? 'Send to the thread' : 'Send to the channel'}
            onClick={() => void post()}
          >
            {sending ? <span className="spinner" /> : '↑'}
          </button>
        </div>
      </div>
    </div>
  )
}

/**
 * One message, as a chat shows it.
 *
 * `opens` is whether this starts a run from a new author. A run shares one
 * header and one avatar: four messages from the same agent repeating its title
 * four times is the noise that made a busy channel hard to read, and the
 * repetition says nothing the first line did not.
 */
function ChannelMessageRow({
  message,
  opens,
  handles,
  onOpenThread,
}: {
  message: ChannelMessage
  opens: boolean
  /** The handles this channel answers to, for drawing the chips. */
  handles: Set<string>
  /** Absent inside a thread: everything there is already in one. */
  onOpenThread?: () => void
}): JSX.Element {
  const html = useMemo(
    () => decorateMentions(renderMarkdown(message.body), handles),
    [message.body, handles],
  )
  // The colour is the session's, carried on the row as a variable so the name,
  // the avatar and the bubble cannot disagree. The person gets none.
  const colour = authorColour(message.author)
  const mine = message.author === AUTHOR_USER
  // Addressed to the person reading it, which is the one thing in a channel
  // worth finding again when scrolling back.
  const addressed = (message.mentions ?? []).includes(AUTHOR_USER)

  return (
    <div
      className={[
        'channel-msg',
        mine ? 'you' : 'from-session',
        opens ? 'opens' : 'continues',
        addressed ? 'addressed' : '',
        message.urgent ? 'urgent' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      style={colour ? ({ '--author': colour } as CSSProperties) : undefined}
    >
      {/* A slot even when empty, so the bubbles of one run stay in a column
          rather than stepping left under the first. */}
      <span className="channel-avatar" aria-hidden={!opens}>
        {opens && !mine ? authorInitials(message.from) : ''}
      </span>

      <div className="channel-bubble">
        {opens && (
          <span className="channel-msg-head">
              <span
              className={mine ? 'channel-from you' : 'channel-from'}
              title={message.from}
            >
              {message.from}
            </span>
            {message.urgent && <span className="channel-urgent">urgent</span>}
            <span className="channel-when">{shortTime(message.created_at)}</span>
          </span>
        )}
        <div className="channel-msg-body md" dangerouslySetInnerHTML={{ __html: html }} />

        {onOpenThread && (message.reply_count ?? 0) > 0 && (
          <button className="channel-replies" onClick={onOpenThread} title={replyLine(message)}>
            {replyLine(message)}
          </button>
        )}
      </div>
    </div>
  )
}

/** "4 members" — the person counts, because they are in the conversation. */
function memberCount(channel: Channel | undefined): string {
  const count = (channel?.members.length ?? 0) + 1
  return `${count} ${count === 1 ? 'member' : 'members'}`
}

function replyLine(message: ChannelMessage): string {
  const count = message.reply_count ?? 0
  const who = (message.reply_authors ?? []).join(", ")
  const replies = count === 1 ? "1 reply" : `${count} replies`
  return who ? `${replies} · ${who}` : replies
}

function shortTime(iso: string): string {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
