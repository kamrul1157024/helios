# 29 — Terminal Host: Replacing tmux

## Problem

Helios drives Claude Code through the tmux CLI. Every terminal operation is a
subprocess invocation, input is delivered as `send-keys` arguments, and screen
state is recovered by polling `capture-pane` and regex-matching the result.

Two product requirements are unreachable on this foundation:

1. **Remote prompts are slow.** An idle session with no pane is resumed with
   `claude --resume <id> -p "<msg>"` (`internal/server/api.go:509`). `-p` is
   one-shot: it spawns a process, replays the transcript, initialises MCP
   servers, loads settings and `CLAUDE.md`, answers, and exits. That cost is
   paid on *every message sent from mobile*.

   Measured on this machine against Claude Code v2.1.226, trivial prompt
   ("reply with exactly: ok"), so essentially all of it is fixed overhead:

   | Path | Latency | Paid |
   |---|---|---|
   | `claude -p` (cold, new session) | 7.7 s | per message |
   | `claude --resume <id> -p` run 1 | 9.2 s | per message |
   | `claude --resume <id> -p` run 2 | 6.2 s | per message |
   | interactive spawn, new session | 2.3 s | once |
   | interactive `--resume`, 1 MB transcript | 4.7 s | once |
   | **write to an already-warm PTY** | **~0 ms** | — |

   The one-shot path is the expensive one, and *resuming* through it is slower
   than starting fresh. Running the same resume interactively costs roughly
   half as much and is paid once per session rather than once per message.
2. **Mobile and terminal cannot hand off.** There is no shared live view. A
   desktop user attaches a tmux pane; a mobile user reads transcript JSONL.
   Nothing keeps them coherent, and a mobile message may spawn a *second*
   process for a session the user has open in a terminal.

This spec replaces tmux with a Helios-owned terminal host: one warm,
PTY-backed Claude process per session, whose output is served from memory to
any number of concurrent viewers.

## Current State

tmux performs exactly four jobs. Only the last is genuinely hard to replace.

| Job | Code | Replacement difficulty |
|---|---|---|
| Spawn Claude with a TTY | `tmux/client.go:136` `CreateWindow` | Low — `creack/pty` |
| Deliver input | `client.go:279` `SendKeys`; `api.go:483,525`; `actions.go:122,131` | Low — write to PTY master |
| Read the screen | `client.go:294` `CapturePane`, polled every 2s at `pane_watcher.go:107` | Low — we own the screen |
| Attach a real terminal | `client.go:242` `Attach`; TUI `sessions.go:504-584` | **Moderate — the actual work** |

Everything semantic already bypasses tmux. Sixteen hooks are registered
(`provider/claude/register.go:76-91`), including `claude.stop`,
`claude.prompt.submit`, and `claude.session.start`/`end`. The mobile app never
renders a terminal — `mobile/lib/models/session.dart` carries `tmuxPane` only
as an opaque handle for send/stop. **The semantic layer is already tmux-free.**

### Defects in the current integration

1. **Subprocess per operation.** ~25 methods, each a `fork`+`exec` of the tmux
   binary. `PaneInWindow` (`client.go:216`) shells out twice; `RebuildPaneMap`
   (`panemap.go:82`) shells out once per pane.
2. **`send-keys` is a lossy input channel.** `api.go:483` passes an arbitrary
   user message from a phone as a tmux argument. Multi-line prompts, leading
   `-`, `;`, and tmux key names (`Enter`, `C-c`) are mangled. No bracketed
   paste, so Claude Code cannot distinguish paste from typing.
3. **Screen scraping as a control plane.** `pane_watcher.go:86` matches
   `"yes, i trust this folder"` against captured text every 2s;
   `reaper.go:104` `claudeIsIdle` looks for `❯` and `…`. Both break on any
   Claude Code UI change, on scrollback, and on width changes.
4. **Liveness costs a full process-table scan.** `claudeRunningInPane`
   (`panemap.go:134`) runs `ps -ax` system-wide and BFS's the tree — per pane,
   per reaper tick. `ListClaudePanes` (`client.go:310`) adds `pgrep` + `ps` +
   `lsof` per pane.
5. **Critical state lives in the tmux server.** Session→pane mapping is a tmux
   pane option (`panemap.go:67`). If the tmux server dies the mapping is lost,
   which is why the TUI carries resurrect/continuum plugin checks
   (`tui/view.go:440-464`).
6. **Tabs are faked with pane surgery.** `join-pane`/`break-pane`/
   `resize-pane` plus a manual `SIGWINCH` (`tui/sessions.go:504-584`).

## Goals

1. **Warm processes.** One long-lived interactive Claude per session. Cold
   start is paid once per session, not once per message.
2. **Seamless mobile ↔ terminal.** Both surfaces drive the *same* process.
   Switching devices mid-conversation requires no restart and loses no state.
3. **Output from memory.** Live output is served from an in-process ring
   buffer and screen model — no polling, no subprocesses.
4. **Lossless input.** Arbitrary prompt text, including multi-line, reaches
   Claude byte-exact.
5. **Hook-driven state.** Session status derives from hooks, not screen text.
6. **Custom tab management.** Tabs become Helios application state, not tmux
   windows.
7. **Zero external multiplexer.** No tmux, no zellij, no install prerequisite.

## Non-Goals

