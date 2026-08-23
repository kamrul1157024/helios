import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'

import { silenceDeviceReports } from './deviceReports.ts'
import { linkHandler, webLinkActivate } from './links.ts'
import { bridge } from '../bridge.ts'
import { currentTab, store, terminalId, useStore, type Tab } from '../store.ts'
import { canResume, hasTerminal, type Session } from '../../shared/models.ts'

/**
 * Output arrives on one channel for every tab, so it is dispatched here rather
 * than by a listener per component: a tab that is mid-remount would otherwise
 * drop bytes it can never ask for again.
 */
const sinks = new Map<string, (data: Uint8Array) => void>()
bridge.term.onOutput(({ tabId, data }) => sinks.get(tabId)?.(data))

const encoder = new TextEncoder()

/**
 * The terminal panel: the selected session's terminal, and every other open one
 * kept mounted behind it.
 *
 * Mounted, not rendered on demand, because an xterm that unmounts loses its
 * scrollback and neither the daemon nor the main process can replay it — only
 * the visible pane is `active`, and the rest sit hidden and silent.
 */
export function TerminalPanes({
  hostId,
  session,
  visible,
}: {
  hostId: string | null
  session: Session | null
  visible: boolean
}): JSX.Element {
  const tabs = useStore((s) => s.tabs)
  const activeTab = useStore(currentTab)
  const agent = hostId && session ? tabs.find((t) => t.id === terminalId(hostId, session.session_id)) : undefined
  // The strip decides which of the session's terminals is in front; the
  // agent's is the one a session starts with and the fallback for everything
  // that does not name one.
  const current = (activeTab ? tabs.find((t) => t.id === activeTab) : undefined) ?? agent
  const detached = useStore((s) => s.detached)
  const isDetached = Boolean(hostId && session && detached.includes(terminalId(hostId, session.session_id)))

  useEffect(() => {
    if (!visible || agent || isDetached || !hostId || !session) return
    // Warm sessions attach as soon as the panel is shown: the host is already
    // running, so opening the tab is the whole of the request. A cold one waits
    // for the button below, because attaching would start an agent.
    if (hasTerminal(session)) void store.openTerminal(hostId, session, false)
  }, [visible, agent, isDetached, hostId, session])

  return (
    <div className={visible ? 'panes' : 'panes hidden'}>
      {tabs.map((tab) => (
        <TerminalPane key={tab.id} tab={tab} active={visible && tab.id === current?.id} />
      ))}

      {visible && !current && hostId && session && (
        <div className="pane-empty">
          {/* Resume, not wake, for a terminated session: a wake would start the
              host and attach to a session the daemon still refuses prompts for.
              Resuming moves it back to idle, and the effect above attaches as
              soon as the handle lands. */}
          <p>
            {canResume(session)
              ? 'Session terminated — resume to bring the agent back.'
              : isDetached
                ? 'Disconnected — the agent kept running.'
                : 'No terminal attached to this session.'}
          </p>
          {canResume(session) ? (
            <button className="filled" onClick={() => void store.resumeSession(hostId, session.session_id)}>
              Resume
            </button>
          ) : (
            <button
              className="filled"
              onClick={() => void store.openTerminal(hostId, session, !hasTerminal(session))}
            >
              {hasTerminal(session) ? 'Attach' : 'Wake and attach'}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

function TerminalPane({ tab, active }: { tab: Tab; active: boolean }): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const theme = useStore((s) => s.terminalTheme)

  // Assigned rather than passed on construction: rebuilding the terminal to
  // recolour it would throw away the scrollback, which the host cannot replay
  // a second time.
  useEffect(() => {
    const term = termRef.current
    if (term) term.options.theme = theme
  }, [theme])

  useEffect(() => {
    const container = hostRef.current
    if (!container) return

    const term = new Terminal({
      allowProposedApi: true,
      // Costs a little blending performance, and is the only way a translucent
      // terminal background reaches the window behind it. The WebGL renderer
      // honours it, so this does not fall back to the slower canvas one.
      allowTransparency: true,
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, monospace',
      fontSize: 13,
      // The host replays its own scrollback on attach, so a deep local buffer
      // only duplicates what a snapshot already delivered.
      scrollback: 5000,
      theme,
      macOptionIsMeta: true,
      // OSC 8 hyperlinks: open in the browser instead of xterm's default, which
      // pops a "potentially dangerous" confirm and then fails to open at all.
      linkHandler,
    })
    const reportSilencer = silenceDeviceReports(term.parser)
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.loadAddon(new WebLinksAddon(webLinkActivate))
    term.open(container)

    try {
      term.loadAddon(new WebglAddon())
    } catch {
      // No GPU context — the canvas renderer is slower but always available.
    }

    term.onData((data) => void bridge.term.input(tab.id, encoder.encode(data)))
    term.onBinary((data) => {
      const bytes = new Uint8Array(data.length)
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 0xff
      void bridge.term.input(tab.id, bytes)
    })

    sinks.set(tab.id, (data) => term.write(data))
    termRef.current = term
    fitRef.current = fit

    return () => {
      sinks.delete(tab.id)
      reportSilencer.dispose()
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [tab.id])

  /**
   * Size votes. The host adopts the smallest size any interactive viewer asks
   * for, so a hidden tab reports 0×0 to abstain — otherwise a small background
   * window would squeeze every other viewer, including a full-screen attach.
   */
  useEffect(() => {
    const container = hostRef.current
    const term = termRef.current
    const fit = fitRef.current
    if (!container || !term || !fit) return

    if (!active) {
      void bridge.term.resize(tab.id, 0, 0)
      return
    }

    const apply = (): void => {
      // A hidden element measures as zero; fit() would then propose 1×1.
      if (container.clientWidth === 0 || container.clientHeight === 0) return
      fit.fit()
      void bridge.term.resize(tab.id, term.cols, term.rows)
    }

    apply()
    term.focus()

    const observer = new ResizeObserver(apply)
    observer.observe(container)
    return () => observer.disconnect()
  }, [active, tab.id])

  /**
   * Adopt the size the host settled on, which is not always the one proposed
   * above: the PTY takes the smallest of its interactive viewers, so a phone
   * or a second window on the same session shrinks it.
   *
   * Rendering wider than the PTY is what makes typing look duplicated. The
   * shell wraps and redraws its line against its own width, and each of those
   * cursor moves lands in the wrong cell of a grid that disagrees — the
   * rewritten characters end up beside the originals instead of over them.
   */
  const hostCols = tab.status.cols
  const hostRows = tab.status.rows
  useEffect(() => {
    const term = termRef.current
    if (!term || !hostCols || !hostRows) return
    if (term.cols === hostCols && term.rows === hostRows) return
    term.resize(hostCols, hostRows)
  }, [hostCols, hostRows])

  // The snapshot a host replays on attach can leave the viewport wherever the
  // scrollback happened to land. What the reader wants after reconnecting is
  // the live end of the session, so go there once the connection is up.
  const live = tab.status.state === 'live'
  useEffect(() => {
    if (!live) return
    const timer = setTimeout(() => termRef.current?.scrollToBottom(), 0)
    return () => clearTimeout(timer)
  }, [live])

  return (
    <div className={`pane ${active ? 'active' : ''}`}>
      <div className="pane-term" ref={hostRef} />
      {tab.status.state !== 'live' && (
        <div className="pane-overlay">
          <span className="spinner" />
          <span>{describe(tab)}</span>
        </div>
      )}
    </div>
  )
}

function describe(tab: Tab): string {
  switch (tab.status.state) {
    case 'connecting':
      return 'Connecting…'
    case 'reconnecting':
      return tab.status.detail ? `Reconnecting — ${tab.status.detail}` : 'Reconnecting…'
    case 'closed':
      return tab.status.detail ?? 'Disconnected'
    default:
      return ''
  }
}
