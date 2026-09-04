import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api } from '../bridge.ts'
import { useHostGroups, useHostNotifications, useHostSessions } from '../host-data.ts'
import { providersQuery, sessionQuery } from '../queries.ts'
import { currentLayout, store, terminalId, useStore, type RightPanel, type Tab } from '../store.ts'
import { ApprovalsPanel } from './approvals.tsx'
import { ChatPanel } from './chat.tsx'
import { PanelBoundary } from './error-boundary.tsx'
import { FilesPanel } from './files.tsx'
import { GitPanel } from './git.tsx'
import { SchedulePanel } from './schedules.tsx'
import { SelectionMenu } from './selection-menu.tsx'
import { sessionActions } from './session-menu.ts'
import { SettingsPane } from './settings.tsx'
import { StatusLine } from './status-line.tsx'
import {
  isBeside,
  isVisible,
  panelItem,
  panelOf,
  placedItems,
  sweep,
  tabOf,
  termItem,
  type Edge,
  type Group,
  type ItemId,
  type Layout,
} from './layout.ts'
import { TerminalPane, TerminalPlaceholder } from './terminal.tsx'
import { canResume, type Session } from '../../shared/models.ts'

const PANELS: RightPanel[] = ['chat', 'terminal', 'approvals', 'git', 'files']

// The transcript is the agent's side of the session, not a chat room, and not
// the agent either — the agent is the thing running, of which this is the
// record. The store key stays 'chat': it is persisted and referenced elsewhere.
const PANEL_LABELS: Record<RightPanel, string> = {
  chat: 'transcript',
  terminal: 'terminal',
  approvals: 'approvals',
  git: 'git',
  files: 'files',
}

/** How long an unseen panel keeps its state before it is unmounted. */
const PANEL_TTL = 5 * 60 * 1000
const SWEEP_INTERVAL = 60 * 1000

/** The gutter between two groups, in pixels. */
const SASH = 6

/** Below this a row of groups is two unreadable columns, so they stack. */
const NARROW = 1240

/** The drag's own type, so a strip can refuse a payload it cannot hold. */
const TAB_DRAG = 'application/x-helios-tab'

/**
 * Which items the idle sweep may unmount.
 *
 * Git and files hold a place the user chose — the file they were reading, the
 * diff they were working through — and losing it to a timer means finding it
 * again by hand. A terminal holds scrollback nothing can replay. What is left
 * is the transcript and the approvals list, both of which come back fetched.
 */
function sweepable(item: ItemId): boolean {
  const panel = panelOf(item)
  return panel === 'chat' || panel === 'approvals'
}

/**
 * Whether a panel is mounted at all, as against merely listed in a strip.
 *
 * A tab is a thing the session has; mounting is what happens the first time it
 * is looked at. Terminals do not go through this at all — see `PaneDeck`.
 */
function mountable(item: ItemId, layout: Layout, seen: Record<ItemId, number>): boolean {
  return isVisible(layout, item) || seen[item] !== undefined
}

/** The item a tab is shown under: its own for a shell, the panel for an agent. */
function slotOf(tab: Tab): ItemId {
  return tab.kind === 'agent' ? panelItem('terminal') : termItem(tab.id)
}

