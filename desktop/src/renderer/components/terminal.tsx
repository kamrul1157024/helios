import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'

import { silenceDeviceReports } from './deviceReports.ts'
import { linkHandler, webLinkActivate } from './links.ts'
import { isLargePaste, pastedName, pastedTextAttachment, terminalPaths } from '../attachments.ts'
import { api, bridge } from '../bridge.ts'
import { store, terminalId, useStore, type Tab } from '../store.ts'
import { canResume, hasTerminal, needsRecovery, type Session } from '../../shared/models.ts'

/**
 * Output arrives on one channel for every tab, so it is dispatched here rather
 * than by a listener per component: a tab that is mid-remount would otherwise
 * drop bytes it can never ask for again.
 */
const sinks = new Map<string, (data: Uint8Array) => void>()
bridge.term.onOutput(({ tabId, data }) => sinks.get(tabId)?.(data))

const encoder = new TextEncoder()

/**
 * The `panel:terminal` item: what stands in for a terminal there is not one of
 * yet, and the thing that goes and gets one.
 *
 * It holds the item's place in the strip, so a session whose agent attaches
 * while the user is reading it does not shuffle the arrangement underneath
 * them — the tab is already there, and the pane arrives inside it.
 */
export function TerminalPlaceholder({
  hostId,
  session,
  visible,
}: {
  hostId: string | null
  session: Session | null
  visible: boolean
}): JSX.Element | null {
  const tabs = useStore((s) => s.tabs)
  const agent = hostId && session ? tabs.find((t) => t.id === terminalId(hostId, session.session_id)) : undefined
  const detached = useStore((s) => s.detached)
  const isDetached = Boolean(hostId && session && detached.includes(terminalId(hostId, session.session_id)))

  useEffect(() => {
    if (!visible || agent || isDetached || !hostId || !session) return
    // Warm sessions attach as soon as the panel is shown: the host is already
    // running, so opening the tab is the whole of the request.
    //
    // A cold one is now woken rather than waiting for a button. The daemon lets
    // idle sessions go cold on its own, so most cold sessions are ones Helios
    // took away rather than ones the user closed — and making the user click to
    // undo that is friction Helios created. Opening the terminal is a clear
    // enough request.
    //
    // Terminated is different and still needs the button: the daemon refuses
    // prompts for one, so attaching would give a terminal that cannot be used.
    if (hasTerminal(session)) void store.openTerminal(hostId, session, false)
    else if (needsRecovery(session)) void store.openTerminal(hostId, session, true)
  }, [visible, agent, isDetached, hostId, session])

  if (!hostId || !session) return null

  return (
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
  )
}

/**
 * One terminal, mounted for as long as its tab exists.
 *
 * Mounted, not rendered on demand, because an xterm that unmounts loses its
 * scrollback and neither the daemon nor the main process can replay it. A pane
 * whose tab is not in front of its group is hidden rather than dropped.
 *
 * `visible` is whether it is on screen anywhere — several are, once the session
 * is split. `focused` is whether its group is the one the keyboard belongs to,
 * which is a narrower question and the only one worth stealing the caret for.
 */