- Replacing the transcript JSONL as the mobile rendering source. Mobile keeps
  rendering structured messages; it does not become a terminal emulator.
- A general-purpose multiplexer. Helios hosts Claude Code sessions, not
  arbitrary user shells.
- Windows support in this phase.
- Preserving compatibility with tmux-resurrect / continuum. Both are deleted.

## Architecture

### Process topology

```
┌─ helios daemon ─────────────────────────────────────────┐
│                                                         │
│   hooks ─────────────> session status, notifications    │
│   transcript reader ─> structured messages (mobile)     │
│                                                         │
│   TerminalRegistry                                      │
│     └─ dial ──┐                                         │
└───────────────│─────────────────────────────────────────┘
                │  unix socket, ~/.helios/run/<short>.sock
    ┌───────────▼──────────────────┐
    │  helios ptyhost <sessionID>  │   one detached process per session
    │    creack/pty  ──> claude    │
    │    vt screen   (in memory)   │
    │    Ring        (raw bytes)   │
    │    fanout      (N viewers)   │
    └──────────────────────────────┘
```

**Why a separate host process.** The one property of tmux worth keeping is
that sessions survive the thing managing them. If the daemon held the PTY
master directly, every `helios` upgrade or crash would `SIGHUP` every live
Claude session. A small detached `ptyhost` preserves that: the daemon
reconnects to existing sockets on startup, and a host that really did die just
leaves its session cold.

`ptyhost` is a subcommand of the existing binary (`helios ptyhost`), not a
second artifact, so `make install` and codesigning are unchanged.

### Components

```
internal/terminal/
  ring.go        — fixed-capacity byte ring with sequence numbers
  screen.go      — vt emulator wrapper; snapshot + resize
  host.go        — PTY lifecycle, fanout, socket server (ptyhost side)
  client.go      — dial, framing, reconnect (daemon + attach side)
  protocol.go    — frame types, shared by both sides
  registry.go    — daemon-side map sessionID → host conn; spawn, prewarm, evict
cmd/helios/
  ptyhost.go     — `helios ptyhost <sessionID>` entry point
  attach.go      — `helios attach` as a socket client
```

Dependencies to add (versions verified against the module proxy 2026-08-09):

| Module | Version | Purpose |
|---|---|---|
| `github.com/creack/pty` | `v1.1.24` (tagged) | PTY allocation, `Setsize` |
| `github.com/charmbracelet/x/vt` | `v0.0.0-20260803091719` (**untagged**) | terminal emulation |

Plus, in phase 5 only:

| Module | Version | Purpose |
|---|---|---|
| `charm.land/bubbletea/v2` | `v2.0.8` (tagged) | cursor placement in the embedded pane (risk 6) |

`x/vt` does **not** build on `x/cellbuf`. It aliases its cell and geometry
types from `github.com/charmbracelet/ultraviolet`, which is not currently a
dependency. Adding it pulls in roughly eight new modules — `ultraviolet`,
`bits-and-blooms/bitset`, `x/exp/ordered`, `x/termios`, `x/windows`,
`golang.org/x/sync`, `golang.org/x/exp` — and bumps `x/ansi` to v0.11.7 and
`colorprofile` to v0.4.2, both of which Bubble Tea also depends on.

The feared conflict with `bubbletea v1.3.10` **does not exist**: adding `x/vt`
to the real `go.mod` was tested and the build and full test suite pass
unchanged (see Prototype Validation).

### Output path

`ptyhost` reads the PTY master in a loop and writes each chunk to two sinks:

- **`Ring`** — a fixed-capacity byte buffer (default 1 MiB/session,
  configurable) holding recent raw output. Every byte gets a monotonic
  sequence number so a viewer that falls behind detects the gap instead of
  rendering a corrupt stream.
- **`Screen`** — the vt emulator, giving an addressable grid. "What is on
  screen" becomes a struct query rather than a regex over `capture-pane`.

Viewers are served from memory:

- On connect, a viewer receives a `Snapshot` frame: the session rendered as
  an ANSI resync sequence, plus the sequence number it corresponds to. The
  snapshot is **scrollback plus viewport**, not the viewport alone
  (`SnapshotScrollbackLines = 1000`). Because Claude Code renders inline
  rather than on the alternate screen, a viewport-only snapshot shows a
  late-joining phone the last 40 rows of a conversation and nothing before
  them. Verified end to end: `TestE2EClaudeSnapshotCatchesUpLateViewer`
  connects a second viewer to a running Claude session and asserts the
  welcome banner — long since scrolled off the grid — is reconstructed in the
  receiver's scrollback.
- Thereafter it receives `Output` frames — raw bytes, live.
- If a viewer's requested sequence has been evicted from the ring, the host
  sends a fresh `Snapshot` instead of a gap.

Slow viewers are never allowed to block the PTY reader. Each viewer has a
bounded send queue; on overflow the viewer is dropped back to snapshot
resync.

`x/vt` maintains its own scrollback (`DefaultScrollbackSize = 10000` lines)
and exposes damage tracking (`CellDamage`, `MoveDamage`, `RectDamage`). The
`Ring` is still required — it holds byte-exact output for raw viewers, which
the cell grid cannot reproduce — but damage regions are the mechanism for
sending cheap deltas to bandwidth-constrained mobile viewers instead of full
snapshots. Scope that as a phase 6 optimisation, not phase 2.

