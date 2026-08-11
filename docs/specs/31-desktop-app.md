# 31 — Desktop App

## Problem

Helios has two front ends and neither is the one a person works in.

The **TUI** (`helios` with no arguments) lists sessions and hands the terminal
over to one of them with `helios attach`. Attaching is all-or-nothing: the
process takes over stdin in raw mode, so the session list is gone until you
detach. One window, one session, no notifications, no diff view.

The **mobile app** has the whole product — session list, transcript, git
status, file browser, permission approvals, multi-host — and deliberately no
terminal. It renders the transcript instead, because a phone cannot hold an
80-column TUI and because a phone that resized the PTY would ruin the desktop
view (spec 29, "Resize policy").

What is missing is the case the product is actually for: sitting at a machine,
watching three sessions work, typing into whichever one needs a human. Today
that means three terminal windows and a phone next to the keyboard for the
approval prompts.

The pieces to close this already exist. Spec 29 replaced tmux with a terminal
host that serves one session's output from memory to any number of concurrent
viewers, over a self-delimiting frame protocol on a unix socket
(`internal/terminal/protocol.go:18`). Task 8 exposed that same byte stream to
authenticated remote clients through a WebSocket relay
(`internal/server/terminal_ws.go:31`). Nothing consumes the remote one yet.

This spec designs the client that does: an Electron desktop app with a session
sidebar and live terminal tabs, against local and remote hosts alike.

## Current State

| Capability | TUI | Mobile | Desktop app |
|---|---|---|---|
| Session list, status, search | yes | yes | yes |
| Live terminal | one at a time, exclusive | no | **the point** |
| Transcript view | no | yes | yes |
| Permission approvals (HITL) | notifications only | yes | yes |
| Notification list + settings | desktop notify | yes | yes |
| Git status / diff | no | yes | yes |
| File browser / viewer | no | yes | yes |
| New session, commands, providers | yes | yes | yes |
| Settings, event filters | partial | yes | yes |
| Multi-host | no | yes | yes |

The desktop app is **full parity with mobile, plus the terminal**. Everything
the phone can do, the desktop does — notification centre, git status, diffs,
file browsing, settings, event filters, multi-host — and the terminal is the
thing only it has.

The daemon side is done and is not expected to change much. Two servers:

- **Internal**, `127.0.0.1:7654`, no auth (`internal/server/server.go:66`) —
  hooks, CLI admin, `/internal/events` SSE, device pairing tokens.
- **Public**, `0.0.0.0:7655`, bearer auth (`server.go:105`) — everything the
  mobile app uses, plus `GET /api/sessions/{id}/terminal` (WebSocket) and
  `POST /api/sessions/{id}/wake`.

## Goals

1. Watch several sessions at once — a sidebar that shows what every session is
   doing, and terminal tabs for the ones being worked on.
2. A terminal that is genuinely usable: full colour, alt-screen, mouse,
   bracketed paste, correct resize. Claude Code's TUI must look the same as it
   does in the user's own terminal.
3. Local and remote hosts through one UI. A session on the laptop and a session
   on the workstation differ only in latency.
4. Hand-off in both directions: a session opened on the phone continues in a
   desktop tab, mid-turn, with scrollback intact — and vice versa.
5. Answer permission prompts natively, without going to the phone.
6. **Everything the mobile app does.** Notification centre and per-channel
   settings, git status and diffs, the file browser, session creation, slash
   commands, provider and event-filter settings. The desktop is not a terminal
   with a session list bolted on; it is the full client.
7. Closing the app must not disturb a single session.

## Non-Goals

- **Replacing the TUI.** `helios attach` stays. It is the tool for a machine you
  are SSH'd into, and it is 200 lines.
- **Being a general terminal emulator.** No local shells, no arbitrary
  commands, no tmux replacement for the user's own work. Tabs are sessions.
- **An editor.** Diff and file *viewing* are in scope; editing is not. The way
  you change a file is to ask the agent.
- **Windows.** macOS and Linux only. This is not just untested: the daemon does
  not compile for Windows today (`GOOS=windows go build ./...` fails on
  `syscall.SIGWINCH` and `SysProcAttr.Setsid`, among others), and `creack/pty`
  is unsupported there. Electron would be the easy part; the runner is the
  blocker, and porting it is out of scope here.
- **Offline use.** The app is a view onto a daemon. No daemon, no app.

## Why Electron

The terminal is the entire product here, so the emulator picks the stack.
xterm.js is the only emulator in this space with a decade of adoption behind it
— VS Code's integrated terminal, every browser IDE, every cloud shell — and the
measurements above confirm it renders our stream faithfully.

Given xterm.js, the question is what hosts it. Electron, over Tauri, for one
dominant reason: **VS Code is Electron plus xterm.js streaming PTY output
through IPC.** That is not an analogy for our architecture, it *is* our
architecture, shipped to millions of users for a decade. Every hard part of
this spec — high-rate byte streaming from a privileged main process into a
sandboxed renderer's xterm.js, at terminal-redraw rates, without dropping bytes
or janking — is a solved problem there, with public source to read.

Tauri would be leaner and faster to launch. But the risks it carries are the
ones that hurt: a much younger `wry`/`tao` webview layer whose behaviour differs
per platform, a Rust rewrite of auth, SSE, models and twelve screens' worth of
client logic, and no Rust toolchain on the dev machine or in CI. Trading ~150 MB
of bundle for a decade of proving on precisely our workload is the right trade
for a tool people leave open all day.

Practical consequences that fall out of choosing Electron:

- **One language.** The frame codec is ~150 lines of TypeScript and is shared
  verbatim between the main process and the renderer, rather than written in
  Rust and re-modelled in TS across an IPC boundary.
- **Node has everything already.** `net.connect` for the unix socket, `ws` for a
  WebSocket that *can* set an `Authorization` header (which a browser
  `WebSocket` cannot), `node:crypto` for Ed25519 signing, `undici` for SSE.
  Nothing needs a native module.
- **The toolchain exists.** Node v24 is already installed here; CI needs a
  `macos-latest` job either way, and `electron-builder` handles notarisation and
  Linux packaging as a solved path.
