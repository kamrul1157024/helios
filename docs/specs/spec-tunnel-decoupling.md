# Spec: Tunnel Decoupling & Daemon Crash Recovery

## Problem

The tunnel process (cloudflared, ngrok, etc.) is a child of the helios daemon. When the daemon crashes or restarts:

1. The tunnel process dies (child process inherits parent lifecycle)
2. A new tunnel gets a **different URL** on restart
3. All connected mobile devices lose connectivity and must rescan/reconnect

## Goals

1. Tunnel process survives daemon crash/restart — same URL persists
2. Daemon auto-recovers from panics via a lightweight supervisor
3. `helios stop` / `helios daemon stop` leaves tunnel alive by default
4. Explicit `helios tunnel stop` to kill tunnel, with user confirmation

---

## Design

### 1. Decoupled Tunnel Process

#### Current behavior

```
daemon (PID 100)
  └── cloudflared (PID 101, child — dies when 100 dies)
```

Providers use `exec.CommandContext(ctx, ...)` which kills the process when the context is cancelled (daemon shutdown).

#### New behavior

```
daemon (PID 100)          cloudflared (PID 101, independent process group)
  │                         │
  └── manages via PID ──────┘
```

Providers use `exec.Command(...)` with `SysProcAttr{Setsid: true}` to create an independent process group. The daemon manages the tunnel via its PID, not via parent-child relationship.

#### Tunnel State File

`~/.helios/tunnel.state` — JSON file persisted on disk:

```json
{
  "pid": 12345,
  "provider": "cloudflare",
  "url": "https://abc-xyz.trycloudflare.com",
  "port": 7655,
  "started_at": "2026-04-11T10:30:00Z"
}
```

Written when tunnel starts. Read on daemon startup to adopt an existing tunnel. Removed when tunnel is explicitly stopped.

#### Tunnel Adoption on Daemon Start

On `daemon.Start()`, before spawning a new tunnel:

1. Read `~/.helios/tunnel.state`
2. Check if PID is alive (`signal(0)`)
3. If alive and provider matches config → adopt it (set as active tunnel, skip spawn)
4. If dead or mismatched → remove stale state file, start fresh

#### File Changes

**`internal/tunnel/state.go`** (new)
- `TunnelState` struct
- `SaveState(heliosDir string, state TunnelState) error`
- `LoadState(heliosDir string) (*TunnelState, error)`
- `RemoveState(heliosDir string) error`

**`internal/tunnel/tunnel.go`**
- `Manager` gets a `heliosDir string` field (for state file path)
- `NewManager(heliosDir string) *Manager`
- New method: `Adopt() (string, error)` — loads state, validates PID, sets active tunnel
- `Start()` writes state file after successful start
- `Stop()` kills process + removes state file
- New method: `StopTunnel() error` — explicit tunnel kill (used by `helios tunnel stop`)

**`internal/tunnel/cloudflare.go`**
- `CloudflareTunnel.Start()` — `exec.Command` + `Setsid: true` instead of `exec.CommandContext`
- `NgrokTunnel.Start()` — same change
- `TailscaleTunnel.Start()` — same change
- Each provider stores PID in struct for state persistence
- Context cancellation no longer kills the process; only explicit `Stop()` does

---

### 2. Daemon Shutdown No Longer Kills Tunnel

#### Current behavior

```go
// daemon.go shutdown
tunnelMgr.Stop()           // kills tunnel
internalSrv.Shutdown(ctx)
publicSrv.Shutdown(ctx)
```

```go
// main.go handleStop()
http.Post("/internal/tunnel/stop")  // also kills tunnel
daemon.Stop()
```

#### New behavior

```go
// daemon.go shutdown
// tunnel is NOT stopped — it keeps running independently
internalSrv.Shutdown(ctx)
publicSrv.Shutdown(ctx)
```

```go
// main.go handleStop()
// NO tunnel stop call
daemon.Stop()
```

The tunnel survives both graceful shutdown and crashes.

---

### 3. New CLI Command: `helios tunnel stop`