### Emulator reply channel

**A terminal emulator is bidirectional.** `x/vt` generates responses that must
be written back to the PTY master: cursor position reports, device attributes,
bracketed-paste wrapping. `Emulator` exposes these via `Read()`, backed by an
unbuffered `io.Pipe`.

This was verified experimentally, and it is load-bearing: if nothing drains
`Read()`, the first call that generates a reply **blocks forever**. A probe
calling `Paste()` with no reader deadlocked inside `emulator.go:310`. With a
drain goroutine, `\x1b[6n` (DSR — which Claude Code sends at startup)
correctly produced `\x1b[2;1R`.

The host therefore runs three goroutines per session, not two:

```
PTY master ──read──> [ Ring, Emulator ] ──> viewer fanout
viewer input ──────────write──────────────> PTY master
Emulator.Read() ───── drain (always) ─────> PTY master
```

The drain goroutine must start before the first byte is written to the
emulator and must never be allowed to exit while the child lives.

### Input path

Input is written directly to the PTY master, removing the `send-keys`
escaping problem (defect 2) outright.

Prompt text uses `Emulator.Paste(text)` rather than hand-wrapping
`\x1b[200~ … \x1b[201~`. `Paste` is mode-aware: it consults the terminal's
DECSET 2004 state and emits the bracketed-paste markers only when the
application has enabled them. Verified — with 2004 off the probe emitted bare
text; with it on, `\x1b[200~` preceded the payload. Hand-wrapping would send
literal escape bytes to an application that had not opted in.

Control input — `Escape` to interrupt, `C-c` to abort, `Enter` to accept a
dialog — is sent as literal control bytes, replacing `SendKeysRaw`
(`actions.go:122,131`).

Screen reads from the fanout path need no additional locking: `SafeEmulator`
wraps `Emulator` with concurrency safety, so viewers may query `CellAt`,
`CursorPosition`, and `IsAltScreen` while the PTY reader is writing.

Concurrent input from two surfaces is permitted but surfaced: the host tracks
which viewer last wrote and broadcasts a `writer` field so the UI can show
"someone else is typing". This is an advisory indicator, not a lock.

### Desktop viewing — the TUI renders the grid

Desktop viewing stays in the terminal. This is a bigger change than "reworking
tabs", because **the TUI has never rendered session output**. `helios sessions`
runs inside a tmux pane and `openTab` (`tui/sessions.go:504`) calls `join-pane`
to physically move Claude's pane beside it, then shrinks itself to
`listPanePercent`. tmux does all compositing, input routing, and cursor
handling. Removing tmux deletes the mechanism outright, so the TUI must grow a
renderer it does not currently have.

Five pieces are required:

1. **`renderANSI(em, w, h) string`** — walk the emulator grid and emit a styled
   string for `View()`. Use `uv.Style.Diff(prev)` to emit only the SGR delta
   between adjacent cells rather than resetting per cell. Measured at 178 µs
   for a 120×40 grid.
2. **Key forwarding** — Bubble Tea parses input into `tea.Key` and discards the
   original bytes, so focused-pane input must be re-encoded back to PTY bytes.
   Verified byte-exact across 20 sequences.
3. **Cursor placement** — the emulator's `CursorPosition()` must be drawn as a
   *real* cursor inside the composed frame, offset by the pane's origin.
   Bubble Tea v1 cannot do this at all; v2's `View.Cursor` can. This is the
   reason phase 5 includes the v2 migration (risk 6).
4. **Focus and a prefix key** — when a pane has focus nearly every key must
   reach the PTY, so returning to the list needs an escape prefix. This is
   unavoidable: it is the key layer a multiplexer owns. Default configurable;
   avoid `C-b` since users may still run Helios inside their own tmux.
5. **Frame coalescing** — Claude Code redraws constantly. Render on a ~60fps
   tick when dirty, never once per PTY chunk. Render cost leaves ~90×
   headroom, so this is about avoiding wasted work, not keeping up.

Each open tab is one `interactive` viewer connection. Because the TUI now owns
compositing, N tabs is bookkeeping rather than pane surgery — the current code
can only really hold one (`openTab` breaks all others before joining).

Resize replaces the `SendResizeSignal` SIGWINCH hack: the TUI knows its own
size from `tea.WindowSizeMsg`, computes the pane area, and sends a `Resize`
frame.

`helios attach <id>` remains as the full-screen single-session path and falls
out of the phase 3 socket client for free.

### Resize policy

Two viewers of different widths is the one genuinely new problem; tmux solves
it by shrinking to the smallest attached client, which would let a 60-column
phone ruin a desktop terminal.

Viewers declare a role on connect:

| Role | Votes on PTY size | Used by |
|---|---|---|
| `interactive` | yes | `helios attach`, TUI tabs |
| `observer` | no | mobile raw-terminal view |

PTY size is `min(cols)`, `min(rows)` over *interactive* viewers only. With no
interactive viewers, the last interactive size is retained (default 120×40).
Mobile connects as `observer`, so in the normal path it cannot affect the
desktop's geometry — and because mobile renders the transcript rather than
the grid, it usually needs no terminal connection at all.

### Session lifecycle and warmth

```
cold ──spawn──> warming ──ready──> warm ⇄ busy
 ▲                                  │
 └────────── evict / exit ──────────┘
```