export function TerminalPane({
  tab,
  active,
  focused,
}: {
  tab: Tab
  active: boolean
  focused: boolean
}): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const hostSized = useRef(false)
  /** Whether the terminal has met the DOM yet; see `apply` below. */
  const opened = useRef(false)
  const theme = useStore((s) => s.terminalTheme)
  const uploads = useStore((s) => s.terminalUploads)
  /** A pasted block big enough to ask about, held until the reader chooses. */
  const [pasted, setPasted] = useState<string | null>(null)

  /**
   * Puts dropped or pasted files where the agent can reach them, and types the
   * paths in.
   *
   * The file is on this machine and the agent is on the daemon's, so the local
   * path names nothing it can open. Uploading the bytes and writing back the
   * path they landed at is what makes this work against a remote host — and
   * against a local one it is the same path either way.
   *
   * The text is typed, not run: the reader decides what to do with it.
   */
  const insertUploads = async (files: File[]): Promise<void> => {
    if (files.length === 0) return
    try {
      const parts = await Promise.all(
        files.map(async (file) => ({
          // A pasted screenshot arrives unnamed, and the name becomes the path.
          name: file.name || pastedName(file.type),
          type: file.type,
          bytes: new Uint8Array(await file.arrayBuffer()),
        })),
      )
      const stored = await api(tab.hostId).uploadFiles(tab.sessionId, parts)
      const text = terminalPaths(stored.map((file) => file.path))
      if (text) await bridge.term.input(tab.id, encoder.encode(text))
    } catch (err) {
      store.notify(`Could not attach to the terminal: ${String(err)}`, 'error')
    }
  }

  /** Types a held block into the terminal after all, bracketing as xterm does. */
  const pasteAnyway = (text: string): void => {
    setPasted(null)
    termRef.current?.paste(text)
    termRef.current?.focus()
  }

  /** Puts a held block in a file and types its path instead. */
  const pasteAsFile = async (text: string): Promise<void> => {
    setPasted(null)
    const { name, type, bytes } = pastedTextAttachment(0, text)
    await insertUploads([new File([bytes as BlobPart], name, { type })])
    termRef.current?.focus()
  }

  // xterm takes paste on a hidden textarea of its own, so this listens in the
  // capture phase to see the paste first.
  //
  // A big block is held rather than offered the way the composer offers it: a
  // composer can take its text back, and a terminal cannot — the bytes are with
  // the agent the moment they are sent. So the choice is made before anything
  // is forwarded. Everything else pastes as it always did.
  useEffect(() => {
    const container = hostRef.current
    if (!container || !uploads) return

    const onPaste = (event: ClipboardEvent): void => {
      const files = [...(event.clipboardData?.files ?? [])]
      if (files.length > 0) {
        event.preventDefault()
        event.stopPropagation()
        void insertUploads(files)
        return
      }
      const text = event.clipboardData?.getData('text') ?? ''
      if (!isLargePaste(text)) return
      event.preventDefault()
      event.stopPropagation()
      setPasted(text)
    }
    container.addEventListener('paste', onPaste, true)
    return () => container.removeEventListener('paste', onPaste, true)
  }, [uploads, tab.id, tab.hostId, tab.sessionId])

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

    // Deliberately not opened here. `open` measures a character against the
    // element it is given, and a pane that mounts behind another tab has no
    // layout to measure — the cell comes out zero wide, and nothing ever
    // measures again: xterm has no resize observer of its own, and
    // `proposeDimensions` gives up on a zero cell, so the pane can no longer
    // even ask the host for a size. It renders an empty grid with a cursor in
    // it until a reconnect builds a new terminal.
    //
    // So the element waits until it has been laid out; see the effect below.
    // Writing before `open` is fine — the bytes land in the buffer, and the
    // snapshot the host replays on attach is there when the pane is shown.

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
      opened.current = false
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
      // First time this pane has had a size: this is where the terminal meets
      // the DOM. Opening earlier would measure a character against an element
      // with no layout and leave the cell zero wide for good.
      if (!opened.current) {
        opened.current = true
        term.open(container)
        // The renderer needs the element, so it cannot be loaded before the
        // open above. One WebGL context per terminal, and the browser evicts
        // the oldest past its limit — a split puts more of them on screen at
        // once, so a lost context is now something that happens.
        try {
          const webgl = new WebglAddon()
          webgl.onContextLoss(() => {
            webgl.dispose()
            try {
              term.loadAddon(new WebglAddon())
            } catch {
              // The canvas renderer is slower and always available.
            }
          })
          term.loadAddon(webgl)
        } catch {
          // No GPU context — the canvas renderer is slower but always available.
        }
      }
      const dims = fit.proposeDimensions()
      if (!dims?.cols || !dims.rows) return
      // Proposed, not applied. fit() would resize the grid here, and the host
      // is free to refuse — it takes the smallest interactive viewer, and it
      // stays silent when the negotiated size does not change. A grid resized
      // to a size the host never adopted therefore never hears otherwise, and
      // renders every redraw against a width the agent is not using.
      //
      // Until the first status there is nothing to adopt, so the proposal is
      // also what to render at: a host that never reports a size would
      // otherwise strand the terminal at xterm's 80×24 default.
      if (!hostSized.current) term.resize(dims.cols, dims.rows)
      void bridge.term.resize(tab.id, dims.cols, dims.rows)
    }

    apply()
    // Only the focused group's terminal takes the caret. With two panes on
    // screen every one of them would otherwise claim it, and the last to
    // render would win on every layout change.
    if (focused) term.focus()

    const observer = new ResizeObserver(apply)
    observer.observe(container)
    return () => observer.disconnect()
  }, [active, focused, tab.id])

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
    hostSized.current = true
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

  // Whether it is on screen is the group's business, not the pane's: the item
  // wrapper carries `hidden`, and a pane that is up fills the cell it is in.
  return (
    <div className="pane">
      <div
        className="pane-term"
        ref={hostRef}
        // Always swallowed, uploads on or off: a file dropped on a window
        // Electron has not claimed navigates it to that file, replacing the app.
        onDragOver={(event) => event.preventDefault()}
        onDrop={(event) => {
          event.preventDefault()
          if (!uploads) return
          void insertUploads([...event.dataTransfer.files])
        }}
      />
      {pasted !== null && (
        <div
          className="paste-offer paste-offer-term"
          onKeyDown={(event) => {
            if (event.key === 'Escape') pasteAnyway(pasted)
          }}
        >
          <span className="paste-offer-text">
            Pasted {new Blob([pasted]).size.toLocaleString()} bytes of text
          </span>
          <button className="ghost" onClick={() => void pasteAsFile(pasted)}>
            Paste as file
          </button>
          {/* The default, and what Escape does: a paste nobody chose about is
              still a paste the reader asked for. */}
          <button className="ghost" onClick={() => pasteAnyway(pasted)} autoFocus>
            Paste text
          </button>
        </div>
      )}
      {tab.status.state !== 'live' && (
        <div className="pane-overlay">
          {/* No spinner once it is closed: nothing is being waited for, and a
              spinner over a dead session reads as one that is still coming. */}
          {tab.status.state !== 'closed' && <span className="spinner" />}
          <span>{describe(tab)}</span>
        </div>
      )}
    </div>
  )
}

function describe(tab: Tab): string {
  switch (tab.status.state) {
    case 'connecting':
      return tab.status.detail ? `Waking — ${tab.status.detail}…` : 'Connecting…'
    case 'reconnecting':
      return tab.status.detail ? `Reconnecting — ${tab.status.detail}` : 'Reconnecting…'
    case 'closed':
      return tab.status.detail ?? 'Disconnected'
    default:
      return ''
  }
}