- **Mature everything else.** Auto-update, crash reporting, native
  notifications, tray, and `safeStorage` for the device key.

The cost is explicit and accepted:

- **Bundle and memory.** ~150–200 MB installed and ~150 MB idle RSS, against
  ~10 MB and ~50 MB for Tauri. For an always-open developer tool sitting
  alongside a browser and an editor, acceptable.
- **Renderer hardening is on us.** `contextIsolation: true`,
  `nodeIntegration: false`, `sandbox: true`, a strict CSP, and a preload script
  exposing only the narrow API in the IPC table below. Tauri gives a process
  boundary by construction; Electron gives one by configuration, and the
  configuration must be reviewed rather than assumed.
- **Full parity means a second client.** Models, pairing, JWT signing, SSE, the
  multi-host registry, the notification centre, git status and diffs, the file
  browser and every settings screen, all written again in TypeScript. Call it
  8–10k lines, and two client codebases to keep in step with the API forever
  after. The counterpart is that no new daemon endpoints are needed for any
  of it.

## Architecture

### Process topology

Nothing below the daemon changes. The desktop app is one more viewer.

```
  ┌─────────────────────────────────────────────────────────────┐
  │  Helios.app (Electron)                                      │
  │                                                             │
  │   renderer (sandboxed)      │   main process (Node)         │
  │   ─────────────────────     │   ────────────────────        │
  │   sidebar, tabs             │   HostRegistry (N daemons)    │
  │   xterm.js per tab  ◀──IPC──▶   ApiClient  (JWT, HTTP)      │
  │   panels: chat, git,        │   EventStream (SSE)           │
  │   files, notifications      │   TerminalConn (frames)       │
  │                             │   safeStorage (device key)    │
  │   nodeIntegration: false    │                               │
  │   contextIsolation: true    │   preload exposes only the    │
  │   sandbox: true             │   API in the IPC table        │
  └─────────────────────────────┴───────────────┬───────────────┘
                                                │
                     ┌──────────────────────────┴──────────┐
                     │                                     │
              local daemon                           remote daemon
         ┌───────────┴───────────┐              ┌──────────┴──────────┐
         │ 127.0.0.1:7655 (API)  │              │ tailnet:7655 (API)  │
         └───────────┬───────────┘              └──────────┬──────────┘
                     │                                     │
        unix socket  │  (direct — no daemon in the path)   │ WS relay
                     ▼                                     ▼
         ~/.helios/run/<digest>.sock            /api/sessions/{id}/terminal
                     │                                     │
              helios ptyhost                        helios ptyhost
                     │                                     │
                  claude                                claude
```

The main process owns every connection. The renderer never speaks to a daemon:
it receives session state and terminal bytes over IPC and sends input back the
same way. That keeps secrets (the device key, the signed JWT) out of the page,
and means a compromised renderer cannot reach the tailnet. The renderer runs
with `nodeIntegration: false`, `contextIsolation: true` and `sandbox: true`, so
"cannot reach" is enforced rather than merely intended.

### Transport: two paths, one codec

The frame protocol is identical on both paths, because the daemon's WebSocket
handler is a byte relay and not a translator (`terminal_ws.go:22-28`). The
codec is therefore written once, in TypeScript, and used for both:

| Host is | Transport | Auth | Why |
|---|---|---|---|
| local | `UnixStream` to `~/.helios/run/<digest>.sock` | none (0700 dir) | no relay, no TLS, no daemon hop — same path `helios attach` takes |
| remote | `wss://…/api/sessions/{id}/terminal` | `Authorization: Bearer <jwt>` | the only way in from outside |

The socket path for a local session arrives in the session list already: the
daemon injects the live host's handle as `session.terminal`
(`injectTerminal`, `internal/server/server.go:45`). If it is absent the session
is cold and must
be woken (`POST /api/sessions/{id}/wake`, or `?wake=1` on the terminal
endpoint) before there is anything to dial.

Local detection: a host whose URL resolves to loopback **and** whose
`~/.helios/run` is readable. Anything else is remote, including a tailnet
address for this same machine.

### The frame codec

Straight port of `internal/terminal/protocol.go`. Length-prefixed:
`uint32 len(type+payload)`, `uint8 type`, payload. Max frame 8 MiB
(`protocol.go:32`).

| Type | Value | Direction | Payload |
|---|---|---|---|
| Hello | 0x01 | → host | JSON `{role, cols, rows, since, name}` |
| Snapshot | 0x02 | ← host | `uint64 seq` + ANSI resync bytes |
| Output | 0x03 | ← host | raw PTY bytes |
| Input | 0x04 | → host | raw key bytes |
| Resize | 0x05 | → host | `uint16 cols`, `uint16 rows` |
| Status | 0x06 | ← host | JSON `{state, writer, viewers, cols, rows}` |
| Exit | 0x07 | ← host | `int32` exit code |
| Ping / Pong | 0x08 / 0x09 | both | empty |

Golden-file tests share fixtures with the Go implementation so the two cannot
drift — see Testing.

### Connection lifecycle

```
  tab opens
     │
     ├─ session cold?  ──yes──▶ POST /wake   (or GET /terminal?wake=1)
     │                             │
     │                             └─ 25 s worst case: show "starting…"
     ▼
  dial (unix | ws)
     │
     ├──▶ Hello{role:"interactive", cols, rows, since: last_seq, name:"desktop"}
     │
     ◀──── Snapshot{seq, ansi}          if since==0 or the ring evicted it
     │  or  Output{replayed bytes}      if since is still retained
     ◀──── Status{state, writer, viewers, cols, rows}
     │
     ├──▶ Input / Resize / Ping …
     ◀──── Output / Status / Exit …
```

`since` is the byte count consumed so far, tracked exactly as
`internal/terminal/client.go:52` does it: add `len(payload)` for every Output,
adopt the snapshot's `seq` for every Snapshot. The host replays from its 1 MiB
ring when that sequence is still retained and sends a fresh snapshot when it is
not (`internal/terminal/host.go:494-509`), so a reconnect after a laptop sleep
is correct without any client-side cleverness — it is simply a resync.