export function Detail(): JSX.Element {
  const selection = useStore((s) => s.selection)
  // Schedules borrow the same panel: the sidebar decides which list it is
  // showing, and the main area shows whatever that list selected.
  const sidebarMode = useStore((s) => s.sidebarMode)
  const { sessions } = useHostSessions()
  const notifications = useHostNotifications()
  const layout = useStore(currentLayout)
  const tabs = useStore((s) => s.tabs)

  const hostId = selection?.hostId ?? null
  const listed =
    (selection && sessions[selection.hostId]?.find((s) => s.session_id === selection.sessionId)) ?? null

  // A session a schedule started is not in the list — the sidebar leaves those
  // out — so opening a run from its schedule needs the session itself. Only
  // asked for when the list does not have it.
  const { data: fetched } = useQuery({
    ...sessionQuery(selection?.hostId ?? '', selection?.sessionId ?? ''),
    enabled: Boolean(selection) && listed === null,
  })

  const session = listed ?? fetched ?? null
  const pendingList =
    Boolean(selection) && session === null && sessions[selection?.hostId ?? ''] === undefined

  const pending = session
    ? (notifications[hostId ?? ''] ?? []).filter((n) => n.source_session === session.session_id).length
    : 0
  const term = hostId && session ? tabs.find((t) => t.id === terminalId(hostId, session.session_id)) : undefined
  const shells = tabs.filter(
    (t) => t.kind === 'shell' && t.hostId === hostId && t.sessionId === session?.session_id,
  )

  // Shells outlive the app, so a restart would leave them running and
  // invisible. Listing them is also how a second window learns about one.
  useEffect(() => {
    if (!hostId || !session) return
    void store.syncShells(hostId, session.session_id)
  }, [hostId, session?.session_id])

  const sessionKey = hostId && session ? `${hostId}:${session.session_id}` : ''
  // A terminated session's file and diff describe a working tree nobody is
  // changing any more, so they close with it. Switching sessions drops them
  // too: they belong to the tree they were opened from.
  const terminated = session ? canResume(session) : false

  // When each item was last on screen, for the idle sweep below.
  const [seen, setSeen] = useState<Record<ItemId, number>>({})
  useEffect(() => {
    setSeen({})
  }, [sessionKey, terminated])

  const onScreen = layout.groups.map((group) => group.active).join('|')
  useEffect(() => {
    if (terminated || !onScreen) return
    const now = Date.now()
    setSeen((current) => {
      const next = { ...current }
      for (const item of onScreen.split('|')) next[item] = now
      return next
    })
  }, [onScreen, terminated])

  // Held state is worth memory for as long as the user is moving between
  // panels, and not much longer. A panel untouched for the timeout is
  // unmounted, and comes back fetched fresh. An item still in front of any
  // group is in use however long ago the user last touched it, which is what
  // `sweep` checks and is the whole point of a split.
  const held = useRef(layout)
  held.current = layout
  useEffect(() => {
    const timer = setInterval(() => {
      setSeen((current) => {
        const dead = sweep(held.current, current, Date.now(), PANEL_TTL).filter(sweepable)
        if (dead.length === 0) return current
        const next = { ...current }
        for (const item of dead) delete next[item]
        return next
      })
    }, SWEEP_INTERVAL)
    return () => clearInterval(timer)
  }, [])

  // A row of groups needs room. Below the breakpoint they stack instead, and
  // the stored axis is left alone: a narrow window is not a decision.
  const axis = useNarrow() ? 'column' : layout.axis
  const [drag, setDrag] = useState<ItemId | null>(null)

  const placed = placedItems(layout)
  // Rendered in an order that does not depend on the arrangement: the node a
  // panel lives in must not move when its tab does, or React remounts it and
  // the panel loses what it was holding.
  const mounted = PANELS.map(panelItem).filter(
    (item) =>
      placed.includes(item) &&
      mountable(item, layout, seen) &&
      // The terminal slot is the deck's cell once a pane is in it. Leaving an
      // empty wrapper there would stack a transparent box over the terminal.
      !(item === panelItem('terminal') && term),
  )

  // The schedules panel covers the session detail; it does not replace it.
  //
  // Unmounting is what this file spends its effort avoiding: a terminal pane
  // that leaves the tree disposes its xterm, while the connection behind it
  // lives in the main process and keeps counting bytes — so the replacement
  // asks the host to catch it up from a sequence it has already passed and gets
  // an empty grid. Switching the sidebar between its two lists must not cost
  // the reader the terminal they were watching, so the detail stays mounted
  // with no layout, and TerminalPane's ResizeObserver refits it on the way
  // back.
  const showingSchedules = sidebarMode === 'schedules'
  const showingSettings = sidebarMode === 'settings'

  return (
    <>
      {showingSchedules && (
        <div className="detail">
          <PanelBoundary resetKey="schedules">
            <SchedulePanel />
          </PanelBoundary>
        </div>
      )}
      {showingSettings && (
        <div className="detail">
          <PanelBoundary resetKey="settings">
            <SettingsPane />
          </PanelBoundary>
        </div>
      )}
      <div className="detail" style={showingSchedules || showingSettings ? { display: 'none' } : undefined}>
      {hostId && session && <ShowNoteStrip hostId={hostId} sessionId={session.session_id} />}

      <div
        className={`panel-body ${axis}`}
        style={
          axis === 'row'
            ? {
                gridTemplateColumns: layout.groups
                  .map((group) => `minmax(0,${group.size}fr)`)
                  .join(` ${SASH}px `),
                gridTemplateRows: 'auto minmax(0,1fr)',
              }
            : {
                gridTemplateColumns: 'minmax(0,1fr)',
                gridTemplateRows: layout.groups
                  .map((group) => `auto minmax(0,${group.size}fr)`)
                  .join(` ${SASH}px `),
              }
        }
      >
        {!session &&
          (pendingList ? (
            // An unfetched host has no entry at all, an empty one has []. Without
            // that distinction a selected session reads as deleted for as long as
            // its host takes to answer.
            <div className="panel-loading">
              <span className="spinner" />
              <span>Loading session…</span>
            </div>
          ) : (
            <div className="panel-empty">
              <p>{selection ? 'That session is no longer listed.' : 'Select a session.'}</p>
            </div>
          ))}

        {hostId &&
          session &&
          layout.groups.map((group, index) => (
            <GroupTabs
              key={group.id}
              group={group}
              style={stripAt(axis, index)}
              focused={group.id === layout.focused}
              hostId={hostId}
              session={session}
              tabs={tabs}
              term={term}
              pending={pending}
              showAdd={group.id === layout.focused}
              dragging={drag}
              onDragItem={setDrag}
            />
          ))}

        {hostId &&
          session &&
          mounted.map((item) => {
            const owner = layout.groups.find((group) => group.items.includes(item))
            if (!owner) return null
            const index = layout.groups.indexOf(owner)
            return (
              // Keyed by session as well as item. A panel holds where the user
              // was in one session — the file open, the worktree picked, the
              // search typed — and none of that means anything in the next one.
              <div
                key={`${sessionKey}:${item}`}
                className="panel-keep"
                style={bodyAt(axis, index)}
                hidden={owner.active !== item}
                onPointerDownCapture={() => store.focusGroup({ hostId, sessionId: session.session_id }, owner.id)}
              >
                <PanelBoundary resetKey={`${sessionKey}:${item}`}>
                  <ItemContent
                    item={item}
                    hostId={hostId}
                    session={session}
                    tabs={tabs}
                    pending={pending}
                    visible={owner.active === item}
                    focused={owner.id === layout.focused}
                    beside={isBeside(layout, item)}
                  />
                </PanelBoundary>
              </div>
            )
          })}

        {/* Every terminal there is, whatever session is on screen. A pane that
            unmounts disposes its xterm, and the connection behind it lives in
            the main process and keeps counting bytes — so the replacement asks
            the host to catch it up from a sequence it has already passed, and
            gets a delta rather than a screen. An empty grid with a cursor in
            it, until a reconnect. Hence: mounted for as long as the tab is. */}
        {tabs.map((tab) => {
          const mine = tab.hostId === hostId && tab.sessionId === session?.session_id
          const owner = mine
            ? layout.groups.find((group) => group.items.includes(slotOf(tab)))
            : undefined
          const shown = Boolean(owner && owner.active === slotOf(tab))
          const index = owner ? layout.groups.indexOf(owner) : 0
          return (
            <div
              key={`pane:${tab.id}`}
              className="panel-keep"
              style={bodyAt(axis, index)}
              hidden={!shown}
              onPointerDownCapture={() =>
                owner && hostId && session
                  ? store.focusGroup({ hostId, sessionId: session.session_id }, owner.id)
                  : undefined
              }
            >
              <TerminalPane
                tab={tab}
                active={shown}
                focused={shown && owner?.id === layout.focused}
              />
            </div>
          )
        })}

        {/* One per gap, and only while something is being dragged: a permanent
            overlay would eat the clicks meant for the panel beneath it. */}
        {hostId &&
          session &&
          drag &&
          layout.groups.map((group, index) => (
            <DropZone
              key={`drop-${group.id}`}
              style={bodyAt(axis, index)}
              axis={axis}
              onDrop={(edge) => {
                const target = { hostId, sessionId: session.session_id }
                if (edge) store.splitItem(target, drag, group.id, edge)
                else store.moveItem(target, drag, group.id, group.items.length)
                setDrag(null)
              }}
            />
          ))}

        {hostId &&
          session &&
          layout.groups.slice(0, -1).map((group, index) => (
            <Sash
              key={`sash-${group.id}`}
              style={sashAt(axis, index)}
              axis={axis}
              onMove={(delta) => store.resizeGroups({ hostId, sessionId: session.session_id }, index, delta)}
              onReset={() => store.evenGroups({ hostId, sessionId: session.session_id })}
            />
          ))}
        </div>

      {hostId && session && <StatusLine hostId={hostId} session={session} />}
      </div>
    </>
  )
}

