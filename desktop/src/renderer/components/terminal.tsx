import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'

import { bridge } from '../bridge.ts'
import { store, useStore, type Tab } from '../store.ts'

/**
 * Output arrives on one channel for every tab, so it is dispatched here rather
 * than by a listener per component: a tab that is mid-remount would otherwise
 * drop bytes it can never ask for again.
 */
const sinks = new Map<string, (data: Uint8Array) => void>()
bridge.term.onOutput(({ tabId, data }) => sinks.get(tabId)?.(data))

const encoder = new TextEncoder()

const THEME = {
  background: '#101014',
  foreground: '#d8d8e0',
  cursor: '#ffb03a',
  selectionBackground: '#33334a',
  black: '#1c1c24',
  red: '#ff6b6b',
  green: '#7ddc8a',
  yellow: '#ffb03a',
  blue: '#6aa9ff',
  magenta: '#c58cff',
  cyan: '#5fd7d7',
  white: '#d8d8e0',
  brightBlack: '#5a5a68',
  brightRed: '#ff8f8f',
  brightGreen: '#a4eaad',
  brightYellow: '#ffca70',
  brightBlue: '#9cc5ff',
  brightMagenta: '#dcb4ff',
  brightWhite: '#ffffff',
}

export function TerminalTabs(): JSX.Element | null {
  const tabs = useStore((s) => s.tabs)
  const activeTab = useStore((s) => s.activeTab)

  if (tabs.length === 0) return null

  return (
    <div className="terminals">
      <div className="tabstrip">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            className={`tab ${tab.id === activeTab ? 'active' : ''} ${tab.status.state}`}
            onClick={() => store.setActiveTab(tab.id)}
          >
            <span className={`dot ${tab.status.state}`} />
            <span className="tab-title">{tab.title}</span>
            <span
              className="tab-close"
              role="button"
              aria-label="Close tab"
              onClick={(event) => {
                event.stopPropagation()
                store.closeTab(tab.id)
              }}
            >
              ×
            </span>
          </button>
        ))}
      </div>

      <div className="panes">
        {tabs.map((tab) => (
          <TerminalPane key={tab.id} tab={tab} active={tab.id === activeTab} />
        ))}
      </div>
    </div>
  )
}

function TerminalPane({ tab, active }: { tab: Tab; active: boolean }): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)

  useEffect(() => {
    const container = hostRef.current
    if (!container) return

    const term = new Terminal({
      allowProposedApi: true,
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, monospace',
      fontSize: 13,
      // The host replays its own scrollback on attach, so a deep local buffer
      // only duplicates what a snapshot already delivered.
      scrollback: 5000,
      theme: THEME,
      macOptionIsMeta: true,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.loadAddon(new WebLinksAddon())
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