**Reconnect policy.** On drop: retry at 0.5s, 1s, 2s, 4s, capped at 8s, with
`since` set to the last consumed sequence. The tab keeps its rendered contents
and shows a dimmed "reconnecting" bar rather than clearing. A `wake` is only
issued on an explicit user action, never automatically on reconnect — a
reconnect loop that resurrects `claude` processes is how a laptop lid closing
becomes a fleet of agents.

### Role and resize — one lie the desktop must not tell

The host adopts the smallest size declared by any **interactive** viewer;
observers are ignored entirely (`host.go:303-319`). Desktop tabs need to be
interactive — but that means a 60-column background tab shrinks the PTY for
everyone, including a `helios attach` in a full-screen terminal.

The obvious fix — re-Hello as an `observer` when a tab is backgrounded — **does
not work**. The host reads Hello exactly once, before the viewer is registered
(`host.go:454`); the reader loop afterwards handles only Input, Resize and Ping
(`host.go:550-571`). A second Hello is silently discarded. Role is fixed for
the life of a connection.

What does work, verified against a live host: **an interactive viewer withdraws
from negotiation by sending `Resize(0, 0)`**. `negotiateSize` skips any viewer
with `cols <= 0` (`host.go:308`), and `Host.Resize` early-returns on a
non-positive size (`host.go:288`) so the PTY is never actually set to zero. The
viewer stays connected, keeps receiving output, and keeps its vote parked until
it sends a real size again.

Also verified: **role does not gate input.** `FrameInput` is handled with no
role check (`host.go:551`), which is how the observer-role mobile app types at
all. Role is purely a size-negotiation concept.

So the rules for the client are:

1. Every tab connects as `interactive`, because every tab can be typed into.
2. The **focused** tab sends `Resize(cols, rows)` with its real geometry.
3. **Backgrounding** a tab sends `Resize(0, 0)`. It keeps streaming — the
   sidebar preview stays live — and contributes nothing to the size.
4. Focusing sends the real geometry again. No reconnect, no resync, no daemon
   change.
5. Below 60×20 a focused tab withdraws instead of declaring, and shows "window
   too small". Claude Code's TUI collapses below that, and the damage would be
   inflicted on every other viewer.

Confirmed by test: two interactive viewers at 100×30 and 60×20 negotiate to
60×20; the smaller one sends `Resize(0,0)` and the PTY returns to 100×30.

### Zombie viewers — a bug the desktop will hit and mobile never did

`viewerQueueDepth` is 64 frames (`host.go:23`). A viewer that falls further
behind is dropped by `broadcast` via `v.close()` (`host.go:255-258`), whose
comment says "the connection loop resyncs".

It does not. `v.close()` only closes the viewer's `closed` channel, which stops
the *writer* goroutine. The reader loop is still blocked in `ReadFrame(conn)`
(`host.go:546`), so `serveConn` never returns, so the deferred
`delete(h.viewers, v)` never runs. The result is a zombie:

- still counted in `Status.Viewers`, so every other client's viewer count lies;
- **still holding a size vote**, so it constrains the PTY forever;
- still connected as far as the client is concerned — the socket is not closed;
- receiving nothing, and answering nothing.

Verified against a live host: a viewer at 60×20 is overrun, then a fresh viewer
joins at 100×30 — the PTY stays at **60×20**, and `Status.Viewers` climbs to 2.
A read on the zombie's socket succeeds (buffered spam) rather than returning
EOF, so connection state is not a usable signal.

Mobile never hit this because it consumes its stream promptly and only ever has
one connection. A desktop with several tabs, one of them a `yes`-loop, hits it
in minutes.

**Client mitigation** (verified): heartbeat with `Ping` every 10 s and require a
`Pong` within 5 s. A dropped viewer answers no pings — `Pong` is enqueued
through the same dead channel (`host.go:570`), and the writer goroutine that
would flush it has already exited. In the test the zombie drained its 3 KB of
buffered output and then went silent through a 2 s ping wait. On timeout the
client tears down the connection and reconnects with `since`, which both
resyncs it and finally lets `serveConn` return and clean up the entry.

**Daemon fix** (preferred, listed below): drop the connection, not just the
viewer, so the comment becomes true.

`Status.writer` and `Status.viewers` are surfaced in the tab's status line —
"2 viewers · writer: phone" — because with hand-off in the product, knowing
someone else is typing is not a nicety.

### Warmth: the desktop breaks the current cap

`DefaultMaxWarm = 3` (`internal/terminal/registry.go:19`) and eviction is
least-recently-active first (`registry.go:288`). `lastActive` is only touched
when a session is started or woken (`registry.go:142`) — **an attached viewer
does not count as activity**.

So: open four tabs, and starting the fourth evicts the host of the tab you have
been reading for an hour. Its `claude` exits, its terminal goes cold, and the
scrollback is gone. The mobile app never hit this because it never attaches.

This is a daemon fix, not a client workaround — see "Daemon Changes Required".

### Session state and SSE

One `EventStream` per host, `GET /api/events`, bearer auth, reconnect with
exponential backoff (3s→30s, as the mobile client does at
`daemon_api_service.dart:275`). Events observed today:

| Event | Desktop reaction |
|---|---|
| `session_status` | update the sidebar row; animate the status dot |
| `notification` | native notification + approval card |
| `notification_resolved` | dismiss the card and the notification |
| `subagent_status` | update the row's subagent count |

The stream is a *hint*, not the model: as on mobile, an event schedules a
debounced `GET /api/sessions` and the response is the truth. The desktop
debounce is shorter (150 ms vs 500 ms) — a machine on the same LAN can afford
it, and the sidebar is being watched.

Fallback polling at 3s when the stream is down, only for the host whose window
is focused.

### Auth and pairing