/** Whether the window is too narrow for groups to sit beside each other. */
function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(() => window.matchMedia(`(max-width: ${NARROW}px)`).matches)
  useEffect(() => {
    const query = window.matchMedia(`(max-width: ${NARROW}px)`)
    const update = (event: MediaQueryListEvent): void => setNarrow(event.matches)
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])
  return narrow
}

type Axis = 'row' | 'column'
type Placement = { gridColumn: string; gridRow: string }

// A row lays the groups across the columns with one shared pair of rows; a
// column gives each group its own strip and body rows in a single column. Both
// leave a track between neighbours for the sash.
function stripAt(axis: Axis, index: number): Placement {
  return axis === 'row'
    ? { gridColumn: `${index * 2 + 1}`, gridRow: '1' }
    : { gridColumn: '1', gridRow: `${index * 3 + 1}` }
}

function bodyAt(axis: Axis, index: number): Placement {
  return axis === 'row'
    ? { gridColumn: `${index * 2 + 1}`, gridRow: '2' }
    : { gridColumn: '1', gridRow: `${index * 3 + 2}` }
}

function sashAt(axis: Axis, index: number): Placement {
  return axis === 'row'
    ? { gridColumn: `${index * 2 + 2}`, gridRow: '1 / -1' }
    : { gridColumn: '1', gridRow: `${index * 3 + 3}` }
}