- **Spawn.** `helios ptyhost <sessionID>` is started detached (`setsid`), runs
  `claude --resume <id>` (interactive, *not* `-p`), and serves its socket.
- **Warm.** Claude sits at its prompt. Idle cost is RSS, not CPU.
- **Busy/ready.** Turn boundaries come from the `claude.stop` and
  `claude.prompt.submit` hooks — already registered. `claudeIsIdle`
  (`reaper.go:104`) is deleted.
- **Evict.** An LRU keeps at most `terminal.max_warm` hosts (default 3 —
  measured at 380 MB RSS each, see risk 1) and evicts after
  `terminal.idle_ttl` (default 2h), or earlier if total warm RSS exceeds
  `terminal.max_warm_rss`. Eviction is a clean exit, and the session returns
  to `cold`.
- **Re-warm.** An evicted session comes back the *same* way it started: a new
  `ptyhost` running `claude --resume <id>` interactively. Eviction is
  therefore never lossy — conversation state lives in the transcript, not in
  the host — and there is exactly one spawn path to maintain.

**Eviction is cheap, and that is a measured claim.** Interactive
`claude --resume` was timed against real transcripts replayed in an isolated
project directory:

| Transcript | Messages | Spawn → usable prompt |
|---|---|---|
| 2 KB | 6 | 3.2 s |
| 119 KB | 91 | 2.9 s |
| 1.0 MB | 415 | 4.7 s |

Replay cost is **sub-linear** in transcript size — a 500× larger transcript
costs ~1.5× more — because fixed startup dominates. Two consequences:

1. Eviction can be aggressive. Worst observed re-warm is under 5 s, and
   prewarm hides it behind the user opening the session view.
2. Interactive resume is roughly **half** the cost of the one-shot
   `--resume -p` path it replaces (6.2–9.2 s), and it is paid once per
   session rather than once per message.

**Prewarm.** Cold start is now rare but still exists. When the mobile app
opens a session detail view it issues `POST /api/sessions/{id}/wake`
(debounced, idempotent). Claude reaches its prompt while the user is still
typing, so the message lands on a warm process. This is the single largest
perceived-latency win and costs nothing when the session is already warm.

### Persistence and recovery

Socket path is `~/.helios/run/<hash>.sock`, where `<hash>` is a short digest
of the session ID — macOS caps `sun_path` at 104 bytes, so a raw UUID under a
long home directory is not safe. Each socket has a sidecar
`<hash>.json` holding `{session_id, pid, cwd, started_at}`.

This replaces the `@helios_session_id` tmux pane option (`panemap.go:67`) as
the durable session→terminal mapping. Consequences:

- **Daemon restart** — scan the run dir, dial each socket, rebuild the
  registry. Replaces `RebuildPaneMap`.
- **Liveness** — a successful dial. O(1) per session, replacing the
  system-wide `ps -ax` BFS in `claudeRunningInPane` and the `pgrep`/`ps`/
  `lsof` fan-out in `ListClaudePanes`.
- **Stale sockets** — a failed dial means the host died; unlink the socket and
  sidecar and let the session go cold. Nothing auto-terminates or auto-respawns
  it: the reconciliation tmux needed to stay in sync with an external
  multiplexer has no counterpart here, because helios owns the terminal and
  knows exactly what it started. A session ends when claude says so, through
  the `SessionEnd` hook; otherwise the user resumes it when they want it back.
- **Reboot** — no hosts survive. Sessions are cold and recovered on demand.
  This is a real regression against tmux-resurrect, accepted because
  `claude --resume` reconstructs conversation state anyway.

### Trust prompt detection

The workspace-trust dialog appears *before* Claude starts, so no hook can
report it — screen inspection remains necessary. It does not go away, but it
gets cheaper and less fragile: the match runs against the in-memory vt screen
on output, not a 2s `capture-pane` poll (`pane_watcher.go:107,137`).

**The match must run against rendered text, never the raw byte stream.** This
is not a stylistic preference. Claude Code lays out text with cursor-column
jumps rather than spaces, so `"Welcome to Claude Code"` arrives on the wire as
`"Welcome\x1b[9Gto\x1b[12GClaude\x1b[19GCode"` and the literal string never
appears in the stream at all. A prototype assertion that grepped raw frames
matched nothing; the same assertion against `Screen.Text()` matched
immediately. Any consumer that needs to know what is on screen — trust-prompt
detection, the TUI, the mobile app — must run an emulator, which is precisely
what this design gives them. The
pattern list at `pane_watcher.go:86` is retained as-is — verified still
matching against Claude Code v2.1.226, which renders both `"quick safety
check"` and `"yes, i trust this folder"`. Accepting it with a literal `\r`
written to the PTY master works, and Claude reaches its main UI ~6 s later.

## Wire Protocol

Length-prefixed binary frames over the unix socket. `uint32` big-endian
length, `uint8` type, then payload.

