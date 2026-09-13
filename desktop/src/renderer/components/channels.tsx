import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { clearDraft, loadDraft, saveDraft } from '../drafts.ts'
import { statusOf } from '../errors.ts'
import { keys } from '../keys.ts'
import { channelMessagesQuery, channelsQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import { renderMarkdown } from '../markdown.ts'
import { AUTHOR_USER, authorColour, authorInitials } from './author-colour.ts'
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
          <span className="channel-name">{channelLabel(channel)}</span>
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

/** internal/store/channels.go — the channel every session is in. */
const GENERAL = 'general'

/**
 * A channel's name as a person reads it.
 *
 * An unnamed channel is its members, so it is shown by them rather than by the
 * id nobody chose — `ch_8f21a0` on a row says nothing about the conversation.
 */
export function channelLabel(channel: Channel): string {
  if (channel.name) return channel.name
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
    <ChannelConversation
      key={`${selection.hostId}:${selection.channelId}`}
      hostId={selection.hostId}
      hostName={host?.name ?? selection.hostId}
      channelId={selection.channelId}
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
  const client = useQueryClient()
  const { data: channels = [] } = useQuery(channelsQuery(hostId))
  const { data: messages = [] } = useQuery(channelMessagesQuery(hostId, channelId))
  const channel = channels.find((one) => one.id === channelId)

  const draftKey = `channel:${hostId}:${channelId}`
  const [draft, setDraft] = useState(() => loadDraft(draftKey))
  const [sending, setSending] = useState(false)
  const composer = useRef<HTMLTextAreaElement | null>(null)
  const scroller = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    saveDraft(draftKey, draft)
  }, [draftKey, draft])

  // The box takes the keyboard when a channel opens, as the transcript's does:
  // the reason for opening one is usually to say something in it.
  useEffect(() => {
    composer.current?.focus()
  }, [channelId])

  // Newest last, and the reader wants the end of it.
  useEffect(() => {
    const el = scroller.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages.length])

  const post = async (): Promise<void> => {
    const text = draft.trim()
    if (!text || sending) return
    setSending(true)
    try {
      await api(hostId).postToChannel(channelId, text)
      setDraft('')
      clearDraft(draftKey)
      await client.invalidateQueries({ queryKey: keys.channelMessages(hostId, channelId) })
      await client.invalidateQueries({ queryKey: keys.channels(hostId) })
    } catch (err) {
      store.fail(err)
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="channel">
      <header className="channel-head">
        <span className="channel-head-name">{channel ? channelLabel(channel) : channelId}</span>
        <span className="channel-head-members">
          {channel?.members.map((id) => (
            <button
              key={id}
              className="member-chip"
              title="Open this session"
              onClick={() => store.select(hostId, id)}
            >
              {channel.titles[id] ?? id}
            </button>
          ))}
          <span className="member-chip you">user</span>
        </span>
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
      <div className="composer">
        <div className="composer-input">
          <textarea
            ref={composer}
            value={draft}
            rows={1}
            placeholder="Message the channel (↵ to send, ⇧↵ for a new line)"
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return
              event.preventDefault()
              void post()
            }}
          />
          <div className="composer-bar">
            <button
              className="filled send-btn"
              disabled={!draft.trim() || sending}
              aria-label="Send to the channel"
              onClick={() => void post()}
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
}: {
  message: ChannelMessage
  opens: boolean
}): JSX.Element {
  const html = useMemo(() => renderMarkdown(message.body), [message.body])
  // The colour is the session's, carried on the row as a variable so the name,
  // the avatar and the bubble cannot disagree. The person gets none.
  const colour = authorColour(message.author)
  const mine = message.author === AUTHOR_USER

  return (
    <div
      className={[
        'channel-msg',
        mine ? 'you' : 'from-session',
        opens ? 'opens' : 'continues',
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
            <span className={mine ? 'channel-from you' : 'channel-from'}>{message.from}</span>
            {message.urgent && <span className="channel-urgent">urgent</span>}
            <span className="channel-when">{shortTime(message.created_at)}</span>
          </span>
        )}
        <div className="channel-msg-body md" dangerouslySetInnerHTML={{ __html: html }} />
      </div>
    </div>
  )
}

function shortTime(iso: string): string {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
