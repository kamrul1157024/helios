# 22 — Setup Flow & Security Architecture

## Problem

Current setup requires multiple manual steps:
1. `helios daemon start`
2. `helios hooks install`
3. `helios auth init` → copy string manually to phone
4. Manually start a tunnel for remote access
5. No guidance on HTTPS (required for push notifications)

Hooks endpoints (`/hooks/*`) have no auth — if exposed via tunnel, anyone can send fake permission requests or approve/deny on behalf of the user.

## Solution: Two-Server Architecture + `helios setup` TUI

### Two HTTP Servers in One Daemon Process

```
Internal Server (127.0.0.1:7654)        Public Server (0.0.0.0:7655)
──────────────────────────────────      ──────────────────────────────
Only reachable from localhost.           Exposed via tunnel to the internet.
Used by Claude hooks and CLI.            Used by phone/browser.
No auth required (localhost trusted).    Cookie-based JWT auth.

Hooks (Claude):                          No auth:
  POST /hooks/permission                   GET  /#/setup?key=...&kid=...
  POST /hooks/stop                         POST /api/auth/login (sets cookie)
  POST /hooks/stop-failure                 GET  /api/health
  POST /hooks/notification
  POST /hooks/session-start              Cookie auth required:
  POST /hooks/session-end                  GET  /                (frontend SPA)
                                           GET  /api/push/vapid-public-key
Admin (CLI):                               GET  /api/notifications
  GET  /internal/health                    POST /api/notifications/batch
  GET  /internal/tunnel/status             POST /api/notifications/{id}/approve
  POST /internal/tunnel/start              POST /api/notifications/{id}/deny
  POST /internal/tunnel/stop               POST /api/notifications/{id}/dismiss
  POST /internal/device/create             GET  /api/events (SSE)
  GET  /internal/device/list               POST /api/push/subscribe
  POST /internal/device/revoke             POST /api/push/unsubscribe
                                           GET  /api/auth/devices
                                           DELETE /api/auth/devices/{kid}
                                           GET  /api/auth/device/me
                                           POST /api/auth/device/me
```

**Internal server** — `127.0.0.1` only. No auth on any endpoint (localhost is trusted). Claude hooks and CLI admin operations.

**Public server** — `0.0.0.0`, exposed via tunnel. Cookie-based JWT auth on everything except setup page, login, and health check.

---

### Cookie-Based Auth Flow

The public server uses `HttpOnly` cookies instead of `Authorization` headers. This means:
- Browser automatically sends the cookie on every request (API, page loads, SSE)
- Service worker gets the cookie too (same origin)
- No need for the frontend to manually manage tokens
- `HttpOnly` flag prevents JavaScript from reading the cookie (XSS protection)

**Login flow (happens once during device setup):**

```
1. Phone scans QR → opens https://tunnel/#/setup?key=...&kid=...
2. Setup page imports Ed25519 private key into IndexedDB
3. Setup page signs a JWT using the private key
4. Frontend calls POST /api/auth/login with JWT in body
5. Server verifies JWT signature against stored public key
6. Server sets HttpOnly cookie: Set-Cookie: helios_token=<JWT>; HttpOnly; Secure; SameSite=Strict; Path=/
7. All subsequent requests use the cookie automatically
8. Redirect to device naming, then dashboard
```

**Cookie details:**
- Name: `helios_token`
- Value: JWT signed by device's Ed25519 private key
- Flags: `HttpOnly`, `Secure` (HTTPS via tunnel), `SameSite=Strict`, `Path=/`
- Expiry: long-lived (30 days), refreshed on each login
- The JWT contains `kid` (device key ID), `iat`, `exp`

**Auth middleware on public server:**
1. Read `helios_token` cookie from request
2. Validate JWT signature using public key from DB (looked up by `kid` in JWT header)
3. If invalid or missing → redirect to `/#/setup` (for page requests) or 401 (for API requests)
4. If valid → proceed, update device `last_seen`

