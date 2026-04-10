# Implementation Plan: Daemon v0.1

## Goal

A working daemon that:
1. Receives Claude Code hook events (permission requests, stop, errors) via HTTP
2. Serves a React frontend where you can see pending permissions and approve/deny them
3. Uses SSE to push real-time updates to the browser
4. Stores state in SQLite
5. Authenticates remote clients via asymmetric JWT (Ed25519) with QR code device setup

**The core loop:** Claude needs permission → open browser → approve → Claude continues.

**Auth:** localhost access skips auth. Remote access (via any tunnel) requires JWT. New devices set up by scanning a QR code from the terminal.

## What We're Building

```
Claude Code (any session, any project)
    |
    |  HTTP hook fires (PermissionRequest, Stop, etc.)
    v
helios daemon (single Go binary, port 7654)
    |
    +-- Hook Handler: /hooks/*
    |     receives events from Claude (localhost only, no auth)
    |     creates notifications
    |     holds HTTP response for permission requests (blocking)
    |
    +-- Client API: /api/*  (auth required for remote)
    |     GET  /api/notifications        → list pending/resolved
    |     POST /api/notifications/:id/approve
    |     POST /api/notifications/:id/deny
    |     POST /api/notifications/batch
    |     GET  /api/events               → SSE stream
    |     GET  /api/health
    |     GET  /api/auth/devices         → list trusted devices
    |     DELETE /api/auth/devices/:kid  → revoke device
    |     POST /api/auth/verify          → verify JWT is valid
    |
    +-- Frontend: /*
    |     /           → setup page (PUBLIC, no auth)
    |                    QR scanner + manual key paste
    |     /dashboard  → notification list (AUTH REQUIRED)
    |                    approve/deny buttons, batch, SSE
    |
    +-- Auth: /api/auth/*
    |     Ed25519 keypair per device
    |     QR code generation in terminal (helios auth init)
    |     JWT validation middleware
    |     Device management in SQLite
    |
    +-- SQLite: ~/.helios/helios.db
          notifications, hook_sessions, devices tables
```

## Project Structure

```
helios/
    cmd/
        helios/
            main.go                 ← entry point
    internal/
        daemon/
            daemon.go               ← daemon lifecycle (start, stop, config)
        server/
            server.go               ← HTTP server setup, routing
            middleware.go           ← auth middleware (local bypass, JWT validation)
            hooks.go                ← hook endpoint handlers (/hooks/*)
            api.go                  ← client API handlers (/api/*)
            auth.go                 ← auth API handlers (/api/auth/*)
            sse.go                  ← SSE event broadcaster
        store/
            store.go                ← SQLite connection, migrations
            notifications.go        ← notification CRUD
            hook_sessions.go        ← claude session_id → hook mapping
            devices.go              ← device/key CRUD
        notifications/
            manager.go              ← notification lifecycle, approval routing
        auth/
            keypair.go              ← Ed25519 key generation, storage
            jwt.go                  ← JWT creation (for QR payload), validation
            qr.go                   ← QR code terminal rendering
            devices.go              ← device registration, revocation
    frontend/
        package.json
        src/
            App.tsx
            pages/
                Setup.tsx           ← QR scanner + manual paste (PUBLIC)
                Dashboard.tsx       ← notification list (AUTH REQUIRED)
            components/
                QRScanner.tsx       ← camera-based QR scanner
                KeyPasteInput.tsx   ← manual setup string input
                NotificationList.tsx
                NotificationCard.tsx
                BatchActions.tsx
            lib/
                auth.ts             ← Web Crypto key storage, JWT signing
                sse.ts              ← SSE connection with auth
                api.ts              ← authenticated fetch wrapper
    go.mod
    go.sum
    docs/
        specs/
        implementation/
```

## SQLite Schema