Identical scheme to mobile, reimplemented in TypeScript: a per-device Ed25519 key,
and a self-signed EdDSA JWT with `kid` = device ID, 1 hour expiry, refreshed on
any 401 (`mobile/lib/services/api_client.dart:42`). The seed lives in the OS
OS keychain via Electron's `safeStorage` (Keychain on macOS, libsecret on
Linux), never in plaintext in the app's own data directory.

Two pairing paths, because a desktop has no camera:

**Local (zero friction).** The app is running on the daemon's machine, so it
can reach the no-auth internal API directly:

```
POST 127.0.0.1:7654/internal/device/create   → {token, expires_in: 120}
POST 127.0.0.1:7655/api/auth/pair            {token, kid, public_key}
POST 127.0.0.1:7654/internal/device/activate {kid}
```

Being able to reach `127.0.0.1:7654` is already the trust boundary for the CLI
— that endpoint mints pairing tokens for `helios setup` today. The app should
still show what it did and which device it registered, and the device appears
in `GET /api/auth/devices` like any other.

**Remote.** Paste a `helios://pair?url=…&token=…` URL (the same one the QR
encodes, `internal/server/api.go:1162`), or register as a URL-scheme handler so
clicking it in a browser opens the app. Then the pending-device dance:
`POST /api/auth/pair`, then poll `GET /api/auth/device/me` until the daemon's
operator approves.

### Multi-host

A `HostRegistry` in the main process, mirroring `HostManager` in Dart: a list of
`{id, name, url, device_id}`, one `ApiClient` + `EventStream` + set of
`TerminalConn`s per host, all of them live at once so the sidebar can show
every machine's sessions together.

Sidebar grouping is by host, then by project directory — reusing
`GET /api/sessions/directories`. A single-host user should never see the word
"host" in the UI.

### Window shape

Three columns: sessions, the session, and its context. The right panel is what
carries the mobile feature set into a shape that suits a wide screen — on the
phone transcript, git and files are separate screens you navigate between;
here they sit beside the terminal.

```
┌──────────────────────────────────────────────────────────────────────────┐
│ ●●●  Helios                                     ⌘K    ⚠ 2    laptop ▾    │
├──────────────────┬──────────────────────────────┬────────────────────────┤
│ laptop           │ helios ×  dotfiles ×  +      │ Chat │Git│Files│Notifs │
│  ▸ helios        ├──────────────────────────────┼────────────────────────┤
│    ● fix race    │                              │ ⚠ Bash: rm -rf build/  │
│    ○ add tests   │  ┌────────────────────────┐  │   ┌──────┬────┬─────┐  │
│  ▸ dotfiles      │  │ xterm.js               │  │   │Allow │Once│Deny │  │
│    ◐ zsh prompt ⚠│  │                        │  │   └──────┴────┴─────┘  │
│                  │  │ claude's TUI,          │  ├────────────────────────┤
│ workstation      │  │ unmodified             │  │ M internal/server/…    │
│  ▸ api           │  │                        │  │ + 42  − 7              │
│    ● migration   │  └────────────────────────┘  │                        │
│                  │                              │ ▸ diff hunks           │
│ + New session    ├──────────────────────────────┤                        │
│ ⚙ Settings       │ ready · 120×34 · 2 viewers   │                        │
└──────────────────┴──────────────────────────────┴────────────────────────┘
      ● active   ○ idle   ◐ waiting on you   ⚠ needs approval
```

- **Sidebar** — every session on every host, grouped by host then directory.
  Status dot, title (the generated one, falling back to the last prompt),
  relative time. A row with a pending permission prompt pins to the top of its
  group. Collapsible to icons.
- **Terminal tabs** — `⌘1…⌘9` to switch. `⌘W` closes the tab and **detaches
  only**, never killing the session (that is `⌘⇧W`, with a confirm).
- **Context panel** — per-session, tabbed:
  - *Chat* — the transcript, same content the phone renders, with the prompt
    box. This is how you drive a session without touching the TUI.
  - *Git* — status and diffs, `/api/git/status` and `/api/git/diff`, with a
    proper side-by-side diff since there is finally room for one.
  - *Files* — the browser and viewer, `/api/files` and `/api/file`.
  - *Notifs* — this session's notifications and approvals.
- **Approval** — a permission prompt appears in the context panel *and* as a
  native notification with Allow/Deny actions, so it is answerable without
  focusing the window. The prompt is also live in the terminal underneath;
  resolving any one of the three resolves all of them, which is the
  convergence the phone already relies on.
- **⌘K** — fuzzy switcher over sessions, directories and commands.
- **Menu bar tray** — pending-approval count, and a quick list to jump to a
  session. This is the thing that replaces glancing at the phone.

### Feature surface

Full parity. Every mobile screen has a desktop home, and every one is backed by
an endpoint that already exists:

| Mobile screen | Desktop | Endpoints |
|---|---|---|
| `sessions_screen` | sidebar | `GET /api/sessions`, `/api/sessions/directories` |
| `session_detail_screen` | terminal tab + Chat panel | `GET /api/sessions/{id}`, `/transcript`, `/subagents`, `POST /send`, `/stop`, `/terminate`, `/resume`, `/wake`, `/title/generate` |
| — (new) | terminal | `GET /api/sessions/{id}/terminal` |
| `new_session_sheet` | ⌘N sheet | `POST /api/sessions`, `GET /api/providers`, `/api/commands` |
| `git_status_screen` | Git panel | `GET /api/git/status`, `/api/git/diff`, `/api/git/worktrees` |
| `file_browser_screen` | Files panel (workspace) | `GET /api/files`, `/api/file`, `/api/files/search`, `/api/files/grep`, `PUT /api/file` |
| `dashboard_screen` | Notifs panel + tray | `GET /api/notifications`, `POST /api/notifications/batch`, `POST /api/notifications/{id}/action` |
| `notification_settings_screen` | Settings › Notifications | `GET`/`POST /api/settings` |
| `event_filter_screen` | Settings › Events | `GET`/`POST /api/settings` |
| `settings_screen` | Settings | `GET`/`POST /api/settings`, `GET /api/auth/devices` |
| `host_detail_screen` | host row in Settings › Hosts | `GET /api/health`, `/api/auth/devices` |
| `setup_screen` | first-run pairing | `POST /api/auth/pair`, `GET /api/auth/device/me` |
| `home_screen` | window chrome | — |