**Setup page exception:**
- The public server serves the frontend SPA for all paths
- The SPA checks if auth cookie exists → dashboard or setup page
- `POST /api/auth/login` is the only API endpoint that doesn't require the cookie (it creates it)
- `GET /api/health` also doesn't require auth (health check)

---

### Internal Admin API (`/internal/*`)

These endpoints are used by the CLI (`helios setup`, `helios devices`, etc.) to manage the daemon. They are NOT exposed via tunnel. No auth required (localhost trusted).

```
GET  /internal/health
  Response: { "status": "ok", "internal_port": 7654, "public_port": 7655 }

GET  /internal/tunnel/status
  Response: {
    "active": true,
    "provider": "cloudflare",
    "public_url": "https://abc-xyz.trycloudflare.com"
  }

POST /internal/tunnel/start
  Body: { "provider": "cloudflare" }
        { "provider": "custom", "url": "https://my-domain.com" }
  Response: { "public_url": "https://abc-xyz.trycloudflare.com" }
  Notes:
    - Stops any existing tunnel first (one at a time)
    - Starts tunnel binary as child process
    - Waits for URL to be available (up to 30s)
    - Saves provider to config.yaml
    - For "local": discovers LAN IP, returns http://<ip>:7655 (no tunnel binary)
    - For "custom": just saves the URL, no child process

POST /internal/tunnel/stop
  Response: { "stopped": true }

POST /internal/device/create
  Body: {}
  Response: {
    "kid": "device-001",
    "key": "<base64url ed25519 private key seed>",
    "setup_url": "https://abc-xyz.trycloudflare.com/#/setup?key=...&kid=..."
  }
  Notes:
    - Generates Ed25519 keypair
    - Stores public key + device in DB (name empty, status "pending")
    - Returns private key (one-time, never stored on server)
    - setup_url includes current tunnel URL
    - Device name is NOT set here — device sets its own name after connecting

GET  /internal/device/list
  Response: {
    "devices": [
      {
        "kid": "device-001",
        "name": "Kamrul's iPhone",
        "platform": "iOS",
        "browser": "Safari 17",
        "status": "active",
        "push_enabled": true,
        "last_seen_at": "2026-04-10T05:30:00Z",
        "created_at": "2026-04-10T04:00:00Z"
      }
    ]
  }

POST /internal/device/revoke
  Body: { "kid": "device-001" }
  Response: { "revoked": true }
```

---

### Public API — Device Self-Registration

After a device connects via QR setup, it registers its own metadata. This replaces CLI-side device naming.

```
POST /api/auth/login                    (no auth — creates the cookie)
  Body: { "token": "<JWT signed by device private key>" }
  Response: 200 OK (sets Set-Cookie header)
  Server-side:
    1. Parse JWT, extract kid from header
    2. Look up device public key by kid
    3. Verify JWT signature
    4. Update device status from "pending" to "active"
    5. Set cookie: Set-Cookie: helios_token=<JWT>; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=2592000
    6. Return { "success": true, "kid": "device-001" }

GET  /api/auth/device/me                (cookie auth — device reads its own info)
  Response: {
    "kid": "device-001",
    "name": "Kamrul's iPhone",
    "platform": "iOS",
    "browser": "Safari 17",
    "push_enabled": true,
    "last_seen_at": "...",
    "created_at": "..."
  }

POST /api/auth/device/me               (cookie auth — device updates its metadata)
  Body: {
    "name": "Kamrul's iPhone",          // user-provided or auto-detected
    "platform": "iOS",                  // auto-detected from user-agent
    "browser": "Safari 17"              // auto-detected from user-agent
  }
  Response: { "success": true }
  Notes:
    - Device can only update its own metadata (kid from cookie JWT)
    - Called automatically after setup with auto-detected values
    - User can optionally name the device in the UI
```

---

### Device DB Schema

