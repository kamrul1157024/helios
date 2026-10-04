import type { ChannelMessage } from '../shared/models.ts'

/**
 * A bounded window over a channel's spine, oldest first.
 *
 * A channel opens on the newest page and pages back from there. The window is
 * held to {@link CHANNEL_MAX_HELD}: scrolling up drops the newest page off the
 * bottom to make room for older, and scrolling back down reloads it. So however
 * long a conversation runs, only a fixed slice is ever held.
 *
 * The transforms are pure so the windowing can be asserted without a DOM: the
 * component owns the scroll position, this owns what is held and whether more
 * exists. See docs/specs/60-group-chat.md.
 */

/** The page shown on open, and the size of every page fetched after. */
export const CHANNEL_PAGE_SIZE = 20

/** The most messages held at once. Past this, a page in one end drops one off the other. */
export const CHANNEL_MAX_HELD = 200

export interface ChannelWindow {
  messages: ChannelMessage[]
  /** Older messages sit above the top of what is held. */
  hasOlder: boolean
  /** Newer messages were dropped off the bottom; scrolling back down reloads them. */
  hasNewer: boolean
}

export const EMPTY_WINDOW: ChannelWindow = { messages: [], hasOlder: false, hasNewer: false }

/** The window a newest page opens onto: a short page is the whole start. */
export function openingWindow(newest: ChannelMessage[]): ChannelWindow {
  return { messages: newest, hasOlder: newest.length >= CHANNEL_PAGE_SIZE, hasNewer: false }
}

/** Prepends an older page. May carry the window past its cap; {@link trimmedNewest} brings it back. */
export function withOlder(held: ChannelWindow, older: ChannelMessage[]): ChannelWindow {
  if (older.length === 0) return held
  return {
    messages: [...older, ...held.messages],
    hasOlder: older.length >= CHANNEL_PAGE_SIZE,
    hasNewer: held.hasNewer,
  }
}

/** How many messages are held over the cap and want dropping. */
export function overflowOf(held: ChannelWindow): number {
  const over = held.messages.length - CHANNEL_MAX_HELD
  return over > 0 ? over : 0
}

/** Drops the newest `count` messages to hold the window's size, reversibly. */
export function trimmedNewest(held: ChannelWindow, count: number): ChannelWindow {
  if (count <= 0) return held
  return {
    messages: held.messages.slice(0, held.messages.length - count),
    hasOlder: held.hasOlder,
    hasNewer: true,
  }
}

/** Appends a newer page and drops the oldest past the cap. */
export function withNewer(held: ChannelWindow, newer: ChannelMessage[]): ChannelWindow {
  if (newer.length === 0) return held
  let messages = [...held.messages, ...newer]
  let hasOlder = held.hasOlder
  if (messages.length > CHANNEL_MAX_HELD) {
    messages = messages.slice(messages.length - CHANNEL_MAX_HELD)
    hasOlder = true
  }
  return { messages, hasOlder, hasNewer: newer.length >= CHANNEL_PAGE_SIZE }
}