No new daemon endpoints are needed for parity. That is the whole reason parity
is affordable: the work is TypeScript client code and views against an API
that is already finished and already exercised by a shipping client.

### Workspace panel

Parity was the floor, not the ceiling. Mobile's file browser is a list you tap
down into; on a desktop the same panel is where you read what the agent just
wrote, so it is an editor: a lazy tree, editor tabs, quick open and find in
files, all rooted at the session's working directory.

The tree, quick open and find all speak to the session's *own* daemon — the
machine Helios runs on is one more host — so browsing a remote session is the
same code path as browsing a local one, with no Electron-side filesystem API
and no second permission model.

Three endpoints were added for it, the only new daemon surface in this app:

| Endpoint | For | Notes |
|---|---|---|
| `GET /api/files/search?path=&q=&limit=` | quick open (⌘P) | candidates from `git ls-files --cached --others --exclude-standard`, falling back to a bounded walk that skips `node_modules`, `.git`, build output and friends; greedy fuzzy subsequence scoring that rewards runs, word boundaries and file-name hits |
| `GET /api/files/grep?path=&q=&regex=&case=&limit=` | find in files (⌘⇧F) | `rg --json` streamed and parsed line by line, with a pure-Go walk-and-scan fallback so the feature works on a host without ripgrep; binaries are skipped on a NUL in the first 8 KiB |
| `PUT /api/file` | saving | body `{path, content, base_mod_time}`; a `base_mod_time` that no longer matches the file on disk is a `409 stale_write` rather than a silent overwrite |

That last one is the whole reason saving is safe to offer. The agent is editing
the same files, so "changed under you" is the normal case, not the edge case:
the save is refused, the header says so, and `⟳` reloads from disk.

All three go through the same `resolveSafePath` as the existing file endpoints
and sit on `protectedMux` behind bearer auth. Both searches are bounded — 10 s,
100k candidates, a result cap — and report `truncated` rather than pretending
the list is complete.

### Lifetime

Sessions outlive the app for free: `helios ptyhost` is spawned detached with
`Setsid` and released (`internal/terminal/spawn.go:44`), precisely so that a
daemon restart cannot SIGHUP live agents. Quitting the app closes viewer
sockets, and the host carries on with zero viewers.

Two things the app must therefore *not* do: kill hosts on quit, and evict
sessions to be tidy. Closing the window is closing a window.

## IPC Surface

The whole contract between the main process and the renderer, exposed to the
page by the preload script via `contextBridge` and nothing else. Commands are
`ipcRenderer.invoke` → `ipcMain.handle`; terminal events arrive on a per-tab
`MessagePort` rather than the shared `ipcRenderer` channel; SSE events use the
shared one.

| Direction | Name | Payload |
|---|---|---|
| cmd | `hosts.list` / `hosts.add` / `hosts.remove` | host records |
| cmd | `hosts.pair_local` / `hosts.pair_url` | pairing flows above |
| cmd | `sessions.list` | `{host_id, q?, cwd?}` → sessions |
| cmd | `sessions.create` | `{host_id, cwd, model, prompt}` |
| cmd | `sessions.send` | `{host_id, id, message}` → ack or the daemon's error |
| cmd | `sessions.stop` / `terminate` / `wake` / `resume` | `{host_id, id}` |
| cmd | `sessions.transcript` / `subagents` | `{host_id, id}` |
| cmd | `term.open` | `{host_id, id, cols, rows}` → `tab_id` |
| cmd | `term.input` | `{tab_id, bytes}` |
| cmd | `term.resize` | `{tab_id, cols, rows}` — `0,0` parks the size vote |
| cmd | `term.close` | `{tab_id}` |
| cmd | `notifs.list` / `notifs.action` / `notifs.batch` | notification centre and approvals |
| cmd | `git.status` / `git.diff` / `git.worktrees` | `{host_id, cwd, …}` |
| cmd | `files.list` / `files.read` | `{host_id, path}` |
| cmd | `files.search` / `files.grep` | `{host_id, path, q, …}` — quick open and find in files |
| cmd | `files.write` | `{host_id, path, content, base_mod_time}` → `409` if it moved under you |
| cmd | `settings.get` / `settings.set` | settings, notification channels, event filters |
| cmd | `meta.commands` / `meta.providers` | for the new-session sheet |
| evt | `term://{tab_id}/output` | `Uint8Array` chunk |
| evt | `term://{tab_id}/snapshot` | `Uint8Array` (clear + write) |
| evt | `term://{tab_id}/status` | `{state, writer, viewers, cols, rows}` |
| evt | `term://{tab_id}/closed` | `{reason, exit_code?}` |
| evt | `sse://{host_id}` | the SSE event, verbatim |

**Throughput is the risk here.** Claude Code redraws aggressively — the 1 MiB
ring holds "seconds of output rather than minutes" (`ring.go:6`). Electron IPC
is not free, so output is coalesced in the main process on an 8 ms tick
(roughly one frame at 120 Hz) into a single `Uint8Array` per flush and
delivered over a dedicated `MessageChannelMain` port per tab, which bypasses
the main `ipcRenderer` queue and transfers the buffer rather than
structured-cloning it. This is the mechanism VS Code uses for the same job.
Target: 5 MB/s sustained into a background tab with no dropped bytes and no visible jank in
the focused one. If that target is missed, the fallback is to stop shipping
bytes to the renderer for unfocused tabs and let the reconnect-with-`since`
path resync them on focus — the protocol already supports it.

## Repo Layout and Build

