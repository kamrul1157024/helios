# Channel Protocol

## Overview

Channels are independent HTTP servers that register with the helios daemon. The daemon doesn't know anything about channels in advance — no hardcoded routes, no fixed paths, no assumptions. Channels tell the daemon everything during registration.

The daemon acts as a reverse proxy, forwarding external webhook traffic to the correct channel based on registered routes. All channel state is persisted in SQLite so the daemon remembers registered channels across restarts.

## Core Principles

1. **Channels are self-describing** — they tell the daemon their name, port, capabilities, routes, and webhook paths during registration
2. **Daemon is a dumb proxy** — it doesn't understand Telegram, Slack, or any channel-specific protocol. It just forwards HTTP requests based on registered routes
3. **Single exposed port** — only helios port 7654 is exposed. All external webhooks route through `/channel-hooks/...`
4. **Channels are independent processes** — they can be any language, any framework, they just need to implement a few HTTP endpoints and register with the daemon

## Architecture

```
External services (Telegram, Slack, etc.)
    |
    |  webhook callbacks
    v
helios daemon (port 7654) — the only exposed port
    |
    +-- /api/*               → helios API
    +-- /                    → React frontend
    +-- /channel-hooks/*     → reverse proxy (dynamic, based on registered routes)
    |     |
    |     +-- routes from SQLite:
    |           /channel-hooks/tg-bot/webhook      → localhost:9001/webhook
    |           /channel-hooks/tg-bot/callback      → localhost:9001/callback
    |           /channel-hooks/my-slack/interact     → localhost:9002/interact
    |           /channel-hooks/my-slack/events       → localhost:9002/events
    |           /channel-hooks/discord-notif/hook    → localhost:9003/hook
    |
    +-- /api/channels/*      → channel management API
```

## Channel Lifecycle

### Phase 1: Channel Starts

Channel process starts independently (manually, via systemd, via helios, whatever). It's just an HTTP server.

### Phase 2: Channel Registers with Daemon

Channel sends POST to daemon's registration endpoint with everything the daemon needs to know.

```
Channel starts on localhost:9001
    |
    v
POST http://localhost:7654/api/channels/register
{
    "name": "tg-bot",
    "display_name": "Telegram Bot",
    "port": 9001,
    "health_endpoint": "/health",
    "capabilities": ["send", "actions", "commands"],

    "routes": [
        {
            "path": "/webhook",
            "methods": ["POST"],
            "description": "Telegram Bot API webhook callback"
        },
        {
            "path": "/callback",
            "methods": ["POST"],
            "description": "Telegram inline keyboard callbacks"
        }
    ],

    "notify_endpoint": "/notify",
    "init_endpoint": "/init",
    "shutdown_endpoint": "/shutdown"
}
```

### Phase 3: Daemon Processes Registration

```
Daemon receives registration:
    |
    +-- Validate: name unique? port reachable?
    +-- Health check: GET localhost:9001/health
    +-- Store in SQLite: channel info + routes
    +-- Register proxy routes:
    |     /channel-hooks/tg-bot/webhook   → localhost:9001/webhook
    |     /channel-hooks/tg-bot/callback  → localhost:9001/callback
    +-- Generate webhook URLs (if public_url configured):
    |     https://your-domain.com/channel-hooks/tg-bot/webhook
    |     https://your-domain.com/channel-hooks/tg-bot/callback
    +-- Call channel's init endpoint with generated URLs:
          POST localhost:9001/init
          {
              "daemon_url": "http://localhost:7654",
              "public_url": "https://your-domain.com",
              "webhook_urls": {
                  "/webhook": "https://your-domain.com/channel-hooks/tg-bot/webhook",
                  "/callback": "https://your-domain.com/channel-hooks/tg-bot/callback"
              }
          }
```

### Phase 4: Channel Receives Init and Sets Up

```
Telegram channel receives init:
    |
    +-- Stores daemon_url for API calls later
    +-- Registers webhook with Telegram API:
    |     POST api.telegram.org/bot<token>/setWebhook
    |     { url: "https://your-domain.com/channel-hooks/tg-bot/webhook" }
    +-- Starts serving
    +-- Responds: { "ok": true }
```

### Phase 5: Running

