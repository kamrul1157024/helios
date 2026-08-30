# Architecture

Helios is one daemon and a set of interchangeable clients. The daemon owns the
terminals the harness runs in; every client reads the same state over HTTP.

## Parts

- **Daemon** — manages terminal-hosted sessions, handles harness hooks, serves an HTTP
  API with SSE for real-time events, and routes notifications.
- **Clients** — desktop app, mobile app, TUI and CLI. All stateless, all talking to the
  same daemon over HTTP.
- **Harness plugins** — Claude Code and OpenAI Codex are implemented, both with native
  hook integration. Another terminal-run harness attaches through the registry in
  `internal/provider`: hooks, actions, commands, models and capabilities are registered,
  not hard-coded.
- **Tunnel** — nine providers behind one picker: Tailscale Serve and Funnel, Cloudflare,
  zrok, ngrok, localtunnel, localhost.run, localxpose, plus plain LAN and a custom URL.
- **Notifications** — the desktop app raises native alerts and answers them in place: an
  approval opens a HUD with the tool call and the same controls the phone has. Phone
  notifications come off the same SSE stream — no push service, no third-party relay.
- **Voice reporting** — narration of tool calls, permission requests, completions and
  errors, generated on the backend and streamed to the phone over SSE. The phone speaks
  it with system TTS; voice, rate, pitch and persona are settings. This narrates session
  activity, it does not read AI answers back to you.

## Phone to daemon

```mermaid
graph LR
    Phone["📱 Helios App<br/>sessions · approve<br/>deny · send msgs"]
    Tunnel["🌐 Tunnel<br/>(Tailscale)"]
    Daemon["🖥️ helios daemon<br/>├── sessions<br/>├── hooks<br/>├── notifications<br/>└── terminals<br/>    ├── claude #1<br/>    └── claude #2"]

    Phone <-->|HTTPS| Tunnel
    Tunnel <-->|HTTPS| Daemon
```

## Many hosts, many devices

One device can hold connections to several machines at once, and one machine can serve
several devices. Every connection is separate: its own pairing, its own credentials, its
own SSE stream. The app keeps all of them live and routes each approve, deny or message
to the machine the session belongs to.

```mermaid
graph TB
    subgraph Devices["Devices"]
        Phone["📱 Phone<br/>Helios App<br/><i>All sessions unified view<br/>color-coded by host</i>"]
    end

    subgraph MachineA["Machine A — Work Laptop"]
        DA["helios daemon"]
        DA --- C1["claude #1"]
        DA --- C2["claude #2"]
        DA --- X3["codex #3"]
    end

    subgraph MachineB["Machine B — Home Desktop"]
        DB["helios daemon"]
        DB --- C4["claude #4"]
        DB --- C5["claude #5"]
    end

    subgraph MachineC["Machine C — Cloud VM"]
        DC["helios daemon"]
        DC --- X6["codex #6"]
    end

    Phone -->|tunnel| DA
    Phone -->|tunnel| DB
    Phone -->|tunnel| DC
```

```mermaid
graph TB
    subgraph MultiDevice["Multiple Devices → One Machine"]
        Ph2["📱 Phone\nHelios App"]
        Tab["📱 Tablet\nHelios App"]
        Mac["💻 Desktop App\n(macOS)"]
    end

    subgraph MachineA2["Machine A"]
        DA2["helios daemon"]
        DA2 --- CC1["claude #1"]
        DA2 --- CC2["claude #2"]
    end

    Ph2 -->|tunnel| DA2
    Tab -->|tunnel| DA2
    Mac -->|tunnel| DA2
```

## Inside the daemon

```mermaid
graph TB
    CLI["CLI / TUI\n(local machine)"]
    Mobile["📱 Mobile / Desktop App\n(phone or laptop)"]

    subgraph Daemon["helios daemon"]
        Internal["Internal Server\n127.0.0.1:7654\n─────────────────\n/internal/health\n/internal/sessions\n/internal/device/create\n/internal/device/list\n/internal/device/activate\n/internal/device/revoke\n/internal/tunnel/start\n/internal/tunnel/stop\n/internal/logs\n/hooks/permission"]

        Public["Public Server\n0.0.0.0:7655\n─────────────────\nGET  /  landing page\nGET  /download  APK\nPOST /api/auth/pair\nPOST /api/auth/login\nGET  /api/auth/device/me\nGET  /api/notifications\nPOST /api/notifications/:id\nGET  /api/sessions\nGET  /api/sse  (realtime)"]

        SQLite["SQLite\nhelios.db"]
        Reaper["Session\nReaper"]
        Tunnel["Tunnel Manager\n(cloudflare/ngrok/..)"]

        subgraph hosts["terminal hosts (helios ptyhost)"]
            S1["claude #1"]
            S2["claude #2"]
            S3["codex #3"]
        end
    end

    Internet["🌐 Public Internet\nhttps://abc.cf"]
    MobileApp["📱 Mobile App\n(Android)\nSessions · Notifications\nApprove/Deny · SSE"]
    DesktopApp["💻 Desktop App\n(macOS)\nSessions · Notifications\nApprove/Deny · SSE"]

    CLI -->|localhost| Internal
    Mobile -->|HTTPS| Public
    Public --> Tunnel
    Tunnel -->|tunnel| Internet
    Internet --> MobileApp
    Internet --> DesktopApp
```

The internal server binds to localhost and is what the CLI and the harness hooks talk
to. The public server is the only one the tunnel exposes.

## On disk

```
~/.helios/
├── config.yaml      ← server config
├── helios.db        ← SQLite (devices, sessions, etc.)
├── daemon.pid       ← running PID
├── helios.apk       ← built APK copy
└── logs/
    └── daemon.log   ← daemon logs

~/.claude/settings.json
└── hooks: [...helios hooks...]
```

## Built with

- **Daemon, CLI and TUI** — Go
- **Mobile** — Flutter (Android, iOS)
- **Desktop** — Electron and TypeScript, packaged with electron-builder (DMG, AppImage,
  deb)
- **Session backend** — Helios terminal hosts (`helios ptyhost`)
- **Real-time** — SSE
- **Auth** — asymmetric JWT (Ed25519), QR code device pairing
- **Harness integration** — Claude Code and OpenAI Codex hooks, native
- **Voice** — backend narration over SSE, Flutter TTS on the phone

Everything runs on your machine. No cloud, no accounts, no subscription.