```
desktop/
├── electron-builder.yml
├── src/
│   ├── main/                  Node — privileged
│   │   ├── main.ts            app lifecycle, windows, tray
│   │   ├── conn.ts            TerminalConn: unix | ws, reconnect, since, ping
│   │   ├── api.ts             ApiClient: JWT sign, 401 refresh, all endpoints
│   │   ├── sse.ts             EventStream (undici)
│   │   ├── hosts.ts           HostRegistry, safeStorage
│   │   ├── notify.ts          native notifications + tray
│   │   └── ipc.ts             handlers + MessageChannelMain ports
│   ├── preload/
│   │   └── preload.ts         contextBridge — the whole IPC table, nothing else
│   ├── shared/
│   │   ├── frames.ts          protocol.go port  (+ golden tests)
│   │   └── models.ts          Session, Notification, GitStatus, FileEntry …
│   └── renderer/              sandboxed
│       ├── sidebar/           sessions, hosts, directories
│       ├── terminal/          xterm.js tabs, size policy
│       └── panels/            chat, git, files, notifications, settings
```

Makefile targets alongside the existing ones: `make desktop` (dev),
`make desktop-app` (release bundle + codesign, mirroring `dmg`). CI needs a
`macos-latest` job it does not currently have — see risk 3.

The Flutter `macos/` build stays until phase 9; then `make dmg` retires and the
Flutter app goes back to being mobile-only.

## Daemon Changes Required

Three were identified, all in `internal/terminal`, all small, all defensible
on their own merits without a desktop app. **Two are now done**, landed while
auditing the reaper. Two further candidates turned out to be unnecessary.

1. **Close the connection when dropping a slow viewer.** *Still outstanding.*
   In `broadcast` (`host.go:255-258`), `v.close()` must be accompanied by
   closing the underlying `net.Conn` so the reader loop unblocks, `serveConn`
   returns, and the deferred `delete(h.viewers, v)` runs. Without it the viewer
   is a zombie that holds a size vote forever — see "Zombie viewers", confirmed
   by test. The fix is to give `viewer` its `conn` and close it in `close()`;
   the existing comment already promises this behaviour.
2. **Viewers count as activity.** *Done.* `Registry.Touch` is now called on
   screen activity (throttled to one update per 5s) and on every prompt or key
   the daemon sends, and `evictForRoom` consults a `Registry.InUse` predicate
   that the host backend answers from each mirror's last `Status.Viewers` — so
   a session with a tab open is never the victim, and if every warm session is
   watched the pool goes over its ceiling rather than killing one. Note the
   mirror is itself a viewer, so the threshold is `> 1`.
3. **Raise `DefaultMaxWarm`.** *Done*, 3 → 20, making `MaxWarmRSS` the ceiling
   that actually binds. Two related fixes came with it: `evictForRoom` now
   takes a `headroom` argument (it always reserved a slot, so the reaper evicted
   a healthy session on every pass and held the pool at `MaxWarm-1`), and the
   RSS ceiling now works on Linux, where `sysctl hw.memsize` returned nothing
   and left the memory ceiling silently disabled. The idle TTL was **removed**
   rather than tuned: a session goes cold when the user closes it and at no
   other time, since an eviction costs the host's scrollback ring and no
   `claude --resume` brings that back.

Not needed after checking:

- ~~A single-session fetch.~~ `GET /api/sessions/{id}` exists and
  `handleGetSession` (`api.go:359`) already calls `injectTerminal`, so a tab
  can resolve its socket path without re-listing.
- ~~A mid-connection role change.~~ Unnecessary once `Resize(0,0)` is used to
  park a background tab's size vote (verified above).

Explicitly *not* touched: the frame protocol, the WS relay, the auth scheme, or
any REST endpoint. Parity with mobile adds **zero** new endpoints. That is the
payoff from task 8 and from the mobile app having gone first.

Three endpoints were added later, and not for parity: `GET /api/files/search`,
`GET /api/files/grep` and `PUT /api/file`, all in `internal/server/filesearch.go`,
for the workspace panel described above. They earn their place independently —
any client wanting to find a file or save an edit needs them — and mobile is
free to pick them up.

## Phasing

| # | Deliverable | Done when |
|---|---|---|
| 0 | Skeleton | `make desktop` opens an empty hardened window (sandbox + contextIsolation) on macOS and Linux |
| 1 | Frame codec | golden fixtures shared with Go round-trip byte-identically |
| 2 | `TerminalConn` over unix socket | a Node test attaches to a real `ptyhost`, types, sees output |
| **3** | **Spike: one hard-coded tab** | **a live Claude session renders in xterm.js and accepts input — the go/no-go** |
| 4 | Daemon fixes | zombie-viewer fix merged with tests (viewer-touch and `MaxWarm` already done) |
| 5 | Auth + `ApiClient` + local pairing | the app pairs itself against the local daemon and lists sessions |
| 6 | Sidebar + SSE | status changes appear without a refresh |
| 7 | Tabs + size policy | two tabs and a `helios attach` coexist; a background tab shrinks nobody |
| 8 | Chat panel | transcript + prompt box, at parity with `session_detail_screen` |
| 9 | Approvals + native notifications + tray | answerable from the desktop; the phone's copy clears |
| 10 | Remote host over WS | the same session over tailnet, indistinguishable but for latency |
| 11 | Multi-host | two daemons, one sidebar |
| 12 | Git panel | status, diff, worktrees |
| 13 | Files panel | browser and viewer |
| 14 | Settings, event filters, commands, providers | the last of mobile parity |
| 15 | Packaging + CI | signed, notarised `.app`; Linux AppImage; both built in CI |

**Phase 3 was the gate, and it has largely been answered ahead of time.**
Headless xterm.js renders both our live stream and our snapshot reconstruction
of a real Claude session faithfully (see "What Was Verified"), so the stack
justification stands. Phase 3 keeps its shape — a hard-coded session, no auth,
no sidebar, no polish, reached as fast as possible — but its remaining job is
narrower: confirm the same fidelity through the *rendered* xterm.js and the IPC
path, and measure throughput against the 5 MB/s target.

Phases 4–9 are the usable product. Phases 12–14 are parity and can ship
incrementally; until each lands, the phone remains the way to do that one
thing, which is an acceptable interim state and not a regression.

## Testing

- **Codec conformance.** A Go test writes one of each frame type — including a
  snapshot with a non-zero sequence, a 4 MiB output frame, and an oversized
  frame that must be rejected — into `testdata/frames/*.bin`. A TS test reads
  the same files and asserts a byte-identical round-trip. Neither side can
  drift without a red build.