```
Daemon pushes notifications:
    POST localhost:9001/notify
    { id: "notif-001", type: "permission", ... }

External webhooks arrive at daemon:
    POST your-domain.com/channel-hooks/tg-bot/webhook
    → daemon proxies to localhost:9001/webhook

Channel processes webhook, calls daemon API:
    POST localhost:7654/api/notifications/notif-001/approve
```

### Phase 6: Shutdown

```
Daemon shutting down:
    POST localhost:9001/shutdown
    → channel cleans up (deregister webhook with Telegram, etc.)

Channel shutting down voluntarily:
    POST localhost:7654/api/channels/deregister
    { "name": "tg-bot" }
    → daemon removes routes, marks inactive in SQLite
```

## Registration Data Model

### What Channel Tells Daemon

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Unique identifier (e.g., "tg-bot", "my-slack", "custom-1") |
| `display_name` | string | yes | Human-readable name |
| `port` | int | yes | Port the channel listens on |
| `health_endpoint` | string | yes | Path for health checks |
| `capabilities` | list | yes | What the channel supports: "send", "actions", "commands" |
| `routes` | list | yes | Webhook routes to proxy (see below) |
| `notify_endpoint` | string | yes | Path where daemon sends notifications |
| `init_endpoint` | string | no | Path for initialization (receives webhook URLs) |
| `shutdown_endpoint` | string | no | Path for graceful shutdown |
| `metadata` | object | no | Arbitrary channel-specific metadata |

### Route Entry

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Channel-local path (e.g., "/webhook", "/interact") |
| `methods` | list | HTTP methods to proxy ("GET", "POST", etc.) |
| `description` | string | Human-readable description of this route |

### What Daemon Tells Channel (on init)

| Field | Type | Description |
|-------|------|-------------|
| `daemon_url` | string | How to reach daemon API (always "http://localhost:7654") |
| `public_url` | string or null | Public URL if configured, null otherwise |
| `webhook_urls` | object | Map of channel path → full public webhook URL |

## SQLite Schema

```sql
CREATE TABLE channels (
    name TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    port INTEGER NOT NULL,
    health_endpoint TEXT NOT NULL,
    capabilities TEXT NOT NULL,          -- JSON array: ["send", "actions"]
    notify_endpoint TEXT NOT NULL,
    init_endpoint TEXT,
    shutdown_endpoint TEXT,
    metadata TEXT,                       -- JSON object
    status TEXT NOT NULL DEFAULT 'active',  -- active, inactive, error
    registered_at TEXT NOT NULL,
    last_health_check TEXT,
    last_health_status TEXT              -- healthy, unhealthy, unknown
);

CREATE TABLE channel_routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_name TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
    path TEXT NOT NULL,
    methods TEXT NOT NULL,               -- JSON array: ["POST"]
    description TEXT,
    webhook_url TEXT,                    -- generated full public URL (null if no public_url)
    UNIQUE(channel_name, path)
);
```

## Daemon Channel Management API

```
POST   /api/channels/register           Channel registers itself
POST   /api/channels/deregister         Channel deregisters itself
GET    /api/channels                     List all registered channels
GET    /api/channels/:name              Get channel details + routes
DELETE /api/channels/:name              Force remove a channel
GET    /api/channels/:name/health       Check channel health
POST   /api/channels/:name/test         Send test notification to channel
```

## Reverse Proxy Behavior

When a request arrives at `/channel-hooks/*`:

```
Request: POST /channel-hooks/tg-bot/webhook
    |
    v
Daemon:
    1. Extract channel name from path: "tg-bot"
    2. Extract remaining path: "/webhook"
    3. Look up in SQLite: channel "tg-bot" route "/webhook"
    4. Found? Check method matches?
         NO  → 404
         YES → proxy to localhost:{port}{path}
    5. Forward request:
         - Copy all headers
         - Copy body
         - Forward to localhost:9001/webhook
    6. Return channel's response to caller
```

The proxy is completely transparent. It doesn't inspect, modify, or understand the request body. It just forwards based on registered routes.

## Health Checks

Daemon periodically health-checks registered channels:

```
Every 30 seconds:
    For each active channel:
        GET localhost:{port}{health_endpoint}
        → 200? mark healthy
        → timeout/error? mark unhealthy
        → 3 consecutive unhealthy? mark status "error", stop sending notifications
```