| Type | Dir | Payload | Meaning |
|---|---|---|---|
| `0x01 Hello` | C→H | JSON `{role, cols, rows, since}` | open a viewer |
| `0x02 Snapshot` | H→C | `uint64 seq` + ANSI resync bytes | full screen state |
| `0x03 Output` | H→C | raw bytes | live PTY output |
| `0x04 Input` | C→H | raw bytes | write to PTY master |
| `0x05 Resize` | C→H | `uint16 cols`, `uint16 rows` | interactive viewers only |
| `0x06 Status` | H→C | JSON `{state, writer, viewers}` | advisory UI state |
| `0x07 Exit` | H→C | `int32 code` | child exited; host shutting down |
| `0x08 Ping` / `0x09 Pong` | both | — | liveness, 15s interval |

The daemon exposes this to the network at
`GET /api/sessions/{id}/terminal` (WebSocket), forwarding frames verbatim.
Terminal bytes are **not** multiplexed onto the existing SSE broadcaster,
which continues to carry semantic events only.

## Code Removed

✅ **Done.** Deleting was most of the win. `internal/tmux/` is gone in full —
`client.go`, `iface.go`, `panemap.go` and every pane, window, plugin and
`send-keys` helper in them.

Elsewhere:

- `tui/sessions.go` — pane-surgery tab emulation, replaced by
  `tea.ExecProcess` running `helios attach <id>`.
- `tui/start.go` `screenTmuxRestart` and `tui/view.go`'s tmux install,
  restart and plugin-check screens.
- `tui/start.go` / `daemon/editor.go` `screenEditorSetup` — the VS Code
  terminal profile existed only to launch tmux; the shell wrapper alone now
  gives an editor terminal a durable session, so `setup editor` is gone and
  `setup all` is an alias for `setup shell`.
- `tui/desktop_notif.go` — the pane focus/click machinery
  (`isUserFocusedOnPane`, `focusPane`, `activateApp` and friends) and the
  `desktop.notify.suppress_focused` setting that depended on it.
- `docs/specs/10-tmux-resurrect-integration.md` — superseded.

The net change removed more lines than it added.

## API Changes

| Endpoint | Change |
|---|---|
| `GET /api/sessions/{id}/terminal` | **new** — WebSocket, frame protocol above |
| `POST /api/sessions/{id}/wake` | **new** — idempotent prewarm |
| `POST /api/sessions/{id}/send` | no longer spawns `claude --resume -p`; writes to the warm host, waking it first if cold |
| `GET /api/sessions` | `tmux_pane` replaced by `terminal` — the live handle, or absent when cold |

`tmux_pane` was not kept as a compatibility alias: both clients ship from this
repo, so the rename lands in one sweep rather than dual-writing two keys.

## Data Model Changes

```sql
ALTER TABLE sessions ADD COLUMN terminal_state TEXT NOT NULL DEFAULT 'cold';
ALTER TABLE sessions ADD COLUMN last_active_at TIMESTAMP;  -- LRU eviction
```

Tab layout becomes application state rather than tmux window state:

```sql
CREATE TABLE tabs (
  id          INTEGER PRIMARY KEY,
  session_id  TEXT NOT NULL REFERENCES sessions(session_id),
  position    INTEGER NOT NULL,
  created_at  TIMESTAMP NOT NULL
);
```

## Migration Plan

Phased, each phase independently shippable. The `backend.Backend` interface was
the seam the two implementations coexisted behind; only `Host` remains.

| Phase | Work | Status |
|---|---|---|
| 1 | `internal/backend`: `Backend` interface (`Start`, `Adopt`, `Wake`, `Handle`, `Endpoint`, `Alive`, `Forget`) with a `Host` implementation, consumed by the daemon and the TUI. | ✅ Done |
| 2 | `internal/terminal`: ring, screen, host, protocol. | ✅ Done |
| 3 | `helios ptyhost`, registry, sidecar persistence, socket recovery. | ✅ Done |
| 4 | Warm lifecycle: prewarm endpoint, LRU eviction, `send` rewritten off `claude --resume -p`. | ✅ Done |
| 8 | Authenticated WebSocket terminal endpoint + `/wake` on the public server. | ✅ Done |
| 4b | `helios attach` as a raw-mode unix-socket client, with `^\ d` to detach. | ✅ Done |
| 5 | Strip tmux from the TUI. **The in-TUI renderer and the bubbletea v2 migration were dropped**: `tea.ExecProcess` hands the whole terminal to `helios attach`, which is what the user wanted anyway and saves 8–10d. | ✅ Done |
| 7 | Delete `internal/tmux`, the tmux onboarding screens, and the editor profile setup. | ✅ Done |
| 6 | Mobile: `tmux_pane` → `terminal` rename; input already goes over the API. | Pending |
| 9 | Tauri desktop app — same API and SSE as mobile, xterm.js over the WebSocket endpoint. | Pending |

Dropping the TUI renderer took roughly a week and a half out of the estimate.

## Alternatives Considered

**tmux control mode (`tmux -CC`).** One persistent connection; tmux pushes
`%output` events instead of us polling. Fixes defects 1, 3, and 4 for about a
week of work. Rejected as the destination — it keeps the dependency, keeps
`send-keys`, and delivers neither warm-process handoff nor custom tabs. Viable
as a stopgap if phase 2 slips.

**Zellij.** `zellij action` is the same subprocess-per-call model with a
smaller install base; its plugin API is WASM and a larger lift than owning a
PTY. Trades a mature dependency for an immature one and fixes none of the six
defects.

**dtach / abduco.** Provide detach-persistence and nothing else. We would
still write the emulation, the fanout, and the protocol — i.e. all of phase 2.