- **Live attach.** A Node integration test spawns a real `helios ptyhost`
  running `bash`, attaches, sends `echo hi\n`, and asserts the bytes come back.
  This is `internal/terminal/e2e_test.go` in the other language.
- **Resize negotiation.** Two interactive viewers at different sizes plus one
  observer; assert the PTY adopts the smallest interactive size, that
  `Resize(0,0)` returns it to the larger, and that an observer never moves it.
  The Go half of this exists as `TestHostObserverDoesNotShrinkPTY` and
  `TestHostInteractiveResizeNegotiatesMinimum`; the withdrawal case should be
  added there permanently, since the desktop app depends on it.
- **Zombie viewers.** Overrun a viewer's 64-frame queue and assert the host
  drops it from `h.viewers`, that `Status.Viewers` falls, and that its size vote
  is released. This test fails against today's daemon — it is the regression
  test for daemon change 1.
- **Reconnect and resync.** Kill the connection mid-output, reconnect with
  `since`; assert no gap. Then sleep past the ring's capacity (write >1 MiB)
  and assert a snapshot arrives instead of a corrupt stream.
- **Throughput.** `yes | head -c 50M` inside a session; measure dropped bytes
  and frame time in the focused tab against the 5 MB/s target.
- **Hand-off.** Send a prompt from the phone, watch it appear in the desktop
  tab; type in the desktop tab, watch the phone's transcript follow.

## Alternatives Considered

**Flutter + `xterm.dart`.** Cheaper by a whole codebase. It should be recorded
plainly that the decision to go for **full mobile parity strengthens this
option considerably**: the twelve screens now in scope already exist in Dart and
already build for macOS, so the Flutter route is close to "add a terminal
widget" while the Electron route is "rewrite the app, then add a terminal
widget".

It is still rejected, on the same single ground: the terminal is the reason the
desktop app exists, and `xterm.dart` is a materially less proven emulator than
xterm.js for the alt-screen, mouse-tracking, synchronized-output workload Claude
Code produces. Paying a large one-off cost to de-risk the one feature that
matters is the trade being made deliberately.

If phase 3 shows xterm.js is no better in practice, this option should win by
default and the spec should be withdrawn.

**A web UI served by the daemon.** No install, works from any machine. Rejected
for the same reason cookies were removed in spec 27: a browser page with a
long-lived credential to an agent that can run arbitrary commands is a bad
trade, and the WebSocket cannot carry an `Authorization` header from a browser
without inventing a query-parameter token to leak into logs.

**Extend the TUI to a multi-pane layout.** Cheapest of all, and re-implements
tmux inside the tool that just finished deleting its tmux dependency. No
approvals UI, no notifications, no diff view — the ceiling is too low.

**Tauri + xterm.js.** Same emulator, ~10 MB instead of ~150 MB, ~50 MB idle
instead of ~150 MB, and a privilege boundary by construction rather than by
configuration. Rejected on risk, not on merit: `wry`/`tao` is a much younger
webview layer that behaves differently per platform, the whole client — auth,
SSE, models, the multi-host registry — would be a Rust rewrite, and there is no
Rust toolchain here or in CI. VS Code has shipped this exact
architecture — Electron, xterm.js, PTY bytes over IPC — for a decade. Worth
~150 MB for a tool that stays open all day.

**Electron with a browser `WebSocket` in the renderer.** Rejected for the same
reason as the browser option: a browser `WebSocket` cannot set an
`Authorization` header, and the renderer must not hold the device key. All
network I/O stays in the main process.

## What Was Verified, and What Was Not

This spec makes claims about a daemon that already exists. Those were checked
against the code and, where behaviour rather than structure was at stake,
against a running host.

**Verified by replaying real captured sessions into a real xterm.js.** A Go
harness attached two viewers to a live host — one from the start, one late —
and captured exactly what a desktop tab would receive: the full Output stream,
and the `Snapshot` our own code reconstructs for the late viewer. Both were
replayed into headless xterm.js (`@xterm/headless` + `@xterm/addon-serialize`,
120×40) and compared against each other and against the Go emulator's own view.

| Case | Snapshot resync == live stream, in xterm.js | xterm.js == Go emulator |
|---|---|---|
| **Claude Code** (12 s of real TUI) | **pass** | **pass** |
| **vim** (alt-screen, full redraw) | **pass** | **pass** |
| ANSI torture (colours, 256/truecolor, box drawing, CJK, emoji, cursor moves) | pass after a fix | pass except two known gaps |

Claude Code round-trips exactly, styling included — 42 SGR sequences in the
live render, 42 in the resync. This is the answer to the question the whole
stack choice rested on, and it is yes.

The torture case found **a real bug, now fixed**. `renderRow` walked every cell
column and emitted a space for the continuation cell of a double-width
grapheme, so a row containing CJK or emoji was rendered *wider than the
terminal*. Replaying that snapshot wrapped the row and shifted everything below
it — the captured resync came back with two extra lines and the whole screen
offset. Fixed in `screen.go:279` by advancing past a wide cell's continuation
cells, with `TestScreenSnapshotDoesNotOverflowWidth` as the regression guard
(a 20-column screen was emitting a 22-column row). Every attaching tab uses the
resync path, so this would have been visible constantly.

Two known, accepted divergences remain, both narrow:

- **`ESC[s` / `ESC[u` (ANSI.SYS save/restore cursor) is not implemented** by the
  Go emulator, while xterm.js implements it. Verified directly: with
  `ESC7`/`ESC8` (DECSC/DECRC) the cursor restores correctly; with `ESC[s`/`ESC[u`
  it does not. This only affects the *snapshot*, since a tab attached from the
  start feeds raw bytes to xterm.js and never consults the Go emulator. Neither
  Claude Code nor vim uses the legacy form — the captured Claude stream contains
  one `ESC7`/`ESC8` pair and zero `ESC[s`/`ESC[u` — so this is logged, not fixed.