```sql
-- Notifications from Claude hooks
CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    claude_session_id TEXT NOT NULL,
    cwd TEXT NOT NULL,
    type TEXT NOT NULL,                      -- permission, done, error, idle
    status TEXT NOT NULL DEFAULT 'pending',  -- pending, approved, denied, dismissed
    tool_name TEXT,
    tool_input TEXT,                         -- JSON
    detail TEXT,
    resolved_at TEXT,
    resolved_source TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Track Claude sessions seen via hooks
CREATE TABLE hook_sessions (
    claude_session_id TEXT PRIMARY KEY,
    cwd TEXT NOT NULL,
    last_event TEXT,
    last_event_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Trusted devices (each has an Ed25519 public key)
CREATE TABLE devices (
    kid TEXT PRIMARY KEY,                    -- key ID: "device-001"
    name TEXT NOT NULL,                      -- "My Phone", "Work Laptop"
    public_key TEXT NOT NULL,                -- base64-encoded Ed25519 public key
    status TEXT NOT NULL DEFAULT 'active',   -- active, revoked
    last_seen_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_notifications_status ON notifications(status);
CREATE INDEX idx_notifications_type ON notifications(type);
CREATE INDEX idx_notifications_claude_session ON notifications(claude_session_id);
CREATE INDEX idx_devices_status ON devices(status);
```

## Auth Flow

### Device Setup (one-time per device)

```
Terminal:
    $ helios auth init --name "My Phone"
    |
    v
    1. Generate Ed25519 keypair
    2. Store public key in SQLite devices table (kid: "device-001")
    3. Build setup payload: helios://setup?key=<base64_private_key>&kid=device-001&v=1
    4. Render QR code in terminal
    5. Also print setup string for manual copy-paste
    6. Wait for first authenticated request from this device

Browser (setup page at /):
    Option A: scan QR with camera → extract key → store in IndexedDB
    Option B: paste setup string → parse → store in IndexedDB
    |
    v
    Key stored in browser via Web Crypto API (non-extractable)
    |
    v
    Browser signs a test JWT → POST /api/auth/verify
    |
    v
    Success → redirect to /dashboard
    Device shows as "connected" in terminal
```

### Request Authentication

```
Every API request from browser:
    1. Browser reads key from IndexedDB
    2. Signs JWT: { alg: "EdDSA", kid: "device-001" } + { exp: now+1h }
    3. Sends: Authorization: Bearer <jwt>

Daemon middleware:
    1. Request from localhost (127.0.0.1, ::1)?
         YES → skip auth, allow
    2. Path is / or /api/health or /api/auth/verify?
         YES → skip auth (public routes)
    3. Has Authorization: Bearer <token>?
         NO → 401
    4. Extract kid from JWT header
    5. Look up public key in SQLite where kid=X and status='active'
         NOT FOUND → 401
    6. Verify Ed25519 signature
    7. Check exp not passed
         EXPIRED → 401
    8. Update last_seen_at for device
    9. Allow request
```

### SSE with Auth

```
Browser opens SSE:
    GET /api/events
    Authorization: Bearer <jwt>

Daemon validates JWT on connection open.
Connection stays open for SSE events.
If token expires during connection → daemon drops it, browser reconnects with fresh JWT.
```

## Hook Installation

User adds hooks to Claude config. CLI command generates the config:

```bash
helios hooks install          # writes to ~/.claude/settings.json (global)
helios hooks install --local  # writes to .claude/settings.local.json (project)
helios hooks show             # prints the JSON to copy-paste manually
helios hooks remove           # removes helios hooks from settings
```

The hook config:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7654/hooks/permission",
            "timeout": 300
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7654/hooks/stop"
          }
        ]
      }
    ],
    "StopFailure": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7654/hooks/stop-failure"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "permission_prompt|idle_prompt",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7654/hooks/notification"
          }
        ]
      }
    ],
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7654/hooks/session-start"
          }
        ]
      }
    ],
    "SessionEnd": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7654/hooks/session-end"
          }
        ]
      }
    ]
  }
}
```

Hooks always target localhost — Claude and the daemon run on the same machine.

## Permission Approval Flow (the core loop)

```
1. Claude hits permission prompt
2. Claude fires HTTP hook: POST localhost:7654/hooks/permission
     Body: { session_id, tool_name, tool_input, cwd, ... }
