# Remote Access & Authentication

## Overview

Helios daemon is an HTTP server. It doesn't know or care how it's exposed to the internet — that's the user's choice (ngrok, cloudflare tunnel, tailscale, wireguard, direct port forward, whatever). Helios just knows: **is this request local or remote?**

## Server Binding

```yaml
# ~/.helios/config.yaml
server:
  bind: "localhost"    # default: local only
  port: 7654

auth:
  enabled: true        # master switch
  skip_local: true     # localhost requests skip auth (default true)
```

```bash
# Local only (default)
helios daemon start
# → localhost:7654, no auth needed

# Bind to all interfaces (LAN, tunnel, etc.)
helios daemon start --bind 0.0.0.0
# → 0.0.0.0:7654, remote requests need JWT

# User exposes however they want — helios doesn't care
ngrok http 7654                          # user's choice
cloudflared tunnel --url localhost:7654   # user's choice
tailscale funnel 7654                    # user's choice
# helios sees requests, checks local vs remote, enforces auth
```

## Auth Model: Asymmetric JWT

Server holds public key. Client holds private key. No login endpoint. No passwords. No token exchange.

1. Server generates Ed25519 keypair
2. Client gets private key (via QR scan)
3. Client signs short-lived JWTs locally
4. Server validates with public key

## Auth Middleware

```
Request arrives
    |
    +-- From localhost? (127.0.0.1, ::1)
    |     YES → skip auth, allow
    |     NO  → check JWT
    |
    +-- Has Authorization: Bearer <token>?
    |     NO  → 401
    |     YES → validate signature, expiry, kid
    |
    +-- Valid?
          NO  → 401
          YES → allow, attach device info to context
```

## The Web Frontend

Helios serves a React SPA. The frontend has two zones:

### Public Zone (no auth)

- **Setup page** — QR scanner + URL input field. This is the only public page.
- **Health endpoint** — `GET /api/health`

### Authenticated Zone (JWT required)

- Everything else — session list, notifications, send message, etc.
- Frontend stores private key in browser (IndexedDB via Web Crypto API)
- Frontend signs JWTs in-browser before every API call
- If no key stored → redirects to setup page

### Frontend Architecture

```
https://your-helios-url.com/

    +-- / (setup page - PUBLIC)
    |     |
    |     +-- QR code scanner camera view
    |     +-- OR manual URL + key input field
    |     +-- On success: stores private key, redirects to /dashboard
    |
    +-- /dashboard (AUTH REQUIRED)
    |     +-- session list, status, badges
    |     +-- create new session
    |
    +-- /notifications (AUTH REQUIRED)
    |     +-- pending permissions with approve/deny
    |     +-- batch operations
    |
    +-- /sessions/:id (AUTH REQUIRED)
          +-- session detail
          +-- send message input
          +-- auto-approve mode toggle
```

### How Auth Works in Browser

```
First visit → / (setup page)
    |
    +-- Option A: scan QR code with phone camera
    |     QR contains: helios://setup?key=<base64_private_key>&kid=<key_id>
    |     But wait — you're ON the phone browser scanning with camera
    |     → extracts key, stores in IndexedDB
    |
    +-- Option B: paste setup string manually
    |     User copies from terminal: helios://setup?key=...&kid=...
    |     → parses, stores in IndexedDB
    |
    +-- Option C: scan QR from another device
    |     Open helios on laptop browser
    |     Terminal shows QR on workstation
    |     Laptop camera scans QR
    |     → stores key in laptop browser
    |
    v
Key stored in browser (IndexedDB, non-extractable via Web Crypto API)
    |
    v
Redirect to /dashboard
    |
    v
Every API call:
    1. Browser JS generates JWT (sign with stored private key)
    2. Sets Authorization: Bearer <jwt>
    3. Sends request
    4. Server validates
```

## QR Code Setup Flow

### Step 1: Generate on Server

```
$ helios auth init

  Helios Device Setup
  -------------------

  Scan this QR code with your browser or mobile device:

  ██████████████████████████████████
  ██ ▄▄▄▄▄ █▀▄▀▄██ ▄▄▄▄▄ █████████
  ██ █   █ █▄▀▄███ █   █ █████████
  ██ █▄▄▄█ █ ▀▄▀██ █▄▄▄█ █████████
  ██████████████████████████████████

  Or copy this setup string:
  helios://setup?key=base64key&kid=device-001

  Key ID: device-001
  Expires: 10 minutes

  [r] Regenerate  [q] Cancel
```