/** What an item draws. The switch the single panel body used to be. */
function ItemContent({
  item,
  hostId,
  session,
  tabs,
  pending,
  visible,
  focused,
  beside,
}: {
  item: ItemId
  hostId: string
  session: Session
  tabs: Tab[]
  pending: number
  visible: boolean
  focused: boolean
  /** True when this item has a pane of its own, beside another. */
  beside: boolean
}): JSX.Element | null {
  // Terminals are drawn by the deck in Detail, which outlives the session being
  // selected. Nothing to render here but the panel behind them.
  if (tabOf(item) !== null) return null

  const panel = panelOf(item)
  const agent = tabs.find((one) => one.id === terminalId(hostId, session.session_id))

  // Approvals ride alongside the transcript instead of behind their own tab: an
  // agent that stops for permission stops the panel the user is already looking
  // at, and a tab round-trip per approval is the whole interaction.
  if (panel === 'chat') {
    return (
      <div className="agent-split">
        <ChatPanel hostId={hostId} session={session} active={visible} />
        {pending > 0 && (
          <aside className="approvals-dock">
            <h3 className="dock-title">
              Approvals <span className="badge">{pending}</span>
            </h3>
            <ApprovalsPanel hostId={hostId} sessionId={session.session_id} />
          </aside>
        )}
      </div>
    )
  }

  // The session's own terminal. The pane is the deck's; what is left here is
  // the button that goes and gets one, for a session with no terminal yet.
  if (panel === 'terminal') {
    return agent ? null : <TerminalPlaceholder hostId={hostId} session={session} visible={visible} />
  }

  if (panel === 'approvals') return <ApprovalsPanel hostId={hostId} sessionId={session.session_id} />

  if (panel === 'git') {
    return (
      <GitPanel
        hostId={hostId}
        cwd={session.cwd}
        revision={session.last_event_at}
        sessionId={session.session_id}
        active={visible}
      />
    )
  }

  if (panel === 'files') {
    return (
      <FilesPanel
        hostId={hostId}
        sessionId={session.session_id}
        cwd={session.cwd}
        visible={visible}
        beside={beside}
      />
    )
  }

  return null
}