3. Daemon:
     a. Upsert hook_sessions (track this claude session)
     b. Create notification in SQLite (status: pending)
     c. Broadcast SSE event: { type: "notification", ... }
     d. OS desktop notification (osascript on mac)
     e. HOLD the HTTP response (don't respond yet)
     f. Wait on a Go channel (chan) keyed by notification ID
4. Browser (local or remote, authenticated):
     a. Receives SSE event, shows notification card with [Approve] [Deny]
     b. User clicks Approve
     c. Browser signs JWT, sends: POST /api/notifications/{id}/approve
5. Daemon:
     a. Validate JWT (skip if localhost)
     b. Update notification in SQLite (status: approved)
     c. Send to the Go channel → unblocks the waiting hook handler
     d. Broadcast SSE event: { type: "notification_resolved", ... }
6. Hook handler unblocks:
     a. Returns HTTP response to Claude:
        { hookSpecificOutput: { hookEventName: "PermissionRequest",
          decision: { behavior: "allow" } } }
7. Claude receives approval, executes tool
```

## Go Dependencies

- `modernc.org/sqlite` — pure Go SQLite (no CGO needed)
- `github.com/golang-jwt/jwt/v5` — JWT parsing and validation
- `github.com/skip2/go-qrcode` — QR code generation
- Standard library: `crypto/ed25519`, `net/http`, `encoding/json`, `embed`

## Frontend Dependencies

- React 18 + TypeScript + Vite
- `html5-qrcode` — QR scanner using browser camera API
- No UI framework — plain CSS or tailwind, keep it minimal

## Implementation Steps

### Step 1: Go Project Scaffolding

- `go mod init github.com/kamrul1157024/helios`
- Create directory structure
- Basic `main.go` with subcommands: `helios daemon start`, `helios auth init`, `helios hooks install`
- Config loading from `~/.helios/config.yaml` with sensible defaults

### Step 2: SQLite Store

- Database initialization at `~/.helios/helios.db`
- Auto-migration (create tables if not exist)
- Notification CRUD (create, list by status/type, update status)
- Hook session tracking (upsert, lookup)
- Device CRUD (create, list, revoke, lookup by kid)

### Step 3: Auth — Keypair and JWT

- Ed25519 keypair generation (`crypto/ed25519`)
- Store public key in SQLite devices table
- Build QR payload: `helios://setup?key=<base64_private>&kid=<kid>&v=1`
- QR code terminal rendering (go-qrcode → PNG → terminal block characters)
- JWT validation: parse header for kid, lookup public key, verify EdDSA signature, check exp
- `helios auth init --name "My Phone"` command
- `helios auth devices` command (list devices)
- `helios auth revoke <kid>` command

### Step 4: HTTP Server and Auth Middleware

- Single server on port 7654
- Route groups: `/hooks/*` (no auth), `/api/auth/verify` (no auth), `/` (no auth), `/api/*` (auth for remote)
- Auth middleware: check source IP, if not localhost → validate JWT
- CORS headers for browser access

### Step 5: SSE Broadcaster

- In-memory hub with connected client list
- Broadcast method pushes events to all clients
- Client connect/disconnect handling
- `GET /api/events` endpoint (auth required for remote, JWT validated on connect)
- Heartbeat every 30s to keep connection alive

### Step 6: Hook Handlers

- `POST /hooks/permission` — creates notification, holds response, waits for decision via Go channel
- `POST /hooks/stop` — updates hook_session state, creates done notification
- `POST /hooks/stop-failure` — creates error notification
- `POST /hooks/notification` — creates idle/input notification
- `POST /hooks/session-start` — upserts hook_session
- `POST /hooks/session-end` — updates hook_session

The permission handler is the critical one — it blocks using a `chan string` per notification ID until approval/denial arrives or timeout (5 min).

### Step 7: Client API

- `GET /api/notifications` — list with query filters (?status=pending&type=permission)
- `POST /api/notifications/:id/approve` — approve, unblock hook, broadcast SSE
- `POST /api/notifications/:id/deny` — deny, unblock hook, broadcast SSE
- `POST /api/notifications/:id/dismiss` — dismiss non-permission notifications
- `POST /api/notifications/batch` — batch approve/deny multiple
- `GET /api/health` — health check (public, no auth)
- `GET /api/auth/devices` — list trusted devices
- `DELETE /api/auth/devices/:kid` — revoke device
- `POST /api/auth/verify` — verify a JWT is valid (public, for setup flow)

### Step 8: Desktop Notification

- On macOS: `osascript -e 'display notification ...'` when a permission notification is created
- Simple exec.Command, fire-and-forget
- Shows: "helios: Claude needs permission — Bash: npm run test"

### Step 9: React Frontend — Setup Page

- Route: `/` (public, no auth required)
- QR code scanner using browser camera (`html5-qrcode` library)
- Manual input field for pasting setup string
- On key received:
  1. Import Ed25519 private key into Web Crypto API as non-extractable
  2. Store CryptoKey handle in IndexedDB
  3. Sign a test JWT
  4. POST /api/auth/verify to confirm it works
  5. On success → redirect to /dashboard
- If key already in IndexedDB → skip setup, go to /dashboard

### Step 10: React Frontend — Auth Library

- `lib/auth.ts`:
  - `storeKey(privateKeyBase64, kid)` → import into Web Crypto, save to IndexedDB
  - `hasKey()` → check if key exists in IndexedDB
  - `signJWT()` → create JWT with header (alg: EdDSA, kid), payload (exp: now+1h), sign with Web Crypto
  - `getAuthHeader()` → returns `Bearer <jwt>` string
- `lib/api.ts`:
  - Wrapper around fetch that auto-attaches auth header
  - Handles 401 → redirect to setup page
- `lib/sse.ts`:
  - EventSource wrapper that adds auth (EventSource doesn't support headers, so use fetch-based SSE or pass token as query param)

### Step 11: React Frontend — Dashboard

- Route: `/dashboard` (auth required — redirects to `/` if no key in IndexedDB)
- Notification list:
  - PENDING section: cards with tool name, command/detail, [Approve] [Deny] buttons
  - COMPLETED section: resolved notifications
  - Real-time updates via SSE
- Batch actions:
  - Checkbox per notification
  - "Approve All" button
  - "Approve Selected" button
- Session grouping: notifications grouped by claude_session_id with cwd shown
- Auto-refresh on SSE events

### Step 12: Embed Frontend in Go Binary

- Build React app: `cd frontend && npm run build`
- Go embed: `//go:embed frontend/dist/*`
- Serve static files at `/` with SPA fallback (all non-API, non-hook routes → index.html)
- Makefile or build script: `make build` runs frontend build then go build

### Step 13: Hook Installer CLI

- `helios hooks install` — read existing `~/.claude/settings.json`, merge helios hooks, write back
- `helios hooks install --local` — same but for `.claude/settings.local.json`
- `helios hooks show` — print hook JSON to stdout for manual copy-paste
- `helios hooks remove` — remove helios hooks from settings
- Handles merging: if user already has hooks, append helios hooks without overwriting

### Step 14: Integration Test

- Start daemon
- Install hooks globally
- Run Claude in a test project
- Trigger a permission prompt
- Open browser at localhost:7654
- Verify notification appears
- Approve from browser
- Verify Claude continues
- Test remote auth:
  - Run `helios auth init`
  - Scan QR / paste setup string in browser
  - Access from non-localhost
  - Verify JWT auth works

## What v0.1 Does NOT Include

- No session management (no create/suspend/resume/kill)
- No tmux integration
- No TUI
- No channel plugins or channel proxy
- No auto-approve
- No provider abstraction
- No key rotation (revoke + new init is the workaround)

These are all future iterations. v0.1 is: hooks → daemon → browser (with auth) → approve.

## Config

```yaml
# ~/.helios/config.yaml
server:
  bind: "localhost"       # "0.0.0.0" to expose
  port: 7654

auth:
  enabled: true           # master switch
  skip_local: true        # localhost skips auth (default true)

db:
  path: "~/.helios/helios.db"
```

Defaults work with zero config. The daemon creates `~/.helios/` on first run.
