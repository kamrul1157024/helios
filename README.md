<h1 align="center">helios</h1>

<p align="center">
  <strong>A head for headless coding harnesses.</strong><br>
  Claude Code and OpenAI Codex run in a terminal with no UI of their own.<br>
  Helios owns those terminals and puts a desktop app, a phone and a tunnel in front of them.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Elastic%202.0-2f6f4e" alt="License: Elastic License 2.0" /></a>
  <a href="https://github.com/kamrul1157024/helios/releases/latest"><img src="https://img.shields.io/github/v/release/kamrul1157024/helios?color=1f6feb" alt="Latest release" /></a>
  <a href="https://github.com/kamrul1157024/helios/actions/workflows/test.yml"><img src="https://img.shields.io/github/actions/workflow/status/kamrul1157024/helios/test.yml?branch=main&label=tests" alt="Test workflow status" /></a>
  <img src="https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.26+" />
  <img src="https://img.shields.io/badge/platforms-macOS%20%7C%20Linux%20%7C%20Android%20%7C%20iOS-6e7681" alt="Platforms: macOS, Linux, Android, iOS" />
  <img src="https://img.shields.io/badge/harnesses-Claude%20Code%20%7C%20Codex-8957e5" alt="Harnesses: Claude Code, Codex" />
</p>

<p align="center">
  <img src="docs/assets/desktop/chat.png" width="700" alt="Helios desktop app — session transcript" />
</p>

<p align="center">
  <img src="docs/assets/mobile/sessions.png" width="220" alt="Helios mobile app — sessions" valign="top" />
  &nbsp;
  <img src="docs/assets/mobile/session-detail.png" width="220" alt="Helios mobile app — live session detail" valign="top" />
  &nbsp;
  <img src="docs/assets/mobile/terminal.png" width="220" alt="Helios mobile app — live terminal" valign="top" />
</p>

<p align="center">
  <sub>The desktop app, and the same sessions on the phone: the list, a live
  transcript, and the real terminal.</sub>
</p>

---

## Keeping the machine awake

