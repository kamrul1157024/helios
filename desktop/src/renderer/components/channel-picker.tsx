import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { statusOf } from '../errors.ts'
import { channelsQuery } from '../queries.ts'
import { store, useStore } from '../store.ts'
import { channelLabel } from './channels.tsx'
import type { Channel } from '../../shared/models.ts'

/**
 * Which channel these sessions should go into — and, if none of them, a new
 * one named by whatever was typed.
 *
 * A search field rather than a submenu of channels. A submenu is fine at three
 * and unusable at thirty, and the row that creates one would sit under a list
 * long enough to have scrolled it off the screen. Here one control does both:
 * type to narrow, and a query that matches nothing becomes the name of a new
 * channel. Modelled on ⌘P, whose markup it reuses.
 */
export function ChannelPicker(): JSX.Element | null {
  const picker = useStore((s) => s.channelPicker)
  if (!picker) return null
  return <Picker hostId={picker.hostId} sessions={picker.sessions} />
}

function Picker({ hostId, sessions }: { hostId: string; sessions: string[] }): JSX.Element {
  const { data: channels = [], error } = useQuery(channelsQuery(hostId))
  const [query, setQuery] = useState('')
  // Optional, and worth offering here rather than after: a channel opened with
  // the question that prompted it saves its members a round trip, and the
  // joining prompt carries it so nobody has to go and fetch it.
  const [opening, setOpening] = useState('')
  const [active, setActive] = useState(0)
  const list = useRef<HTMLDivElement | null>(null)

  // A closed channel is not somewhere to put anybody: the daemon refuses the
  // join, so offering it would be offering an error.
  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const open = channels.filter((channel) => !channel.archived)
    if (!needle) return open
    return open.filter((channel) => haystack(channel).includes(needle))
  }, [channels, query])

  // Creating is a row of its own at the end, not a separate button: it is
  // where the eye already is after typing a name that matched nothing.
  const naming = query.trim()
  const exact = matches.some((channel) => channel.name.toLowerCase() === naming.toLowerCase())
  // A daemon older than channels answers 404. Offering to make one on a
  // machine that cannot hold it would be offering a button that fails.
  const unsupported = statusOf(error) === 404
  const rows = exact || unsupported ? matches.length : matches.length + 1

  useEffect(() => {
    setActive(0)
  }, [query])

  useEffect(() => {
    list.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest' })
  }, [active])

  const take = (index: number): void => {
    const channel = matches[index]
    if (channel) void store.addToChannel(hostId, channel.id, sessions, opening)
    else void store.startChannel(hostId, sessions, naming, opening)
  }

  const keys = (event: React.KeyboardEvent): void => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActive((at) => Math.min(at + 1, rows - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActive((at) => Math.max(at - 1, 0))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      take(active)
    } else if (event.key === 'Escape') {
      event.preventDefault()
      store.closeChannelPicker()
    }
  }

  return (
    <div className="quick-backdrop" onMouseDown={() => store.closeChannelPicker()}>
      <div className="quick" onMouseDown={(event) => event.stopPropagation()}>
        <input
          autoFocus
          className="quick-input"
          placeholder={
            sessions.length === 1
              ? 'Add to a channel, or name a new one…'
              : `Add ${sessions.length} sessions to a channel, or name a new one…`
          }
          spellCheck={false}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={keys}
        />
        <div className="quick-list" ref={list}>
          {matches.map((channel, index) => (
            <button
              key={channel.id}
              className={`quick-row ${index === active ? 'active' : ''}`}
              onMouseEnter={() => setActive(index)}
              onClick={() => take(index)}
            >
              <span className="quick-name">{channelLabel(channel)}</span>
              <span className="quick-dir">{memberCount(channel)}</span>
            </button>
          ))}

          {unsupported && (
            <p className="empty-note">
              This machine is running a Helios without channels. Update it to start one here.
            </p>
          )}

          {!exact && !unsupported && (
            <button
              className={`quick-row new-channel ${active === matches.length ? 'active' : ''}`}
              onMouseEnter={() => setActive(matches.length)}
              onClick={() => take(matches.length)}
            >
              <span className="quick-name">
                {naming ? `New channel “${naming}”` : 'New channel'}
              </span>
              {/* Without a name the daemon's rule applies: the same sessions
                  asked for twice are the same conversation, not two. */}
              <span className="quick-dir">{naming ? '' : 'named by its members'}</span>
            </button>
          )}
        </div>

        {/* Below the list, because which channel comes first and what to say
            comes second. Enter in here sends rather than picking a row. */}
        <textarea
          className="quick-message"
          rows={2}
          placeholder="Say something to open with (optional)"
          value={opening}
          onChange={(event) => setOpening(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              event.preventDefault()
              store.closeChannelPicker()
              return
            }
            if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return
            event.preventDefault()
            take(active)
          }}
        />
      </div>
    </div>
  )
}

/** A channel is findable by its name and by who is in it: somebody looking for
 *  "Port the client" is looking for the channel that session is in. */
function haystack(channel: Channel): string {
  return [channel.name, ...channel.members.map((id) => channel.titles[id] ?? id)]
    .join(' ')
    .toLowerCase()
}

function memberCount(channel: Channel): string {
  const count = channel.members.length
  return `${count} ${count === 1 ? 'session' : 'sessions'}`
}