```
$ helios tunnel stop

Tunnel is running: https://abc-xyz.trycloudflare.com (cloudflare, PID 12345)

WARNING: Killing the tunnel will disconnect all mobile devices.
         They will need to rescan and reconnect.

Kill tunnel? [y/N]: y
Tunnel stopped.
```

- Reads `~/.helios/tunnel.state` directly (no daemon needed)
- Prompts for confirmation, default is **N** (keep alive)
- Only kills on explicit `y` or `Y`
- Sends SIGTERM to tunnel PID, waits, then SIGKILL if needed
- Removes state file

Also add `helios tunnel status`:

```
$ helios tunnel status
Tunnel active: https://abc-xyz.trycloudflare.com (cloudflare, PID 12345, up 2h30m)

$ helios tunnel status
No tunnel running.
```

These commands work **without the daemon running** — they read the state file directly.

---

### 4. Daemon Panic Recovery

Wrap the core daemon logic with `defer recover()`:

```go
func Start(cfg *Config) (err error) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("PANIC: %v\n%s", r, debug.Stack())
            err = fmt.Errorf("daemon panicked: %v", r)
        }
    }()
    // ... existing daemon logic
}
```

This ensures:
- Panic is caught and logged with stack trace to `daemon.log`
- `Start()` returns an error instead of crashing the whole process
- The supervisor can detect the error and restart

---

### 5. Supervisor (Process Manager)

A lightweight supervisor that keeps the daemon alive.

#### Architecture

```
helios daemon start -d
  └── supervisor (PID 200, background, own process group)
        └── daemon (runs in-process, restarts on failure)
```

The supervisor is **not** a separate binary. It's a function in the daemon package that:
1. Runs the daemon in a loop
2. On daemon failure (panic/error), logs it and restarts with backoff
3. Writes `~/.helios/supervisor.pid`
4. Handles SIGTERM/SIGINT — stops the daemon gracefully, then exits

#### File Changes

**`internal/daemon/supervisor.go`** (new)

```go
type Supervisor struct {
    cfg         *Config
    maxRestarts int           // max restarts within window (default: 5)
    window      time.Duration // restart count window (default: 5 minutes)
    backoff     time.Duration // initial backoff (default: 1 second, doubles each restart)
    maxBackoff  time.Duration // cap backoff (default: 30 seconds)
}

func (s *Supervisor) Run() error
```

**Run loop:**

```
1. Write supervisor.pid
2. Trap SIGTERM/SIGINT
3. Loop:
   a. Call daemon.Start(cfg)
   b. If error/panic → log, increment restart count
   c. If restart count > maxRestarts within window → exit with error
   d. Sleep backoff duration (doubles each restart, capped at maxBackoff)
   e. Reset restart count if window elapsed since last restart
   f. Restart daemon
4. On signal → daemon.Stop() + remove supervisor.pid + exit
```

**`cmd/helios/main.go`**
- `helios daemon start -d` now spawns a detached process that runs `Supervisor.Run()` instead of `daemon.Start()` directly
- `helios daemon stop` sends SIGTERM to supervisor PID (supervisor then stops daemon)
- `helios daemon status` checks supervisor PID

**PID file changes:**
- `~/.helios/supervisor.pid` — supervisor process (used by `daemon stop`)
- `~/.helios/daemon.pid` — still written by daemon for health checks
- `~/.helios/tunnel.state` — tunnel process state

#### Health Check

The supervisor does a basic health check between restarts:
- After daemon starts, poll `GET http://127.0.0.1:{port}/internal/health` every 30 seconds
- If 3 consecutive health checks fail → restart daemon
- This catches hangs/deadlocks, not just panics

---

### 6. `helios cleanup` Update

`helios cleanup all` should also:
- Kill tunnel process if running (read tunnel.state, kill PID)
- Kill supervisor if running (read supervisor.pid, kill PID)
- Remove all state files

---

## File Summary