## Channel Config in helios config.yaml

The daemon config only needs `public_url`. Everything else comes from channel registration.

```yaml
server:
  bind: "localhost"
  port: 7654
  public_url: "https://abc123.ngrok.io"    # optional, set when exposed

# No channel-specific config in daemon!
# Channels are self-contained.
# They register themselves at runtime.
```

Channel-specific config (bot tokens, chat IDs, etc.) lives in the channel's own config, not in helios config. Helios doesn't know or care about Telegram bot tokens.

## Daemon Restart Behavior

When daemon restarts:

```
Daemon starts
    |
    +-- Load channels from SQLite
    +-- For each channel with status "active":
    |     +-- Health check: GET localhost:{port}{health_endpoint}
    |     +-- Healthy? Re-register proxy routes from SQLite. Ready.
    |     +-- Unhealthy? Mark "inactive". Log warning.
    |
    +-- Proxy routes are live
    +-- Waiting for channels to re-register if they restarted too
```

If a channel process also restarted, it re-registers (POST /api/channels/register). Daemon updates SQLite, re-inits the channel with webhook URLs.

## Channel Restart Behavior

When a channel restarts:

```
Channel starts
    |
    +-- POST /api/channels/register (same registration payload)
    +-- Daemon sees: name "tg-bot" already exists in SQLite
    |     +-- Update port/routes/capabilities if changed
    |     +-- Re-register proxy routes
    |     +-- Call /init with webhook URLs
    +-- Channel sets up webhooks with external service
    +-- Ready
```

Registration is idempotent. Re-registering just updates the existing entry.

## No Public URL Scenario

If `public_url` is not configured:

```
Channel registers with routes
    |
    v
Daemon:
    +-- Stores routes in SQLite
    +-- Registers proxy routes (they work on localhost too)
    +-- Calls /init with webhook_urls: all null
    |
    v
Channel receives init with null webhook_urls:
    +-- Cannot register webhooks with external services
    +-- Falls back to polling mode (getUpdates, etc.)
    +-- Still receives notifications from daemon (POST /notify)
    +-- Still calls daemon API for actions/commands
```

Everything works except external webhooks. Channel just polls instead.

## Example: Minimal Custom Channel (send-only)

A channel that just forwards notifications to a Discord webhook. No actions, no commands, no webhook routes.

```
Registration:
POST /api/channels/register
{
    "name": "my-discord",
    "display_name": "My Discord Alerts",
    "port": 9005,
    "health_endpoint": "/health",
    "capabilities": ["send"],
    "routes": [],                          ← no webhook routes needed
    "notify_endpoint": "/notify"
}

The channel is a server with 2 endpoints:
    GET  /health  → { "ok": true }
    POST /notify  → receives notification, POSTs to Discord webhook, returns ack
```

No init, no shutdown, no routes, no proxy. Just receives notifications and forwards them. 20 lines of code in any language.

## Example: Full Interactive Channel (Telegram)

```
Registration:
POST /api/channels/register
{
    "name": "tg-bot",
    "display_name": "Telegram Bot",
    "port": 9001,
    "health_endpoint": "/health",
    "capabilities": ["send", "actions", "commands"],
    "routes": [
        { "path": "/webhook", "methods": ["POST"], "description": "Telegram updates" }
    ],
    "notify_endpoint": "/notify",
    "init_endpoint": "/init",
    "shutdown_endpoint": "/shutdown"
}

The channel is a server with:
    GET  /health    → health check
    POST /init      → receives webhook URLs, registers with Telegram API
    POST /notify    → receives notification, sends Telegram message with buttons
    POST /webhook   → receives Telegram updates (proxied by daemon)
                      parses user actions/commands
                      calls daemon API (approve, send message, list, etc.)
    POST /shutdown  → deregisters webhook with Telegram API
```

## Multiple Instances of Same Channel Type

You can run two Telegram bots (different tokens, different chats):

```
tg-personal (port 9001) — your personal chat
tg-team (port 9002) — team channel

Both register with different names. Daemon proxies independently.
```

Names are unique, not channel types. You can have as many instances of any channel type as you want.