**Headless `--input-format=stream-json`.** Structured JSON both ways, no PTY,
no emulation. Attractive in isolation and initially proposed for
daemon-managed sessions. **Rejected:** it creates two incompatible session
kinds — headless-managed and interactive-PTY — which is exactly what makes
mobile↔terminal handoff impossible. Goal 2 requires one kind.

## Prototype Validation

Every load-bearing claim in this spec was tested, not assumed. Environment:
Go 1.26.5, darwin/arm64, `x/vt v0.0.0-20260803091719`, `creack/pty v1.1.24`,
Claude Code v2.1.226. **All gates passed**; the two design changes they forced
are the `max_warm` default (risk 1) and the Bubble Tea v2 migration (risk 6).

A first probe confirmed the emulator primitives:

| Behaviour | Result |
|---|---|
| SGR parsing — `\x1b[1;31m` | ✅ `CellAt(6,0)` = `"r"`, Fg set |
| Cursor tracking through `\r\n` | ✅ reports `(0,1)` |
| `Resize(100, 30)` | ✅ dimensions update |
| DSR `\x1b[6n]` | ✅ replies `\x1b[2;1R` |
| `Paste` with DECSET 2004 **off** | ✅ bare text, no markers |
| `Paste` with DECSET 2004 **on** | ✅ emits `\x1b[200~` prefix |
| `Paste` with no `Read()` drain | ❌ **deadlocks** in `emulator.go:310` |

A second spike then exercised the full path against real PTYs and a real
full-screen application (`vi`), plus Bubble Tea's input layer:

| Behaviour | Result |
|---|---|
| `pty.StartWithSize` → emulator → grid | ✅ child output lands in cells |
| Input written to PTY master | ✅ `echo` round-trips |
| Alt screen (`\x1b[?1049h`) handled | ✅ `IsAltScreen()` true |
| `pty.Setsize` + `em.Resize` | ✅ both emulator and child observe new size |
| Real TUI app (`vi`) rendered back via `renderANSI` | ✅ content matches |
| **Key re-encode fidelity, 20 sequences** | ✅ **20/20 byte-exact** |
| Bracketed paste through Bubble Tea | ✅ `Paste=true`, content intact |
| Kitty keyboard protocol (`\x1b[97;5u`) | ⚠️ **silently swallowed** |
| `renderANSI` on a 120×40 grid | ✅ **178 µs/frame**, ~4.9 KB/frame |
| **`x/vt` added to the real repo** | ✅ **build + all tests pass** |

Two of these retire risks this spec previously flagged. The Bubble Tea
dependency conflict is **not real**: adding `x/vt` to the actual `go.mod` only
bumps `x/ansi` 0.11.6 → 0.11.7 and leaves `cellbuf` at 0.0.15, and everything
still compiles and passes. (A scratch module *did* fail, but only because it
resolved `cellbuf v0.0.13`, which predates the current `x/ansi` API — a
resolution artifact, not an incompatibility.) And key forwarding, assumed to
be the sharpest risk in the terminal-embedded path, round-trips exactly for
every sequence tested including UTF-8, CJK, emoji, and Alt-modified keys.

Render cost leaves ~90× headroom against a 60fps budget, so frame coalescing
is about avoiding wasted work, not about keeping up.

### Against real Claude Code

The remaining gates were then run against an actual `claude` v2.1.226 process
on a PTY, not a stand-in. **All passed.**

| Gate | Result |
|---|---|
| Trust dialog renders through `x/vt` | ✅ correct at 100×30 |
| `pane_watcher.go:86` trust patterns still match | ✅ both patterns present |
| **Main UI renders after accepting trust** | ✅ **full UI, 16 lines, 0 overflow** |
| Typing into a live session | ✅ `hello from helios` appears in the input box |
| Cursor tracked while typing | ✅ col 19, row 14 — just after the text |
| **Resize 100×30 → 60×20 against the real TUI** | ✅ **reflows, 0 lines exceed 60 cols** |
| Typed content survives resize | ✅ preserved |
| Cursor in bounds after resize | ✅ col 19, row 15 of 60×20 |
| Warm idle RSS (process + descendants) | **380 MB** |
| Cold spawn → usable prompt | **2.3 s** |
| Interactive `--resume`, 2 KB → 1 MB transcript | **3.2 s → 4.7 s** (sub-linear) |
| `setsid` child survives parent exit | ✅ reparented, still running |
| `setsid` child is its own session leader | ✅ `getsid(pid) == pid`, differs from parent |

Two findings correct earlier assumptions in this spec:

**Claude Code does not use the alternate screen.** `IsAltScreen()` is `false`
throughout; v2.1.226 renders inline and redraws in place. `x/vt` handles alt
screen correctly, we just do not need it here. The consequence is that
scrollback is real and load-bearing — output accumulates in the primary
buffer — so the `Ring` and `x/vt`'s 10,000-line scrollback both matter more
than if Claude had owned a fixed alt-screen viewport.

This surfaced as a genuine defect during implementation rather than as
theory. The first snapshot implementation rendered the visible grid only;
the end-to-end test connecting a second viewer to a live Claude session
reconstructed the theme picker correctly but had lost the banner above it.
`SafeEmulator` exposes `Scrollback()`, `ScrollbackLen()` and
`ScrollbackCellAt()`, so `Screen.RenderSnapshot(n)` now emits the last `n`
scrolled-off lines ahead of the viewport, prefixed with `ESC[3J` so replaying
a snapshot twice does not stack two copies of the history.