| File | Action | Description |
|------|--------|-------------|
| `internal/tunnel/state.go` | **New** | TunnelState struct, Save/Load/Remove |
| `internal/tunnel/tunnel.go` | Modify | Add heliosDir, Adopt(), state persistence |
| `internal/tunnel/cloudflare.go` | Modify | Setsid: true, remove CommandContext |
| `internal/daemon/supervisor.go` | **New** | Supervisor with restart loop + health check |
| `internal/daemon/daemon.go` | Modify | Panic recovery, remove tunnel stop on shutdown |
| `cmd/helios/main.go` | Modify | Add `tunnel` subcommand, supervisor for `-d`, update stop flow |

## State Diagrams

### Process Lifecycle

```
                        ┌─────────────────────────────────────────────┐
                        │              helios daemon start            │
                        └──────────────────┬──────────────────────────┘
                                           │
                                           ▼
                                  ┌─────────────────┐
                                  │   Supervisor     │
                                  │  (writes .pid)   │
                                  └────────┬─────────┘
                                           │
                              ┌────────────▼────────────┐
                              │    Start Daemon          │
                              │  (in-process goroutine)  │
                              └────────────┬─────────────┘
                                           │
                         ┌─────────────────▼──────────────────┐
                         │        tunnel.state exists?         │
                         └──┬──────────────────────────────┬───┘
                          yes                               no
                            │                               │
                    ┌───────▼────────┐              ┌───────▼────────┐
                    │  PID alive?     │              │ Provider       │
                    └──┬──────────┬──┘              │ configured?    │
                     yes          no                └──┬──────────┬──┘
                       │          │                  yes          no
               ┌───────▼──┐  ┌───▼──────────┐       │            │
               │  Adopt    │  │ Remove stale │  ┌────▼─────┐     │
               │  tunnel   │  │ state file   │  │ Spawn    │     │
               └───────────┘  └───┬──────────┘  │ tunnel   │     │
                                  │             │ (Setsid) │     │
                                  │             └────┬─────┘     │
                                  │                  │           │
                                  └───────┬──────────┘           │
                                          │                      │
                                          ▼                      ▼
                                  ┌──────────────┐      ┌──────────────┐
                                  │ Write state  │      │  No tunnel   │
                                  │   file       │      │              │
                                  └──────┬───────┘      └──────────────┘
                                         │
                                         ▼
                                ┌──────────────────┐
                                │  Daemon Running   │
                                │  (serving HTTP)   │
                                └──────────────────┘
```

### Daemon Shutdown / Crash

```
                                ┌──────────────────┐
                                │  Daemon Running   │
                                └────────┬─────────┘
                                         │
                          ┌──────────────┼──────────────┐
                          │              │              │
                    ┌─────▼─────┐  ┌─────▼─────┐  ┌────▼──────┐
                    │  SIGTERM   │  │   Panic    │  │  Error    │
                    │  /SIGINT   │  │  (crash)   │  │  (port    │
                    │            │  │            │  │  conflict)│
                    └─────┬─────┘  └─────┬──────┘  └────┬──────┘
                          │              │               │
                          ▼              ▼               ▼
                  ┌──────────────┐  ┌────────────┐  ┌────────────┐
                  │ Graceful     │  │ recover()  │  │ Return     │
                  │ shutdown     │  │ catches,   │  │ error to   │
                  │ HTTP servers │  │ returns    │  │ supervisor │
                  └──────┬───────┘  │ error      │  └────┬───────┘
                         │         └────┬────────┘       │
                         │              │                │
                         │              └───────┬────────┘
                         │                      │
                         ▼                      ▼
                ┌──────────────────┐   ┌──────────────────┐
                │ Supervisor sees  │   │ Supervisor sees   │
                │ clean exit →     │   │ error → restart   │
                │ exit             │   │ with backoff      │
                └──────────────────┘   └──────────────────┘
                         │                      │
                         │                      │
                         ▼                      ▼
                ┌──────────────────────────────────────────┐
                │     Tunnel process keeps running         │
                │  (independent process group, Setsid)     │
                │  tunnel.state file preserved on disk     │
                └──────────────────────────────────────────┘
```