### Step 2: Client Receives Key

**Browser (setup page):**
- Camera scans QR → extracts payload → stores key → redirects to dashboard

**Mobile browser:**
- Same flow. No native app needed. PWA-capable.

**Paste manually:**
- Copy setup string from terminal → paste into setup page input → same result

### QR Payload

```
helios://setup?key=<base64_ed25519_private_key>&kid=<key_id>&v=1
```

Ed25519 private key is 32 bytes → 44 characters base64. Fits easily in a QR code.

## Device Management

```
$ helios auth init --name "My Phone"         # generate new device keypair
$ helios auth devices                         # list all devices

Key ID        Name               Status    Last Seen
----------    ----------------   ------    ---------
device-001    My Phone           active    2m ago
device-002    Work Laptop        active    2h ago

$ helios auth revoke device-002              # revoke a device
$ helios auth rotate device-001              # new keypair, show new QR
```

### Revocation

Revoking a device = deleting its public key from `~/.helios/auth/trusted_keys/`. Immediate. Next request from that device gets 401.

## Key Storage by Client Type

| Client | Where private key is stored | Security |
|--------|---------------------------|----------|
| Browser | IndexedDB via Web Crypto API (non-extractable) | Good — key operations happen in browser crypto engine, JS can't read raw bytes |
| Mobile browser (PWA) | Same as browser | Same |
| Native mobile app (future) | iOS Keychain / Android Keystore | Best — hardware-backed, biometric-gated |
| CLI on remote machine | File at ~/.helios/client/key with 600 perms | OK — standard file permissions |

## JWT Details

### Structure

Header:
```json
{"alg": "EdDSA", "typ": "JWT", "kid": "device-001"}
```

Payload:
```json
{"iat": 1712678400, "exp": 1712682000, "sub": "helios-client"}
```

Signed with Ed25519 private key. TTL: 1 hour. Client signs a fresh JWT before each request (or caches for a few minutes).

### Server Validation

1. Extract `kid` from header
2. Find public key in `~/.helios/auth/trusted_keys/<kid>.pub`
3. Verify Ed25519 signature
4. Check `exp` not passed
5. If kid not found or revoked → 401

## Key Rotation (v0.2)

```
helios auth rotate device-001
    |
    +-- Generate new Ed25519 keypair
    +-- Store new public key (old key valid for 24h grace period)
    +-- Show new QR code in terminal
    +-- User scans new QR on device
    +-- New key active immediately
    +-- Old key expires after grace period
```

## What Helios Does NOT Do

- Does NOT manage tunnels (ngrok, cloudflare, tailscale)
- Does NOT know its public URL
- Does NOT handle TLS (tunnel/reverse proxy handles that)
- Does NOT have a login page or password
- Does NOT have OAuth, OIDC, or any external auth provider
- Does NOT have user accounts (single-user tool, multiple devices)

Helios is a single-user tool. "Auth" means "is this device trusted?" not "which user is this?"

## Endpoints Summary

```
PUBLIC (no auth):
    GET  /                          → serves setup page (React SPA entry)
    GET  /api/health                → health check
    POST /api/auth/verify           → verify a JWT is valid (for client self-check)

AUTH REQUIRED (JWT):
    GET    /api/sessions            → list sessions
    POST   /api/sessions            → create session
    GET    /api/sessions/:id        → session details
    DELETE /api/sessions/:id        → kill session
    PATCH  /api/sessions/:id        → update session
    POST   /api/sessions/:id/suspend  → suspend
    POST   /api/sessions/:id/resume   → resume
    POST   /api/sessions/:id/message  → send message
    GET    /api/notifications       → list notifications
    POST   /api/notifications/:id/approve  → approve
    POST   /api/notifications/:id/deny     → deny
    POST   /api/notifications/batch        → batch action
    GET    /api/events              → SSE stream
    GET    /api/channels            → list channels
    GET    /api/auth/devices        → list devices
    DELETE /api/auth/devices/:kid   → revoke device

LOCALHOST ONLY (skip auth):
    All of the above without JWT when accessed from 127.0.0.1
```

## Frontend Serving

The React frontend is embedded in the Go binary using `go:embed`. Single binary serves both API and frontend. No separate deployment.

```
helios daemon start
    → serves API on /api/*
    → serves React SPA on /* (with fallback to index.html for client-side routing)
```

Build process:
1. Build React app → produces static files in `frontend/build/`
2. Go binary embeds `frontend/build/` via `//go:embed`
3. Single `helios` binary contains everything