- `Screen.Text()` still pads the continuation cell of a wide grapheme with a
  space, so `Text()` reports `中 文` where xterm.js reports `中文`. That method
  is documented as being for diagnostics and trust-prompt matching, not display,
  and the desktop app never uses it. Left alone deliberately.

**Verified by running a test against a live `Host`:**

- An interactive viewer withdraws from size negotiation with `Resize(0, 0)`:
  100×30 + 60×20 negotiates to 60×20, and returns to 100×30 when the smaller
  viewer sends zeros. This is the mechanism the tab-focus policy rests on.
- A viewer dropped for exceeding its 64-frame queue is **not** removed: it stays
  in `h.viewers`, `Status.Viewers` keeps counting it, and it pins the PTY to its
  old size while a new 100×30 viewer is ignored. Its socket is not closed.
- That zombie answers no pings, so a Ping/Pong timeout is a sound client-side
  detector — the only one available, since the connection looks healthy.

**Verified by reading the code:**

- Hello is read once, before registration (`host.go:454`); the reader loop
  handles only Input, Resize and Ping. No mid-connection role change exists.
- `FrameInput` has no role check (`host.go:551`) — observers can type.
- The WS relay is `io.Copy` over `websocket.NetConn` (`terminal_ws.go:78-82`),
  so WebSocket message boundaries carry no meaning and the client must treat it
  as a byte stream, exactly like the unix socket. In Node that means feeding
  `ws` message buffers into a running parser, not one-frame-per-message.
- Cold is 409 (`terminal_ws.go:163`), an unreachable host is 502
  (`terminal_ws.go:48`), and a failed wake is 500.
- Every endpoint in the parity table exists, at the paths given
  (`server.go:72-215`).
- `GET /api/sessions/{id}` already injects the terminal handle (`api.go:359`).
- `touch` is called from one place only (`registry.go:142`).
- `helios ptyhost` is spawned with `Setsid` and released (`spawn.go:44`), so
  quitting the app cannot take a session with it.

**Not verified — the open risks:**

- Electron IPC throughput at the rates a redrawing TUI produces. No measurement
  in *this* app, only a designed fallback and the knowledge that VS Code moves
  PTY bytes the same way at higher volumes.
- xterm.js *rendering* — the measurements above used `@xterm/headless`, which
  runs the same parser and buffer as the full package but no canvas/WebGL
  renderer. Cell contents and styling are proven; paint performance and font
  metrics are not.
- `safeStorage` against macOS Keychain and Linux libsecret. On a Linux box with
  no Secret Service running, `safeStorage.isEncryptionAvailable()` returns false
  and the app must degrade to a 0600 file rather than crash.
- Notarisation and Linux packaging of an `electron-builder` bundle in this
  project's CI.
- **Windows.** Excluded as a non-goal, and now confirmed as more than a
  packaging gap: `GOOS=windows go build ./...` fails today (`syscall.SIGWINCH`
  at `attach.go:125`, `SysProcAttr.Setsid` at `spawn.go:44`, plus eight sites in
  `internal/tunnel`), there are no build tags anywhere in the repo, and
  `creack/pty` returns `ErrUnsupported` on Windows. `GOOS=linux` builds clean.
  Electron itself is fine on Windows; the daemon is not. Note also that
  `defaultMaxWarmRSS` shells out to `sysctl -n hw.memsize` (`registry.go:363`),
  so on Linux it returns 0 and the memory ceiling is silently disabled.

## Risks and Open Questions

1. **xterm.js fidelity — resolved.** This was the top risk and the whole stack
   choice rested on it. Measured, not assumed: Claude Code and vim both
   round-trip exactly through the snapshot path into xterm.js, and the one bug
   the exercise found is fixed. What is left is the narrower renderer question
   listed above, not correctness.
2. **IPC throughput** is unmeasured against the 5 MB/s target. The mitigation
   (stop feeding unfocused tabs, resync from `since` on focus) is designed and
   protocol-supported, but adds a visible pause when switching to a busy tab.
3. **CI cannot build or sign this today.** Node v24 is installed on the dev
   machine, so phase 0 is unblocked, but `.github/workflows/release.yml` is a
   single `ubuntu-latest` job with Go and Flutter that builds an APK. A signed,
   notarised macOS bundle needs a `macos-latest` runner, an Apple Developer
   certificate in secrets, and a notarisation step — none of which exist yet.
   That is phase 15, and it is real work that is easy to under-count when
   reading the rest of this document.
4. **Parity doubles the client surface permanently.** Twelve mobile screens
   reimplemented a second time, and every future endpoint costs Dart *and*
   TypeScript from then on. This is now an accepted cost rather than a mitigated
   one — the earlier draft proposed keeping the desktop surface deliberately
   narrow, and that has been rejected in favour of parity. The remaining
   mitigation is sequencing: ship the terminal and approvals first (phases 3–9),
   let git, files and settings land later (12–14), and keep the phone as the
   fallback for anything not yet ported.
5. **Approval races.** The same prompt can be answered on the phone, the
   desktop, and in the terminal within milliseconds. The daemon already resolves
   this — 410 with `already_resolved`, both for a non-pending notification
   (`api.go:63`) and for a lost race inside `Resolve` (`api.go:88`). The desktop
   must render 410 as "answered elsewhere", never as a failure.
6. **Status frames can be dropped.** `broadcastStatus` enqueues
   non-blockingly and discards on a full queue (`host.go:374-378`). The client
   must treat `Status` as a hint and never as a state machine input — geometry
   and viewer count can silently go stale until the next status.
7. **Local socket permissions.** Direct dialling assumes `~/.helios/run` is 0700
   and the app runs as the same user. Under the macOS App Sandbox it could not
   reach the socket at all, so the bundle ships unsandboxed — which forecloses
   Mac App Store distribution.
8. **Two macOS apps in one transition.** The Flutter `make dmg` build exists and
   users may have it installed. Proposal: keep shipping it until phase 9, then
   retire it in the same release that first ships the Electron app, so there is
   never a window in which two apps claim the `helios://` scheme.
