# Setup walkthrough

The short version lives in the [README](../README.md#setup). This page shows every
screen you will see, in order.

## Prerequisites

```bash
brew install go                     # Go (to build helios)

# Pick ONE tunnel provider — this is how your phone reaches helios:
brew install tailscale              # Tailscale (recommended) — or the Mac app from
                                    # https://tailscale.com/download
# or
brew install cloudflared            # Cloudflare Tunnel (public URL, changes on every restart)
# or
brew install ngrok                  # ngrok (free tier, requires signup at ngrok.com)
```

## Step 1 — Install the binary

```bash
$ make install
```

```
┌──────────────────────────────────────────────┐
│  $ make install                              │
│  go build -o helios ./cmd/helios/            │
│  helios installed to ~/.local/bin/helios     │
└──────────────────────────────────────────────┘
```

## Step 2 — Start helios

```bash
$ helios start
```

The TUI checks your environment and walks you through setup:

```
┌──────────────────────────────────────────────┐
│                                              │
│  helios                                      │
│                                              │
│    ✓ Daemon running                          │
│    ✓ Claude hooks installed                  │
│    ✗ No tunnel configured                    │
│    · No devices registered                   │
│                                              │
│    enter continue  q quit                    │
│                                              │
└──────────────────────────────────────────────┘
```

## Step 3 — Pick a tunnel

Your phone needs a way to reach your machine. Pick a tunnel provider:

```
┌────────────────────────────────────────────────────────────┐
│                                                            │
│  helios — Tunnel Setup                                     │
│                                                            │
│    How will your phone connect?                            │
│                                                            │
│    > Tailscale Serve (recommended)            ready        │
│        Private to your tailnet. Needs the Tailscale VPN    │
│        switched ON on your phone.                          │
│      Tailscale Funnel                         ready        │
│      Cloudflare Tunnel                        needs setup  │
│      zrok (open-source, stable URLs)          needs setup  │
│      ngrok                                    needs setup  │
│      localtunnel (zero signup)                needs setup  │
│      localhost.run (no install — uses SSH)    ready        │
│      localxpose (regional, reserved domains)  needs setup  │
│      Local Network (no HTTPS)                 ready        │
│      Custom URL                               ready        │
│                                                            │
│    ↑/↓ navigate  enter select  q quit                      │
│                                                            │
└────────────────────────────────────────────────────────────┘
```

The right-hand column is checked live, so a provider that needs a login or an
install says so before you pick it.

**Tailscale Serve is the recommendation.** It keeps the daemon inside your
tailnet — nothing is published to the internet, the hostname never changes
between restarts, and there is no certificate to manage. The cost is that the
Tailscale VPN has to be switched on at the phone end.

Pick **Tailscale Funnel** instead if you want a stable URL reachable without
the VPN. Funnel is public, and it needs HTTPS certificates enabled for the
tailnet, which publishes this machine's name to public Certificate Transparency
logs. Serve does not.

Everything else is a public tunnel. Cloudflare is the easiest of them — no
account — but its URL changes on every restart, so every paired device has to
be re-pointed.

## Step 4 — Main dashboard with QR codes

Once the tunnel connects, the dashboard shows two QR codes:

```
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  helios                                                  │
│                                                          │
│    ✓ Daemon running                                      │
│    ✓ Claude hooks installed                              │
│    ✓ Tunnel: https://macbook.tail4c2f.ts.net             │
│                                                          │
│    · No devices connected yet.                           │
│                                                          │
│    Download app:                                         │
│    ┌─────────────────────────────────┐                   │
│    │  ▄▄▄▄▄▄▄ ▄▄ ▄ ▄▄▄▄ ▄▄▄▄▄▄▄    │                   │
│    │  █ ▄▄▄ █ ▄▀██▀▄▀▄  █ ▄▄▄ █    │  ← scan with      │
│    │  █ ███ █ ▀█▄▀ ▀█ ▄ █ ███ █    │    phone camera    │
│    │  █▄▄▄▄▄█ ▄ █▄▀ █ ▄ █▄▄▄▄▄█    │    to download     │
│    │  ▄▄▄▄▄ ▄▄▄▀▄ ▄▀  ▄ ▄ ▄ ▄ ▄    │    the app         │
│    │  █▄▄▄▄▄█ ▀▄▀▄ ▀▄  █▄▄▄▄▄▄█    │                   │
│    │  ▀▀▀▀▀▀▀ ▀ ▀▀ ▀ ▀▀ ▀▀▀▀▀▀▀    │                   │
│    └─────────────────────────────────┘                   │
│    https://macbook.tail4c2f.ts.net                       │
│                                                          │
│    Pair a new device:                                    │
│    ┌─────────────────────────────────┐                   │
│    │  ▄▄▄▄▄▄▄ ▄ ▄▄ ▄▄▄  ▄▄▄▄▄▄▄    │                   │
│    │  █ ▄▄▄ █ █▀▀▄█ █▀▄ █ ▄▄▄ █    │  ← scan from      │
│    │  █ ███ █ ██▀▄ ▀ ▀▄ █ ███ █    │    inside the      │
│    │  █▄▄▄▄▄█ █ ▀▄█▄█ ▄ █▄▄▄▄▄█    │    helios app      │
│    │  ▄▄  ▄ ▄▄▄▀ ▀▄▄ ▄▄ ▄ ▄ ▄ ▄    │                   │
│    │  █▄▄▄▄▄█ ▄▀▀█▄ ▀█  █▄▄▄▄▄▄█    │                   │
│    │  ▀▀▀▀▀▀▀ ▀▀ ▀ ▀▀▀  ▀▀▀▀▀▀▀    │                   │
│    └─────────────────────────────────┘                   │
│    Expires in 1:42  (auto-refreshes)                     │
│                                                          │
│    q quit                                                │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

## Step 5 — Download the app (QR 1)

Scan the **Download QR** with your phone camera. It opens a landing page:

```
┌─────────────────────────┐
│ ┌─────────────────────┐ │
│ │ ◀  abc.trycloudfl.. │ │
│ └─────────────────────┘ │
│                         │
│                         │
│         Helios          │
│  Orchestrate AI coding  │
│  agents from your phone │
│                         │
│  ┌───────────────────┐  │
│  │                   │  │
│  │  Download for     │  │
│  │  Android          │  │
│  │  APK              │  │
│  │                   │  │
│  └───────────────────┘  │
│                         │
│  ┌───────────────────┐  │
│  │                   │  │
│  │  Download for     │  │
│  │  macOS            │  │
│  │  DMG              │  │
│  │                   │  │
│  └───────────────────┘  │
│                         │
│  ┌───────────────────┐  │
│  │ How to connect    │  │
│  │ 1. Download app   │  │
│  │ 2. Run helios     │  │
│  │    start          │  │
│  │ 3. Scan pairing   │  │
│  │    QR code        │  │
│  └───────────────────┘  │
│                         │
└─────────────────────────┘
```

## Step 6 — Pair your device (QR 2)

Open the Helios app and scan the **Pairing QR**:

```
┌─────────────────────────┐
│          helios          │
│                         │
│  ┌───────────────────┐  │
│  │                   │  │
│  │                   │  │
│  │   ┌───────────┐   │  │
│  │   │           │   │  │
│  │   │  CAMERA   │   │  │
│  │   │ VIEWFINDER│   │  │
│  │   │           │   │  │
│  │   │   [ ]     │   │  │
│  │   │           │   │  │
│  │   └───────────┘   │  │
│  │                   │  │
│  │                   │  │
│  └───────────────────┘  │
│                         │
│  Scan the QR code from  │
│  your terminal          │
│                         │
│  Run helios start in    │
│  your terminal to       │
│  generate a QR code     │
│                         │
│  Paste URL manually     │
│                         │
└─────────────────────────┘
```

## Step 7 — Approve the device

The app registers and waits. The terminal asks you to confirm:

```
┌─────────────────────────┐          ┌──────────────────────────────────────────────┐
│                         │          │                                              │
│  ┌───────────────────┐  │          │  helios — New Device                         │
│  │                   │  │          │                                              │
│  │  helios           │  │          │    A device wants to pair:                   │
│  │  Setting up...    │  │          │                                              │
│  │                   │  │          │      Name:     Android — Helios App          │
│  │  + Generating     │  │          │      Platform: Android                       │
│  │    keys...        │  │          │      KID:      a1b2c3d4-e5f6                 │
│  │  + Registering    │  │          │                                              │
│  │    device...      │  │          │    Allow this device?                        │
│  │  + Authenticating │  │          │                                              │
│  │  + Waiting for    │  │          │    y approve  n reject                       │
│  │    approval...    │  │          │                                              │
│  │                   │  │          └──────────────────────────────────────────────┘
│  │  ┌─────────────┐  │  │
│  │  │  Press "y"  │  │  │                       press y
│  │  │ in terminal │  │  │                          │
│  │  │ to approve  │  │  │                          ▼
│  │  └─────────────┘  │  │
│  │                   │  │          ┌──────────────────────────────────────────────┐
│  │       ⟳           │  │          │  helios                                      │
│  │                   │  │          │                                              │
│  └───────────────────┘  │          │    ✓ Daemon running                          │
│                         │          │    ✓ Claude hooks installed                  │
└─────────────────────────┘          │    ✓ Shell wrapper (zsh)                     │
                                     │    ✓ Tunnel: https://macbook.tail4c2f...     │
        Phone                        │                                              │
                                     │    * Android — Helios App  push:on  just now │
                                     │                                              │
                                     └──────────────────────────────────────────────┘

                                                    Terminal
```

## Step 8 — You're in

The app navigates to the dashboard. Start a session and control Claude from your phone:

```bash
$ helios new "fix the auth bug in login.go"
```

```
┌──────────────────────────────────────────────┐
│  Session a3f1c2e8 started                    │
│    cwd: /Users/you/workspace/myapp           │
│    Attach with: helios attach a3f1c2e8       │
└──────────────────────────────────────────────┘
```