**Grepping the raw stream does not work on a real TUI.** Claude Code
positions text with `ESC[<n>G` column jumps instead of spaces, so no
human-readable phrase survives contiguously in the byte stream. Every
assertion about screen content — in tests and in production — has to go
through an emulator. See "Trust prompt detection".

**An earlier "Claude Code hangs at the trust prompt" observation was a test
artifact.** The probe's screen-stability helper returned before Claude had
advanced. With a plain timeline probe, Claude reaches its main UI ~6 s after
Enter, every run. There is no hang.

### Terminal query replies

Because an application that blocks on an unanswered query would hang, every
query Claude Code was observed to emit was replayed against `x/vt`:

| Query | `x/vt` reply |
|---|---|
| DSR cursor position `CSI 6n` | ✅ `\x1b[1;1R` |
| DSR status `CSI 5n` | ✅ `\x1b[?0n` |
| DA1 `CSI c` | ✅ `\x1b[?62;1;6;22c` |
| DA2 `CSI >c` | ✅ `\x1b[>1;10;0c` |
| DECRQM sync output `CSI ?2026$p` | ✅ `\x1b[?2026;0$y` |
| DECRQM bracketed paste `CSI ?2004$p` | ✅ `\x1b[?2004;2$y` |
| OSC 10 / OSC 11 colour queries | ✅ both answered |
| **kitty keyboard query `CSI ?u`** | ❌ **no reply** (real terminals answer) |
| **XTVERSION `CSI >0q`** | ❌ **no reply** (real terminals answer) |

The two gaps are real but **proven not to matter**: Claude Code emits a kitty
query and does not block on it. A control run with both queries shimmed
(`\x1b[?0u`, an XTVERSION `DCS` reply) produced an identical outcome — same
timeline, same working UI. Should a future application block on either, the
fix is a handler on our side, not an emulator change. Worth a regression test
rather than a design change.

## Risks and Open Questions

1. **Memory per warm session — measured, and it constrains the default.**
   A warm idle Claude Code (process plus descendants) is **380 MB RSS**. So:

   | Warm sessions | Resident |
   |---|---|
   | 3 | 1.1 GB |
   | 5 | 1.9 GB |
   | 10 | 3.7 GB |

   `max_warm = 5` (1.9 GB) is defensible on a 16 GB machine and wasteful on
   8 GB. **Lower the default to 3 and make eviction memory-aware**, not just
   LRU-by-time: evict the least-recently-used host when total warm RSS crosses
   a configurable ceiling (`terminal.max_warm_rss`, default 25% of system
   memory).

   A smaller pool is affordable precisely because re-warming is cheap:
   evicted sessions come back via interactive `claude --resume` in under 5 s
   even for a 1 MB transcript, and prewarm hides that. Memory pressure, not
   latency, is the binding constraint — which is the right trade to make.
2. **`x/vt` maturity — still the largest technical risk, but materially
   reduced.** It has *no tagged release*; the newest pseudo-version is dated
   2026-08-03, so it is actively developed but pre-1.0 with no API stability
   promise, and its package documentation carries the maintainer note
   `"SKIP: Fix typecheck errors - function signature mismatches and undefined
   types"`.

   Against that: it now has **the only test that really counts** behind it —
   real Claude Code renders, accepts input, and reflows correctly through it,
   and it answers every terminal query Claude actually depends on. The
   residual risk is churn in an unpinned API, not correctness. Pin the
   pseudo-version and re-evaluate at the end of phase 2.

   The fallback remains weak: `hinshun/vt10x` was last published in **March
   2022** and is effectively unmaintained. If `x/vt` proves unusable the
   realistic options are vendoring it or writing the emulator. Vendoring is
   now the more attractive of the two, because we have evidence the current
   snapshot works.
3. **Reboot persistence is lost** relative to tmux-resurrect. Accepted above;
   flag it in release notes since it is a user-visible regression.
4. **Detached spawn on macOS — resolved.** Verified two ways. A `setsid`
   child survives its parent exiting (reparented, kept running), and
   `getsid(pid) == pid` confirms it becomes its own session leader while
   holding a PTY. More decisively, **this codebase already does exactly this
   in production**: `cmd/helios/main.go:325` self-daemonizes with
   `SysProcAttr{Setsid: true}` + `Start` + `Process.Release()` + a pidfile,
   and all seven tunnel providers spawn the same way. `ptyhost` is the
   established pattern, not a new one. Downgraded from a phase 3 gate to a
   normal implementation detail.
5. **Input arbitration.** Advisory only in this spec. If concurrent typing
   proves confusing in practice, a soft lock with takeover is the follow-up.