```sql
CREATE TABLE devices (
    kid          TEXT PRIMARY KEY,
    name         TEXT DEFAULT '',
    public_key   TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',   -- pending | active | revoked
    platform     TEXT DEFAULT '',                    -- iOS | Android | macOS | Windows | Linux
    browser      TEXT DEFAULT '',                    -- Safari 17 | Chrome 120
    last_seen_at TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
```

`push_enabled` is derived at query time by checking if the device has a row in `push_subscriptions` (joined by `device_kid`).

---

### Tunnel Interface

```go
// internal/tunnel/tunnel.go
type Tunnel interface {
    Start(ctx context.Context, localPort int) error
    Stop() error
    URL() string
    Provider() string
}
```

**Implementations:**

| Provider | Binary | How URL is discovered |
|----------|--------|----------------------|
| cloudflare | `cloudflared` | Parse stderr for `https://*.trycloudflare.com` |
| ngrok | `ngrok` | Call `http://127.0.0.1:4040/api/tunnels` after start |
| tailscale | `tailscale` | Run `tailscale status --json`, extract DNS name |
| local | (none) | Discover LAN IP via `net.InterfaceAddrs()` |
| custom | (none) | User provides URL, no process to manage |

The daemon manages **one** active tunnel. Switching provider stops the old one first.

---

### `helios setup` — TUI Flow (bubbletea)

```
FLOW A: First Time Setup
═════════════════════════

$ helios setup

Screen 1: Status Check
┌─────────────────────────────────────────┐
│  helios — Setup                         │
│                                         │
│  Checking environment...                │
│                                         │
│  ✓ Daemon running (7654/7655)           │
│  ✓ Claude hooks installed               │
│  ✗ No tunnel configured                 │
│  ✗ No devices registered                │
│                                         │
│  enter continue                         │
└─────────────────────────────────────────┘
                  │
                  ▼
Screen 2: Tunnel Provider
┌─────────────────────────────────────────┐
│  helios — Tunnel Setup                  │
│                                         │
│  How will your phone connect?           │
│                                         │
│  > ● Cloudflare Tunnel (recommended)    │
│    ○ ngrok                              │
│    ○ Tailscale                          │
│    ○ Local Network (no HTTPS)           │
│    ○ Custom URL                         │
│                                         │
│  ↑/↓ navigate  enter select             │
└─────────────────────────────────────────┘
                  │
                  ▼ (if binary not found)
Screen 2b: Binary Not Found
┌─────────────────────────────────────────┐
│  helios — Tunnel Setup                  │
│                                         │
│  ✗ cloudflared not found                │
│                                         │
│  Install it:                            │
│    brew install cloudflared              │
│                                         │
│  enter retry  q go back                 │
└─────────────────────────────────────────┘
                  │
                  ▼
Screen 3: Tunnel Starting
┌─────────────────────────────────────────┐
│  helios — Tunnel Setup                  │
│                                         │
│  Starting cloudflare tunnel... ⣾        │
│                                         │
└─────────────────────────────────────────┘
                  │
                  ▼
Screen 4: QR Code + Waiting
┌─────────────────────────────────────────┐
│  helios — Scan with your phone          │
│                                         │
│      ┌───────────────────────┐          │
│      │                       │          │
│      │      ▄▄▄ █▄█ ▄▄▄     │          │
│      │      █▄█ ▄▄▄ █▄█     │          │
│      │      ▄▄▄ █▄█ ▄▄▄     │          │
│      │                       │          │
│      └───────────────────────┘          │
│                                         │
│  https://abc-xyz.trycloudflare.com      │
│                                         │
│  Waiting for device to connect... ⣾     │
│                                         │
│  q quit                                 │
└─────────────────────────────────────────┘
                  │
                  │ (polls /internal/device/list
                  │  until device status = "active")
                  │
                  ▼
Screen 5: Success
┌─────────────────────────────────────────┐
│  helios — Setup Complete!               │
│                                         │
│  ✓ Daemon running                       │
│  ✓ Claude hooks installed               │
│  ✓ Tunnel active (cloudflare)           │
│  ✓ Device connected (Kamrul's iPhone)   │
│                                         │
│  Your phone will now receive push       │
│  notifications when Claude needs        │
│  permission.                            │
│                                         │
│  enter exit                             │
└─────────────────────────────────────────┘


FLOW B: Already Set Up (re-running setup)
══════════════════════════════════════════

$ helios setup

┌─────────────────────────────────────────┐
│  helios — Setup                         │
│                                         │
│  ✓ Daemon running (7654/7655)           │
│  ✓ Claude hooks installed               │
│  ✓ Tunnel active (cloudflare)           │
│  ✓ 1 device connected                   │
│                                         │
│  What would you like to do?             │
│                                         │
│  > ● Add another device                 │
│    ○ Change tunnel provider             │
│    ○ Reset (revoke all devices)         │
│    ○ Exit                               │
│                                         │
│  ↑/↓ navigate  enter select             │
└─────────────────────────────────────────┘
                  │
                  ▼ (Add another device)
   Creates new keypair via /internal/device/create
   Goes directly to QR screen (tunnel already running)
```

