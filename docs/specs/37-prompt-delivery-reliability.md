# Prompt Delivery Reliability

A prompt sent from the phone or the desktop can be silently dropped: the
daemon types it into the session's PTY, no hook ever acknowledges it, and the
caller gets `504 the session did not accept the message`. The text is not
always lost — it can sit unsubmitted in Claude's composer for minutes and then
be flushed by the *next* prompt's Enter, so two messages arrive as one.

This spec covers the two independent defects behind that, both confirmed
against a live session, and the client-side change that keeps large pastes out
of the PTY altogether.

## Evidence

Session `80fc19d3`, 2026-08-15, from `~/.helios/logs/daemon.log`:

```
00:36:27 session-send  status=idle live=true      → 00:36:35 never acknowledged
00:36:28 hook claude.session.start                ← agent booted 1s AFTER the send
00:36:38 send → 00:36:46 never acknowledged
00:36:56 send → 00:37:04 never acknowledged
00:37:10 hook session.end · 00:37:12 new host · 00:37:15 session.start
00:37:22 send → 00:37:30 never acknowledged
00:40:10 send (67 chars) → accepted
```

The prompt was 12,738 characters. The transcript
(`~/.claude/projects/.../80fc19d3-….jsonl`) shows a **single** user message at
`18:40:10.427Z` of exactly 12,738 characters whose tail is the *later*, small
prompt. So the big text reached the composer and stayed there; the small
prompt's Enter submitted both as one turn.

## Defect 1 — a warm host is not a ready agent

`handleSessionSend` (`internal/server/api.go:474`) branches on
`Backend.Alive(id)`, which is `Registry.IsWarm` — a socket probe
(`internal/terminal/registry.go:111`). The socket exists from the moment
`ptyhost` starts; the agent inside it is not reading its terminal for seconds
afterwards, and longer still when `claude --resume` has an 8 MB transcript to
load.

The cold path already handles this: it subscribes to `SignalAgentReady` and
waits `agentBootTimeout` before typing. The warm path does not, so anything
that started the host without the send knowing — the mobile app's
`POST /api/sessions/{id}/wake` on session-detail open
(`internal/server/terminal_ws.go:170`), a terminal attach, or the recovery
restart — leaves `live=true` with a booting agent. That is sends 1–3 above.

`restartForPermissionMode` (`api.go:676-687`) documents this exact race
already; the fix is to extend the same guarantee to the warm case.

### Design

- `Registry` entry gains `ready bool`: `false` on a fresh spawn, `true` on
  `adopt` (an adopted host has been running, so its agent is up).
- `Registry.MarkReady(id)` / `IsReady(id)`; `backend.Host.Ready(id)` passthrough.
- `hooks.go` `SessionStarted` marks ready alongside the existing signal fire.
- `handleSessionSend` subscribes to `SignalAgentReady` **before** testing
  readiness, then waits `agentBootTimeout` whenever `!Ready(id)` — warm or cold.

Readiness is per host instance: waking a session that died and respawned must
reset it, or the second boot inherits the first boot's `true`.

## Defect 2 — Enter is dropped after a large paste

`Mirror.SendText` (`internal/terminal/mirror.go:184`) writes the text, sleeps
30 ms, then writes `\r`. Claude enables bracketed paste (`?2004`, verified) and
treats a burst of raw bytes as a paste; when the burst is large the trailing
`\r` is absorbed into it and becomes a newline in the composer instead of a
submit.

### Measured

Real `ptyhost` + real `Mirror`, 9,831-byte payload, fresh Claude, 8 s boot,
counted by whether the model replied:

| Strategy | Submitted |
|---|---|
| Current: text, 30 ms, `\r` | 2/4 |
| text, 0 ms, `\r` | 0/4 |
| Bracketed `ESC[200~ … ESC[201~`, 30 ms, `\r` | 4/4 |
| Bracketed, settle, `\r` | 4/4 |
| Raw text, settle 250 ms, `\r` | 3/3 |

The same payload written to a bare PTY in one `write(2)` always submits, which
is why this only shows up through the host: it is timing-dependent, not
size-dependent, and 50% of sends is the observed rate — matching a bug that
"usually works".

### Design

Bracketing is the principled fix: it tells the application exactly where the
paste ends, so the following `\r` is unambiguous. Settling is the fallback for
applications that never set `?2004`.