6. **Bubble Tea v1 cannot place the cursor — this forces a v2 upgrade in
   phase 5.** Confirmed against the API: `tea.Model.View()` returns a plain
   `string`, and v1 exposes only `ShowCursor`/`HideCursor` with no positioning
   primitive. An embedded terminal pane must draw the cursor *inside* the
   composed frame, which v1 structurally cannot do — the best available
   workaround is emitting a styled reverse-video cell, which does not blink,
   ignores cursor shape, and is invisible to screen readers.

   Bubble Tea v2 solves it directly: `View` is a struct with a
   `Cursor *tea.Cursor` field carrying position, colour, shape, and blink.
   **Note the module path moved** — it is `charm.land/bubbletea/v2` (v2.0.8),
   *not* `github.com/charmbracelet/bubbletea/v2`; the latter fails to resolve.
   It also builds on the same `ultraviolet` base as `x/vt`, removing the
   cell-type impedance mismatch, and its richer `tea.Key` (with `Text`, `Mod`,
   `ShiftedCode`, `BaseCode`) improves key re-encoding. Adding it upgrades
   `ultraviolet` and `golang.org/x/sys`.

   Treat the v2 migration as **in scope for phase 5**, not a follow-up. Key
   re-encoding still remains necessary — v2 gives no raw bytes either — but
   that path is already proven 20/20 byte-exact under v1.
7. **Kitty keyboard protocol is swallowed** by Bubble Tea v1 — `\x1b[97;5u`
   produced no `KeyMsg` at all. Containable, because our emulator controls
   what it advertises to the inner application, so Claude Code should never
   negotiate kitty encoding through us. The TUI must also not advertise kitty
   support to the *outer* terminal while on v1. Subsumed by the v2 upgrade in
   risk 6.
8. **Codesigning.** `make build` codesigns the binary; confirm a detached
   subcommand spawning a PTY does not trip macOS hardened-runtime
   restrictions. Mitigated by the same precedent as risk 4 — the daemon
   already spawns detached signed subprocesses of itself.
9. ~~**Scrollback matters more than expected.**~~ **Resolved in
   implementation.** The risk was real and materialised: the first `Snapshot`
   rendered the visible grid only, and the end-to-end test against a live
   Claude session caught it losing everything above the fold. `Snapshot` is
   now defined as viewport plus `SnapshotScrollbackLines = 1000` lines of
   history, implemented in `Screen.RenderSnapshot` and covered by
   `TestScreenSnapshotCarriesScrollback`,
   `TestScreenSnapshotScrollbackIsBounded`, and
   `TestE2EClaudeSnapshotCatchesUpLateViewer`.

## Testing

Status as of the phase 2/3 implementation: 44 tests in `internal/terminal`,
all passing, `go vet` clean. Items below are marked ✅ where implemented.

- ✅ **Ring** — sequence continuity, eviction, gap detection, wraparound.
- ✅ **Screen** — resize reflow; snapshot round-trip (including scrollback) against a recorded Claude
  Code output capture.
- ✅ **Reply channel** — assert the drain goroutine keeps `Paste` and DSR
  (`\x1b[6n`) non-blocking, and that replies reach the PTY master. A
  regression here deadlocks the session, so this needs a timeout-guarded test,
  not a happy-path one.
- ✅ **Host** — fanout to N viewers, PTY EOF and exit-code propagation,
  observer-vs-interactive resize negotiation. Slow-viewer eviction is
  implemented but not yet exercised by a test.
- ✅ **Protocol** — frame round-trip, truncated and oversized frames.
- ✅ **Recovery** — `TestE2EHostSurvivesSpawnerExit` detaches a host from a
  process that then exits, and drives the survivor.
  `TestE2ERegistryRecoversAndEvicts` rebuilds a fresh registry from the run
  directory; `TestE2ERegistryCleansStaleSockets` covers the phantom-sidecar
  case. `TestE2ERegistryWakeIsIdempotent` and
  `TestE2EEvictionRespectsMaxWarm` cover the warm-pool contract.
- **Latency** — keystroke-to-viewer p50/p99, gating the phase 2 → 3 decision.
- **Terminal query replies** — assert the emulator answers DSR, DA1, DA2, and
  DECRQM 2004/2026. `x/vt` does not answer the kitty keyboard query or
  XTVERSION; Claude Code tolerates that today, so the test should record the
  current behaviour and fail loudly if an application ever starts blocking.
- **Eviction round-trip** — evict a warm host and re-warm it via
  `claude --resume`, asserting no conversation state is lost and the re-warm
  stays inside a latency budget. Replay cost is sub-linear today; a regression
  to linear would make eviction expensive for long conversations, so assert
  against a large transcript, not a trivial one.
- ✅ **Real Claude Code integration** — `TestE2EClaudeCodeBootsUnderHost` and
  `TestE2EClaudeSnapshotCatchesUpLateViewer` run the real binary under a
  detached `helios ptyhost`, skipped when `claude` is absent. They assert
  against a client-side emulator, not the raw stream, and cover: Claude
  reaching an interactive prompt (which alone proves the emulator answered
  its startup queries — without replies it would hang), arrow keys and Enter
  reaching the TUI as keystrokes, and a late-joining viewer being
  reconstructed from a single snapshot with truecolor styling and scrollback
  intact. Both use a scratch `HOME`, so Claude shows its onboarding flow and
  no prompt is ever submitted — the tests spend no tokens. This is the suite
  that would catch an `x/vt` regression, the top residual risk.
- ⬜ **Resize reflow against Claude Code** — not yet ported from the probes.

Golden-file tests for the emulator require a captured Claude Code output
stream; record one during phase 2. Note that Claude Code renders **inline,
not in the alternate screen**, so a golden capture must include scrollback
behaviour and not assume a fixed viewport.