---

### Phone-Side Flow (what user sees after scanning QR)

```
Phone camera scans QR
        │
        ▼
Opens browser at:
  https://abc-xyz.trycloudflare.com/#/setup?key=...&kid=...
        │
        ▼
┌─────────────────────────────┐
│                             │
│      helios                 │
│                             │
│  Setting up device...       │
│                             │
│  ✓ Key imported             │
│  ✓ Authenticated            │
│  ⣾ Detecting device...      │
│                             │
└─────────────────────────────┘
        │
        │  Auto-detects platform + browser from user-agent
        │  Calls POST /api/auth/device/me with metadata
        │
        ▼
┌─────────────────────────────┐
│                             │
│      helios                 │
│                             │
│  ✓ Connected!               │
│                             │
│  Name this device:          │
│  ┌─────────────────────┐   │
│  │ iPhone — Safari      │   │  (pre-filled from user-agent)
│  └─────────────────────┘   │
│                             │
│  [Save & Continue]          │
│                             │
└─────────────────────────────┘
        │
        ▼
┌─────────────────────────────┐
│  ┌───────────────────────┐  │
│  │ "abc-xyz.trycloudflare│  │
│  │  .com" wants to send  │  │
│  │  you notifications    │  │
│  │                       │  │
│  │  [Block]    [Allow]   │  │
│  └───────────────────────┘  │
│                             │
│  Browser permission prompt  │
└─────────────────────────────┘
        │
        ▼ (user taps Allow)
┌─────────────────────────────┐
│                             │
│      helios                 │
│                             │
│  ✓ All set!                 │
│                             │
│  Add to home screen for     │
│  the best experience.       │
│                             │
│  [Add to Home Screen]       │
│                             │
│  [Go to Dashboard]          │
│                             │
└─────────────────────────────┘
        │
        ▼
┌─────────────────────────────┐
│                             │
│  helios          Push ON    │
│                             │
│  No pending permissions.    │
│                             │
│  History                    │
│  ┌─────────────────────┐   │
│  │ approved  Bash       │   │
│  │ git status           │   │
│  │ 5m ago  via browser  │   │
│  └─────────────────────┘   │
│                             │
│  Dashboard — ready to go    │
└─────────────────────────────┘


Push Notification on Phone:
───────────────────────────

┌──────────────────────────────────┐
│  helios                    now   │
│  Claude needs permission         │
│  Bash: npm test                  │
│                                  │
│  [Approve]         [Deny]        │
└──────────────────────────────────┘

User taps [Approve]
  → Service worker calls POST /api/notifications/{id}/approve
  → Cookie sent automatically (same origin)
  → Server approves, unblocks Claude
  → Notification dismissed
```

---

### `helios devices` — Device Management TUI