/**
 * One group's strip: the items in it, in the order they were dragged into.
 *
 * Tabs within a tab would be a hierarchy nobody asked for — a shell is a
 * sibling of the transcript, not a mode of the terminal — so panels and
 * terminals share one row here, as they always have.
 */
function GroupTabs({
  group,
  style,
  focused,
  hostId,
  session,
  tabs,
  term,
  pending,
  showAdd,
  dragging,
  onDragItem,
}: {
  group: Group
  style: Placement
  focused: boolean
  hostId: string
  session: Session
  tabs: Tab[]
  term: Tab | undefined
  pending: number
  showAdd: boolean
  dragging: ItemId | null
  onDragItem: (item: ItemId | null) => void
}): JSX.Element {
  const [over, setOver] = useState<number | null>(null)
  const target = { hostId, sessionId: session.session_id }

  // Which slot the pointer is in, from the buttons themselves rather than from
  // state: a drop can land in the same tick as the drag start.
  const slotFor = (event: React.DragEvent<HTMLElement>): number => {
    const buttons = [...event.currentTarget.querySelectorAll('[data-item]')]
    for (const [index, button] of buttons.entries()) {
      const rect = button.getBoundingClientRect()
      if (event.clientX < rect.left + rect.width / 2) return index
    }
    return buttons.length
  }

  return (
    <nav
      className={`panel-tabs ${focused ? 'focused' : ''} ${over !== null ? 'dropping' : ''}`}
      style={style}
      onPointerDown={() => store.focusGroup(target, group.id)}
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes(TAB_DRAG)) return
        event.preventDefault()
        event.dataTransfer.dropEffect = 'move'
        setOver(slotFor(event))
      }}
      onDragLeave={() => setOver(null)}
      onDrop={(event) => {
        const item = event.dataTransfer.getData(TAB_DRAG)
        setOver(null)
        onDragItem(null)
        if (!item) return
        event.preventDefault()
        store.moveItem(target, item, group.id, slotFor(event))
      }}
    >
      {group.items.map((item, index) => (
        <TabButton
          key={item}
          item={item}
          active={group.active === item}
          insertBefore={over === index}
          hostId={hostId}
          session={session}
          tabs={tabs}
          term={term}
          pending={pending}
          onDragItem={onDragItem}
        />
      ))}
      {dragging && over === group.items.length && <span className="tab-insert" />}

      {showAdd && (
        <button
          className="tab-add"
          title="New shell in this session's directory"
          aria-label="New shell"
          onClick={() => void store.openShell(hostId, session.session_id)}
        >
          +
        </button>
      )}

      {showAdd && <SessionMenuButton hostId={hostId} session={session} />}
    </nav>
  )
}

/**
 * The session's own menu, at the right-hand end of the strip.
 *
 * The same actions the row offers on a right-click, and built from the same
 * function: a second list that drifted from the first would be worse than no
 * second way in at all. It is here because the sidebar can be scrolled away
 * from the session being read, or dragged narrow enough to hide it.
 */
function SessionMenuButton({ hostId, session }: { hostId: string; session: Session }): JSX.Element {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null)
  const { groups, unsupported } = useHostGroups()
  // Only once the menu is open, for the same reason the sidebar's copy waits:
  // the answer never goes stale, but a strip nobody has opened a menu on should
  // not fetch it.
  const { data: providers } = useQuery({ ...providersQuery(hostId), enabled: at !== null })

  return (
    <>
      <button
        className="tab-menu"
        title="Session actions"
        aria-label="Session actions"
        onClick={(event) => {
          const box = event.currentTarget.getBoundingClientRect()
          // Below the button rather than at the pointer: this one has a fixed
          // place on screen, unlike a right-click, and a menu that lands
          // wherever the click did reads as unmoored from it.
          setAt({ x: box.left, y: box.bottom + 4 })
        }}
      >
        ⋯
      </button>

      {at && (
        <SelectionMenu
          x={at.x}
          y={at.y}
          actions={sessionActions(hostId, session, unsupported[hostId] ? [] : (groups[hostId] ?? []), providers)}
          onClose={() => setAt(null)}
        />
      )}
    </>
  )
}