Sessions only run while the machine is awake. Close the lid on a laptop and every
session you were watching from your phone stops with it.
[v-claw](https://github.com/kamrul1157024/v-claw) is a companion menu bar app that
blocks lid-close sleep, display sleep and the screen lock while the machine is on the
power adapter, and releases it all when you unplug. Same author, and it is what makes a
laptop usable as a helios host.

## What it is

You have five agent sessions open across three projects. One is blocked on a permission
prompt. One finished an hour ago. One died. You cannot tell which is which without
walking your terminal tabs.

Helios is a daemon that runs each session in a terminal it owns, watches the harness
hooks, and shows you every session in one place — on your desktop, on your phone, or in
the terminal. When a session needs you, you hear about it and can answer from wherever
you are.

- **Desktop app** — every session on every machine in one sidebar, with the live
  terminal, the transcript, git diffs, approvals and the file tree
- **Mobile app** — the same sessions on your phone, and a notification the moment one
  blocks on a permission
- **Tunnel** — nine providers, one keypress, so the phone can reach the daemon without
  you configuring a network

Everything runs on your machine. No cloud, no accounts, no subscription.

## Install

```bash
git clone https://github.com/kamrul1157024/helios.git && cd helios

make install           # daemon + CLI  → /usr/local/bin/helios   (needs Go 1.26+)
make desktop-install   # desktop app   → /Applications/Helios.app (macOS, needs Node 20+)
make apk-install       # Android app   → the device on adb        (needs Flutter 3.32+)

helios start           # the TUI checks your setup and walks you through the rest
```

Only `make install` is required. The daemon is the product; the apps are clients for it.

| Command | Builds | Installs to |
| --- | --- | --- |
| `make install` | Go binary | `/usr/local/bin/helios` |
| `make desktop-install` | Electron app | `/Applications/Helios.app` |
| `make desktop-app` | Electron app | `desktop/release/*.dmg` (no install) |
| `make apk-install` | Debug APK | the connected Android device |
| `make apk-release` | Release APK | `~/.helios/helios.apk` |

Prebuilt DMGs, an AppImage, a `.deb` and an APK are attached to every
[release](https://github.com/kamrul1157024/helios/releases/latest).

For iPhone there is no prebuilt build: you connect the phone by cable and build it
locally. See [docs/ios.md](docs/ios.md).

## Setup

1. **Install a tunnel provider.** `brew install tailscale` is the recommendation, or
   `brew install cloudflared` if you want a public URL with no account.
2. **Run `helios start`.** The TUI checks the daemon, the harness hooks and the tunnel,
   and tells you what is missing.
3. **Pick your tunnel.** Tailscale Serve keeps the daemon inside your tailnet and the
   hostname never changes; the cost is that the VPN has to be on at the phone end.
   Tailscale Funnel gives you a stable public URL instead. Cloudflare needs no account
   but its URL changes on every restart, so paired devices have to be re-pointed.
4. **Scan the download QR** with your phone camera. It opens a page with the APK and
   the DMG.
5. **Scan the pairing QR** from inside the app, then press `y` in the terminal to
   approve the device.

Screen by screen, with every TUI view: [docs/setup-walkthrough.md](docs/setup-walkthrough.md).

## From your phone

Start a session anywhere:

```bash
$ helios new "fix the auth bug in login.go"
```

When Claude asks for permission your phone buzzes, and you answer it there:

<p align="center">
  <img src="docs/assets/mobile/push-notification.jpg" width="250" alt="Push notification on phone" />
  <img src="docs/assets/mobile/notifications.jpg" width="250" alt="Notifications tab" />
  <img src="docs/assets/mobile/question-notification.jpg" width="250" alt="Question notification" />
</p>

The phone is not a read-only mirror. It carries the session list across every paired
machine, the live transcript, the permission mode, the same terminal the desktop app
attaches to, the session's git worktrees and its files.

<p align="center">
  <img src="docs/assets/mobile/permission-mode.png" width="250" alt="Permission mode picker" />
  <img src="docs/assets/mobile/git.png" width="250" alt="Git worktrees on the phone" />
  <img src="docs/assets/mobile/files.png" width="250" alt="File viewer on the phone" />
</p>

Helios can also read the session out loud as it works — tool calls, permission requests,
completions and errors — so you can follow along without looking.

## Desktop app

On first launch the app pairs itself with the daemon on the same machine, with no QR
code and no token. `Add host` in the sidebar pairs it with remote machines the same way
the phone does.

The sidebar lists every session on every paired host. The right side is the selected
session across five tabs: **Chat**, **Terminal**, **Git**, **Approvals** and **Files**.
Tool calls in the transcript collapse to one line and expand in place, so a long run
stays readable.

<p align="center">
  <img src="docs/assets/desktop/chat-expanded.png" width="700" alt="Desktop app — expanded tool calls in the transcript" />
</p>

<p align="center">
  <img src="docs/assets/desktop/files.png" width="700" alt="Desktop app — file tree and file viewer" />
</p>

## CLI

```bash
helios start                    # TUI: status, tunnel picker, pairing QR codes
helios new "refactor auth"      # create a session
helios sessions                 # list sessions
helios attach a3f1c2e8          # attach to a session's terminal
helios devices                  # list / approve / revoke paired devices
helios tunnel                   # start, stop or inspect the tunnel
helios logs                     # tail the daemon log
helios stop                     # stop the daemon
```

## How it works

The daemon runs each session in a terminal host it owns (`helios ptyhost`) and keeps the
output in memory, so any number of clients can read it at once. Clients hold no state:
the desktop app, the phone, the TUI and the CLI all talk to the same HTTP API and follow
the same SSE stream. Claude Code and Codex attach through the provider registry in
`internal/provider`, which is also where the next harness will plug in.

Diagrams, endpoints and the on-disk layout: [docs/architecture.md](docs/architecture.md).

## Requirements

- Go 1.26+ (the version in `go.mod`)
- A headless coding harness — Claude Code or OpenAI Codex today
- Node 20+ (only to build the desktop app from source)
- Flutter 3.32+ (only to build the mobile app from source)
- Xcode 15+ and a cabled iPhone (only to install the iOS app; it is never prebuilt)

## Docs

| | |
| --- | --- |
| [Setup walkthrough](docs/setup-walkthrough.md) | Every screen of the pairing flow |
| [Architecture](docs/architecture.md) | Diagrams, endpoints, file layout, tech stack |
| [iOS](docs/ios.md) | Building the iPhone app yourself |
| [Specs](docs/specs/README.md) | Design documents — intent, not merged state |
| [AGENTS.md](AGENTS.md) | Conventions and procedures for coding agents |

## Status

Shipping. The latest tag is `v1.5.5`; binaries, DMGs, an AppImage, a `.deb` and an APK
are attached to each [release](https://github.com/kamrul1157024/helios/releases/latest).

## License

[Elastic License 2.0 (ELv2)](LICENSE)