```
$ helios devices

Screen 1: Device List
┌─────────────────────────────────────────────────────┐
│  helios — Devices                                   │
│                                                     │
│  ┌─────────────────────────────────────────────┐   │
│  │  ● Kamrul's iPhone                          │   │
│  │    device-001 · iOS · Safari                │   │
│  │    Last seen: 2m ago · Push: ON             │   │
│  └─────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────┐   │
│  │  ○ Work iPad                                │   │
│  │    device-002 · iPadOS · Safari             │   │
│  │    Last seen: 3h ago · Push: ON             │   │
│  └─────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────┐   │
│  │  ○ Home MacBook                             │   │
│  │    device-003 · macOS · Chrome              │   │
│  │    Last seen: 1d ago · Push: OFF            │   │
│  └─────────────────────────────────────────────┘   │
│                                                     │
│  ↑/↓ navigate  enter details  a add  q quit        │
└─────────────────────────────────────────────────────┘
                  │
                  ▼ (press enter on a device)
Screen 2: Device Detail
┌─────────────────────────────────────────────────────┐
│  helios — Device: Kamrul's iPhone                   │
│                                                     │
│  Key ID:     device-001                             │
│  Platform:   iOS                                    │
│  Browser:    Safari 17                              │
│  Status:     active                                 │
│  Push:       enabled                                │
│  Last seen:  2 minutes ago                          │
│  Created:    Apr 10, 2026                           │
│                                                     │
│  r revoke   b back                                  │
└─────────────────────────────────────────────────────┘
                  │
                  ▼ (press r)
Screen 3: Revoke Confirmation
┌─────────────────────────────────────────────────────┐
│  helios — Revoke Device                             │
│                                                     │
│  Are you sure you want to revoke                    │
│  "Kamrul's iPhone" (device-001)?                    │
│                                                     │
│  This device will no longer receive                 │
│  notifications or be able to approve                │
│  permissions.                                       │
│                                                     │
│  y yes, revoke   n no, go back                      │
└─────────────────────────────────────────────────────┘
```

---

### Frontend Auto-Setup Flow (Setup.tsx)

```tsx
// Triggered when URL contains key params:
// https://tunnel/#/setup?key=...&kid=...

async function autoSetup(key: string, kid: string) {
  // 1. Store Ed25519 private key in IndexedDB
  await storeKey(key, kid);

  // 2. Sign a JWT using the private key
  const jwt = await signJWT();

  // 3. Login → server verifies JWT, sets HttpOnly cookie
  await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token: jwt }),
  });

  // 4. Auto-detect device metadata from user-agent
  const platform = detectPlatform(navigator.userAgent);
  const browser = detectBrowser(navigator.userAgent);

  // 5. Register device metadata (cookie sent automatically)
  await fetch('/api/auth/device/me', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: `${platform} — ${browser}`, platform, browser }),
  });

  // 6. Prompt user to customize device name (pre-filled)
  // 7. Request push notification permission
  // 8. Subscribe to push + register with daemon
  // 9. Prompt PWA install
  // 10. Redirect to dashboard
}
```

---

### Config Schema

```yaml
# ~/.helios/config.yaml
server:
  internal_port: 7654    # localhost only — hooks + CLI admin
  public_port: 7655      # tunnel-exposed — frontend + API
auth:
  enabled: true
tunnel:
  provider: ""           # cloudflare | ngrok | tailscale | local | custom
  custom_url: ""         # only used when provider=custom
```

---

### Daemon Startup

```
daemon.Start(cfg)
  ├── open DB, run migrations
  ├── init VAPID keys + push sender
  ├── start internal server on 127.0.0.1:{internal_port}
  │     ├── /hooks/*       (Claude hooks, no auth)
  │     └── /internal/*    (CLI admin API, no auth)
  ├── start public server on 0.0.0.0:{public_port}
  │     ├── /api/auth/login, /api/health    (no auth)
  │     ├── /api/*                          (cookie auth)
  │     └── /*                              (frontend SPA, cookie auth)
  ├── if tunnel.provider configured → auto-start tunnel
  ├── write PID file
  └── signal handling (graceful shutdown: both servers + tunnel)
```