function TabButton({
  item,
  active,
  insertBefore,
  hostId,
  session,
  tabs,
  term,
  pending,
  onDragItem,
}: {
  item: ItemId
  active: boolean
  insertBefore: boolean
  hostId: string
  session: Session
  tabs: Tab[]
  term: Tab | undefined
  pending: number
  onDragItem: (item: ItemId | null) => void
}): JSX.Element {
  const tabId = tabOf(item)
  const shell = tabId !== null ? tabs.find((one) => one.id === tabId) : undefined
  const panel = panelOf(item) as RightPanel | null

  const drag = {
    draggable: true,
    onDragStart: (event: React.DragEvent) => {
      event.dataTransfer.effectAllowed = 'move'
      event.dataTransfer.setData(TAB_DRAG, item)
      event.dataTransfer.setData('text/plain', item)
      onDragItem(item)
    },
    onDragEnd: () => onDragItem(null),
  }

  if (shell) {
    return (
      <ShellTab tab={shell} active={active} insertBefore={insertBefore} drag={drag} />
    )
  }
  if (!panel) return <></>

  return (
    <button
      data-item={item}
      className={`${active ? 'active' : ''} ${insertBefore ? 'insert' : ''}`}
      {...drag}
      onClick={() =>
        panel === 'terminal' ? store.showTerminal(hostId, session) : store.setPanel(panel)
      }
    >
      {/* The old tabstrip carried the connection state, and it is still the one
          thing about a terminal worth seeing from another panel. */}
      {panel === 'terminal' && term && <span className={`dot ${term.status.state}`} />}
      {PANEL_LABELS[panel]}
      {panel === 'approvals' && pending > 0 && <span className="badge">{pending}</span>}
      {panel === 'terminal' && term && (
        <>
          <span
            className="tab-close reload"
            role="button"
            aria-label="Reconnect"
            title="Reconnect — the agent keeps running"
            onClick={(event) => {
              event.stopPropagation()
              void store.reconnectTab(term.id)
            }}
          >
            ⟳
          </span>
          <span
            className="tab-close"
            role="button"
            aria-label="Disconnect"
            title="Disconnect — the agent keeps running"
            onClick={(event) => {
              event.stopPropagation()
              store.disconnectTab(term.id)
            }}
          >
            ⏻
          </span>
        </>
      )}
    </button>
  )
}

/**
 * The half of a group's body a drop would land in.
 *
 * Only mounted while a tab is in flight. A permanent overlay would swallow
 * every click meant for the panel underneath it.
 */
function DropZone({
  style,
  axis,
  onDrop,
}: {
  style: Placement
  axis: Axis
  onDrop: (edge: Edge | null) => void
}): JSX.Element {
  const [edge, setEdge] = useState<Edge | null | 'none'>('none')

  // The edges claim a quarter each and split; the middle half moves the tab
  // into the group. Along the layout's axis only: a flat row of groups has
  // nowhere to put a cross-axis split.
  const edgeFor = (event: React.DragEvent<HTMLElement>): Edge | null => {
    const rect = event.currentTarget.getBoundingClientRect()
    const along =
      axis === 'row'
        ? (event.clientX - rect.left) / rect.width
        : (event.clientY - rect.top) / rect.height
    if (along < 0.25) return 'before'
    if (along > 0.75) return 'after'
    return null
  }

  return (
    <div
      className={`group-drop ${edge === 'none' ? '' : `over ${edge ?? 'into'}`}`}
      style={style}
      onDragOver={(event) => {
        event.preventDefault()
        event.dataTransfer.dropEffect = 'move'
        setEdge(edgeFor(event))
      }}
      onDragLeave={() => setEdge('none')}
      onDrop={(event) => {
        event.preventDefault()
        const landed = edgeFor(event)
        setEdge('none')
        onDrop(landed)
      }}
    />
  )
}