### Supervisor Restart Logic

```
                        ┌───────────────────┐
                        │ Daemon exited     │
                        │ with error        │
                        └────────┬──────────┘
                                 │
                        ┌────────▼──────────┐
                        │ Count restarts    │
                        │ in last 5 min     │
                        └────────┬──────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  restarts >= 5?          │
                    └──┬──────────────────┬───┘
                     yes                   no
                       │                   │
              ┌────────▼────────┐  ┌───────▼────────────┐
              │ Give up, exit   │  │ Sleep backoff       │
              │ with error      │  │ (1s → 2s → 4s →    │
              └─────────────────┘  │  8s → 16s → 30s)   │
                                   └───────┬─────────────┘
                                           │
                                   ┌───────▼─────────────┐
                                   │ Restart daemon      │
                                   │ (loop back to       │
                                   │  Start)             │
                                   └─────────────────────┘
```

### Tunnel Stop Flow

```
                        ┌───────────────────┐
                        │ helios tunnel stop │
                        └────────┬──────────┘
                                 │
                        ┌────────▼──────────┐
                        │ Read tunnel.state  │
                        └────────┬──────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  State exists?           │
                    └──┬──────────────────┬───┘
                      no                  yes
                       │                   │
              ┌────────▼────────┐  ┌───────▼────────────┐
              │ "No tunnel      │  │ PID alive?          │
              │  running."      │  └──┬──────────────┬───┘
              └─────────────────┘   no               yes
                                     │                │
                            ┌────────▼──────┐  ┌──────▼──────────┐
                            │ Clean stale   │  │ Show warning:   │
                            │ state file    │  │ "devices will   │
                            └───────────────┘  │  disconnect"    │
                                               └──────┬──────────┘
                                                      │
                                              ┌───────▼──────────┐
                                              │ Kill tunnel?     │
                                              │ [y/N]            │
                                              └──┬───────────┬───┘
                                                 │           │
                                              y / Y      anything else
                                                 │           │
                                        ┌────────▼────┐  ┌───▼───────────┐
                                        │ SIGTERM →   │  │ "Tunnel kept  │
                                        │ wait 3s →   │  │  alive."      │
                                        │ SIGKILL     │  └───────────────┘
                                        │ Remove      │
                                        │ state file  │
                                        └─────┬──────┘
                                              │
                                      ┌───────▼──────────┐
                                      │ "Tunnel stopped." │
                                      └──────────────────┘
```

### Process Ownership (Before vs After)

```
BEFORE:
                    ┌────────────────┐
                    │  OS / Shell    │
                    └───────┬────────┘
                            │ spawns
                    ┌───────▼────────┐
                    │    Daemon      │──── dies ──→ children die too
                    └───────┬────────┘
                            │ child process
                    ┌───────▼────────┐
                    │  cloudflared   │──── dead
                    └────────────────┘


AFTER:
                    ┌────────────────┐
                    │  OS / Shell    │
                    └───┬────────┬───┘
                        │        │
            spawns      │        │  spawns (Setsid, independent group)
                        │        │
                ┌───────▼──┐  ┌──▼───────────┐
                │Supervisor│  │ cloudflared   │◄── survives daemon death
                └────┬─────┘  └──────────────┘    managed via PID in
                     │                             tunnel.state
              ┌──────▼──────┐
              │   Daemon    │──── dies ──→ supervisor restarts it
              │ (in-process)│              tunnel unaffected
              └─────────────┘

  PID files:
    ~/.helios/supervisor.pid  ─→ supervisor process
    ~/.helios/daemon.pid      ─→ daemon (same PID as supervisor)
    ~/.helios/tunnel.state    ─→ tunnel process (independent)
```

## Non-Goals

- No systemd/launchd integration (keep it self-contained)
- No named Cloudflare tunnels (requires account setup)
- No multi-tunnel support (still one tunnel at a time)
- Supervisor does not manage tunnel — tunnel is fully independent