`Screen.Paste` (`screen.go:114`) and `Host.Paste` (`host.go:417`) already
exist and are dead code — nothing calls them and no frame reaches them. They
are correct as written: `vt.Emulator.Paste` emits the markers **only** when the
child has set `?2004`, and the emulator's reply pipe is drained into the PTY
master (`host.go:215`), so the decision is made by the emulator that watched
the child enable the mode rather than by a guess in the daemon.

- `protocol.go`: `FramePaste = 0x0d`, C→H, payload is the raw text.
- `host.go` frame switch: `case FramePaste:` → `h.Paste(string(fr.Payload), v.name)`,
  same writer-role treatment as `FrameInput`. Overlay capture does not apply —
  `captureInput` already returns false for `RoleControl`.
- `client.go`: `Paste(text string) error`.
- `Mirror.pump`: stamp `lastOutput` on `FrameOutput` / `FrameSnapshot`.
- `Mirror.SendText`: send the paste frame, wait for the screen to go quiet
  (~250 ms idle, cap ~3 s), then send `\r`. The settle replaces the fixed
  30 ms sleep and covers the non-`?2004` case.

The settle happens before `ack.Wait`, so it does not consume the 8 s
`promptAckTimeout` budget.

### Rollout: the host is older than the daemon

`ptyhost` processes outlive the daemon that spawned them — they survive its
restart, and `make install` replaces the binary without touching them. So a
new daemon routinely talks to hosts from the previous build, and the host's
frame switch has no `default`: a frame it does not know is dropped in silence.
Shipping `FramePaste` without accounting for that broke every send on the
machine until the hosts were restarted — the paste vanished and the `\r` that
followed submitted an empty composer.

The sidecar therefore carries `protocol` (`terminal.HostProtocol`, 1 = knows
`FramePaste`). `NewMirror` reads it from beside the socket; absent or
unreadable means 0, and the mirror types the text as plain input instead. The
settle still applies, which is the bulk of the benefit — measured 3/3 above.

Guessing low costs bracketing. Guessing high costs the prompt. Any future
frame on this path needs the same treatment and a bumped `HostProtocol`.

## Defect 2b — spill oversized prompts to a file

Even with bracketing, a prompt of arbitrary size is a poor thing to push
through a PTY one keystroke-stream at a time. Over `promptSpillLimit`
(**8 KB** — the case that broke was 12.7 KB), `handleSessionSend` writes the
message to the session's upload directory as `prompt-<timestamp>.md` and types
a short reference instead:

```
Read <path> — my full message is there.
```

`UpdateSessionLastUserMessage` keeps storing the **original** text, so session
lists and history still show what the user wrote, not the pointer. The upload
directory already exists for attachments (`internal/server/uploads.go`), is
`0700`, and lives outside the workspace so nothing lands in the user's diff.

## Client change — offer to attach a large paste

Reduces how often a huge prompt is generated at all. The attachment pipeline
(`POST /api/sessions/{id}/files` → `Attached: <path>` lines) already exists on
both clients.

Default is **inline**: the paste lands in the composer exactly as today, and an
offer appears alongside it. Nothing changes without the user choosing it.

- Desktop (`desktop/src/renderer/`): `onPaste` captures the pasted string; if
  it is over threshold, show a dismissible "Large paste (12.4 KB) — attach as
  file?". Accepting removes that exact substring from the composer and pushes
  an `Attachment` built from the bytes (`pasted-1.txt`) into the existing chip
  and upload flow.
- Mobile (`mobile/lib/screens/session_detail_screen.dart`): no paste hook in
  Flutter, so detect a single-change length jump over threshold on the
  controller and offer the same choice in a bottom sheet.
- Threshold ~2,000 characters or ~50 lines, a constant per client.

## Testing

- `internal/terminal`: paste frame round-trip through a real `ptyhost`; settle
  logic against a scripted child that renders slowly. The Claude-driven
  reliability harness that produced the table above is not CI-suitable (needs
  a live model) and is not committed.
- `internal/server/send_test.go`: send blocks until ready when the host is warm
  but the agent has not reported in; spill path writes the file and types the
  reference while history keeps the original text.
- `desktop/test/attachments.test.ts`: paste-to-attachment conversion and
  substring removal.

## Order

1. Defect 2 (paste frame + settle) — highest failure rate, self-contained.
2. Defect 1 (readiness gate) — removes the silent-drop window entirely.
3. Defect 2b (spill).
4. Client paste offer.

Each ships independently.