/** The gutter between two groups, and the handle that moves it. */
function Sash({
  style,
  axis,
  onMove,
  onReset,
}: {
  style: Placement
  axis: Axis
  onMove: (delta: number) => void
  onReset: () => void
}): JSX.Element {
  // The drag reads as a fraction of the row rather than in pixels, so the same
  // gesture means the same thing at any window width — and the weights it
  // moves are what the grid is built from.
  const resize = (event: React.PointerEvent<HTMLDivElement>): void => {
    event.preventDefault()
    const body = event.currentTarget.parentElement
    if (!body) return
    const rect = body.getBoundingClientRect()
    const span = axis === 'row' ? rect.width : rect.height
    let from = axis === 'row' ? event.clientX : event.clientY
    const move = (moved: PointerEvent): void => {
      const at = axis === 'row' ? moved.clientX : moved.clientY
      onMove((at - from) / span)
      from = at
    }
    const done = (): void => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', done)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', done)
  }

  return (
    <div
      className={`group-sash ${axis}`}
      role="separator"
      aria-label="Resize the panels"
      title="Drag to resize — double-click to even them out"
      onPointerDown={resize}
      onDoubleClick={onReset}
      style={style}
    />
  )
}

/**
 * A shell's tab. Closing it kills the process — unlike the agent's terminal,
 * which is the session's and only ever detaches — so the cross is the real
 * thing here, and the name is the user's to set.
 */
function ShellTab({
  tab,
  active,
  insertBefore,
  drag,
}: {
  tab: Tab
  active: boolean
  insertBefore: boolean
  drag: Record<string, unknown>
}): JSX.Element {
  const [renaming, setRenaming] = useState(false)
  const [draft, setDraft] = useState(tab.title)

  if (renaming) {
    return (
      <input
        className="tab-rename"
        autoFocus
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => {
          store.renameTab(tab.id, draft)
          setRenaming(false)
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter') event.currentTarget.blur()
          if (event.key === 'Escape') {
            setDraft(tab.title)
            setRenaming(false)
          }
        }}
      />
    )
  }

  return (
    <button
      data-item={termItem(tab.id)}
      className={`${active ? 'active' : ''} ${insertBefore ? 'insert' : ''}`}
      {...drag}
      onClick={() => store.selectTab(tab.id)}
      onDoubleClick={() => {
        setDraft(tab.title)
        setRenaming(true)
      }}
      title={`${tab.title} — double-click to rename`}
    >
      <span className={`dot ${tab.status.state}`} />
      {tab.title}
      {/* Reload and end, the pair the agent's terminal carries, in the same
          place. Its third move — detaching, and staying detached — has no
          meaning here: the shell list re-attaches anything this client is not
          showing the next time the session is opened, so a shell let go of
          comes back on its own. Ending it is the cross. */}
      <span
        className="tab-close reload"
        role="button"
        aria-label="Reload"
        title="Reload — the shell keeps running"
        onClick={(event) => {
          event.stopPropagation()
          void store.reconnectTab(tab.id)
        }}
      >
        ⟳
      </span>
      <span
        className="tab-close"
        role="button"
        aria-label="Close shell"
        title="Close — ends this shell"
        onClick={(event) => {
          event.stopPropagation()
          void store.killShell(tab.id)
        }}
      >
        ×
      </span>
    </button>
  )
}

/**
 * Why the agent moved the view.
 *
 * A panel that switches on its own is indistinguishable from a bug. This says
 * who did it and what to look at, and it stays until dismissed rather than
 * disappearing on a timer — the point is to be read.
 */
function ShowNoteStrip({ hostId, sessionId }: { hostId: string; sessionId: string }): JSX.Element | null {
  const note = useStore((s) => s.showNote)
  if (!note || note.hostId !== hostId || note.sessionId !== sessionId) return null

  return (
    <div className="show-note">
      <span className="show-note-who">agent</span>
      <span className="show-note-text">{note.text}</span>
      <button
        className="icon-btn"
        aria-label="Dismiss"
        title="Dismiss"
        onClick={() => store.clearShowNote()}
      >
        ✕
      </button>
    </div>
  )
}