---

### Hook Config

Hooks always point to the internal port (localhost only):

```json
{
  "hooks": {
    "PermissionRequest": [{
      "matcher": "*",
      "hooks": [{ "type": "http", "url": "http://localhost:7654/hooks/permission", "timeout": 300 }]
    }],
    "Stop": [{
      "matcher": "*",
      "hooks": [{ "type": "http", "url": "http://localhost:7654/hooks/stop" }]
    }],
    "StopFailure": [{
      "matcher": "*",
      "hooks": [{ "type": "http", "url": "http://localhost:7654/hooks/stop-failure" }]
    }],
    "Notification": [{
      "matcher": "permission_prompt|idle_prompt",
      "hooks": [{ "type": "http", "url": "http://localhost:7654/hooks/notification" }]
    }],
    "SessionStart": [{
      "matcher": "*",
      "hooks": [{ "type": "http", "url": "http://localhost:7654/hooks/session-start" }]
    }],
    "SessionEnd": [{
      "matcher": "*",
      "hooks": [{ "type": "http", "url": "http://localhost:7654/hooks/session-end" }]
    }]
  }
}
```

---

### CLI Commands

```
helios setup                    # Interactive TUI — full first-time setup
helios daemon start [-d]        # Start daemon (reads config, auto-starts tunnel)
helios daemon stop              # Stop daemon + tunnel
helios daemon status            # Show daemon + tunnel status
helios devices                  # Device management TUI (list, detail, revoke, add)
helios hooks install [--local]  # Install Claude hooks
helios hooks show               # Print hook config
helios hooks remove             # Remove hooks
```

`helios auth init`, `helios auth devices`, `helios auth revoke`, `helios tunnel *` are all subsumed by `helios setup` and `helios devices`.

---

### Security Summary

| Concern | Mitigation |
|---------|-----------|
| Hooks exposed to internet | Hooks on internal server (127.0.0.1 only), never tunneled |
| Fake permission requests | Only reachable from localhost |
| Admin API exposed | On internal server (127.0.0.1 only), never tunneled |
| Unauthorized approve/deny | Public API requires cookie-based JWT auth |
| Frontend pages exposed | All pages except setup require auth cookie; unauthenticated visitors see setup page only |
| VAPID key leak | Requires auth cookie, not publicly accessible |
| XSS token theft | Cookie is `HttpOnly` — JavaScript cannot read it |
| CSRF attacks | Cookie is `SameSite=Strict` — only sent for same-origin requests |
| Device key theft | Private key shown once in QR, never stored on server, transmitted only over HTTPS tunnel |
| Tunnel URL guessing | URL is random, changes on restart, requires auth cookie for any action |
| Push subscription hijack | Subscribe endpoint requires auth cookie |
| Service worker auth | Cookie is automatically included in SW fetch requests (same origin) |
| Per-device revocation | Each device has its own keypair; revoking one doesn't affect others |

---

### Implementation Order

1. Update device DB schema (add platform, browser columns; status default "pending")
2. Split server into internal + public (refactor `server.go`)
3. Cookie auth middleware + `POST /api/auth/login`
4. Add `/internal/*` admin API endpoints
5. Add `/api/auth/device/me` (GET + POST)
6. Tunnel interface + cloudflare implementation
7. Update daemon to start both servers + auto-start tunnel
8. Update config schema (internal_port, public_port, tunnel)
9. Frontend: auto-import from URL params + device naming + push prompt flow
10. Bubbletea TUI: `helios setup` (status check, tunnel selection, QR, wait for device)
11. Bubbletea TUI: `helios devices` (list, detail, revoke, add)
12. Update CLI `main.go` to use new commands
13. Add ngrok, tailscale, local tunnel implementations
14. Update hook config to use internal port
