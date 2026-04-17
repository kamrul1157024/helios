# Spec 28: Managed Session Recovery

## Problem

When the tmux server is killed and helios restarts, the in-memory PaneMap is lost.
Sessions that were `active` or `idle` remain in those statuses in the DB but have no
pane attached. This causes:

- Mobile shows an unexplained `⚠` warning with no actionable recovery path
- Stop/Terminate buttons silently fail (`no_tmux_pane` error)
- Sessions stuck indefinitely — never reaped, never recoverable from UI

Additionally, Claude sessions started outside helios (plain terminal, no tmux) are
discovered via transcript scan and appear in the UI with the same broken state, but
these cannot be auto-recovered since helios never owned them.

## Solution

Introduce a `managed` boolean on sessions. Helios auto-recovers managed sessions
when their pane is lost. Unmanaged sessions show a recovery UI where the user can
choose what to do — including handing off to helios permanently.

---

## `managed` Flag

### DB migration

```sql
ALTER TABLE sessions ADD COLUMN managed BOOLEAN NOT NULL DEFAULT 0;
```

Default `0` (false) — conservative. Existing sessions before migration get no
surprise auto-recovery.

### What sets managed=true

| Origin | Code path |
|---|---|
| `helios new` / `helios wrap` | `handleInternalCreateSession`, `handleWrap` |
| Mobile "new session" | `handleCreateSession` |
| SessionStart hook — pane found in PaneMap or PendingPanes | `hooks.go` |

### What sets managed=false

| Origin | Code path |
|---|---|
| Transcript discovery | `InsertDiscoveredSession` |
| SessionStart hook — no pane found | `hooks.go` |

### User control

The user can toggle `managed` at any time:
- Session detail settings
- Long-press context menu on session card
- Recovery bottom sheet ("Hand off to Helios" / "Detach from Helios")

`PATCH /api/sessions/<id>` gains a `managed` field alongside `pinned`, `archived`, `title`.

---

## State Machine

### Status transitions (unchanged)

```
starting → idle → active ↔ compacting
                         ↔ waiting_permission
                → terminated
```

### Pane loss behaviour matrix

| status | managed | hasTmux | behaviour |
|---|---|---|---|
| active / idle | true | ✓ | Normal operation |
| active / idle | true | ✗ | **Auto-recover** — spawn new pane silently |
| active / idle | false | ✓ | Normal operation |
| active / idle | false | ✗ | **Show ⚡ recovery UI** — user chooses |
| terminated | any | any | No recovery |

### Full state machine diagram

```
  ORIGIN
  ──────
  helios new / wrap / mobile new session       ──► managed=true
  SessionStart hook (pane in PaneMap/Pending)  ──► managed=true
  SessionStart hook (no pane found)            ──► managed=false
  Discovery (transcript scan)                  ──► managed=false


  STATUS TRANSITIONS
  ──────────────────

        [helios launch]              [SessionStart hook]
              │                              │
              ▼                              ▼
        ┌──────────┐   hook:SessionStart  ┌──────────┐
        │ starting │ ──────────────────► │   idle   │
        └──────────┘                     └──────────┘
                                              │   ▲
                              user prompt     │   │  claude done / user stop
                                              ▼   │
                                         ┌──────────┐
                                         │  active  │◄──┐
                                         └──────────┘   │
                                              │          │
                                    ┌─────────┤          │
                                    │         │          │
                                    ▼         ▼          │
                             ┌──────────┐ ┌──────────┐  │
                             │compacting│ │ waiting_ │  │
                             └──────────┘ │permission│  │
                                    │     └──────────┘  │
                                    └──────────┬─────────┘
                                               │
                                    [process exit / terminate]
                                               │
                                               ▼
                                         ┌──────────┐
                                         │terminated│
                                         └──────────┘


  PANE LOSS — managed=true (auto-recover)
  ────────────────────────────────────────

  daemon startup                reaper tick (every 10s)
       │                               │
       ▼                               ▼
  RebuildPaneMap              SweepDeadPanes
       │                               │
       └──────────────┬────────────────┘
                      │
                      ▼
        for each session where:
          status ∈ {starting, active,
                    waiting_permission,
                    compacting, idle}
          AND managed = true
          AND paneID not in PaneMap
                      │
                      ▼
          CreateWindow(cwd, "claude --resume <id>")
          PaneMap.Set(id, newPane)
          SetPaneSessionID(newPane, id)
          SSE broadcast: tmux_pane updated
          (no status change — hooks will update)


  PANE LOSS — managed=false (recovery UI)
  ─────────────────────────────────────────

                   ┌──────────────────────┐
                   │   ⚡ bottom sheet     │
                   └──────────────────────┘
                              │
         ┌────────────────────┼────────────────────┐
         │                    │                     │
         ▼                    ▼                     ▼
  ┌─────────────┐   ┌─────────────────┐   ┌─────────────────┐
  │ 🛡 Hand off │   │ 🔗 Attach to    │   │ ▶ Continue in   │
  │   to Helios │   │   existing pane │   │   new tmux pane │
  └─────────────┘   └─────────────────┘   └─────────────────┘
         │                    │                     │
         ▼                    ▼                     ▼
  managed = true      user picks from       claude --resume <id>
  reaper recovers     unbound pane list     new pane spawned
  ⚡ disappears       PaneMap rebind        managed unchanged
                      managed unchanged


  MANAGED FLAG TRANSITIONS
  ────────────────────────

  managed=false  ──► "Hand off to Helios" / settings toggle  ──► managed=true
  managed=true   ──► "Detach from Helios" / settings toggle  ──► managed=false
```

---

## Backend Changes

### 1. DB schema

```sql
ALTER TABLE sessions ADD COLUMN managed BOOLEAN NOT NULL DEFAULT 0;
```

### 2. `store.Session` struct

Add `Managed bool` field. Include in all SELECT/INSERT/UPDATE queries.

### 3. Session creation paths — set managed=true

- `handleInternalCreateSession` (`api.go:827`)
- `handleCreateSession` (`api.go:1377`)
- `handleWrap` (`api.go:895`) — always managed, helios wrap implies ownership

### 4. Discovery — set managed=false

`InsertDiscoveredSession` already uses a separate path; set `managed=0` explicitly.

### 5. SessionStart hook (`hooks.go`)

After resolving pane from PaneMap/PendingPanes:
- pane found → `managed=true` (update DB)
- pane not found → leave `managed=false`

### 6. Auto-recovery function

New function `recoverManagedSessions(db, tmux, paneMap, sse)`:

```go
func recoverManagedSessions(db *store.Store, tc *tmux.Client, pm *tmux.PaneMap, sse *server.SSEBroadcaster) {
    if !tc.Available() {
        return
    }
    sessions, _ := db.ListManagedOrphanedSessions()
    for _, sess := range sessions {
        if _, hasPane := pm.Get(sess.SessionID); hasPane {
            continue
        }
        cmd := fmt.Sprintf("claude --resume %s", sess.SessionID)
        paneID, err := tc.CreateWindow(sess.CWD, cmd)
        if err != nil {
            log.Printf("recover: failed to recover session %s: %v", sess.SessionID, err)
            continue
        }
        pm.Set(sess.SessionID, paneID)
        tc.SetPaneSessionID(paneID, sess.SessionID)
        sse.Broadcast(server.SSEEvent{
            Type: "session_status",
            Data: map[string]interface{}{
                "session_id": sess.SessionID,
                "tmux_pane":  paneID,
            },
        })
        log.Printf("recover: recovered managed session %s → pane %s", sess.SessionID, paneID)
    }
}
```

Called from:
- `daemon.go` after `RebuildPaneMap` on startup
- `reapStaleSessions` in `reaper.go` on every tick

### 7. New DB query

```go
// ListManagedOrphanedSessions returns managed sessions that are not terminated.
func (s *Store) ListManagedOrphanedSessions() ([]Session, error) {
    // returns sessions where managed=1 AND status NOT IN ('terminated')
}
```

### 8. `PATCH /api/sessions/<id>` — add managed field

```go
var req struct {
    Pinned   *bool   `json:"pinned"`
    Archived *bool   `json:"archived"`
    Title    *string `json:"title"`
    Status   *string `json:"status"`
    Managed  *bool   `json:"managed"`   // new
}
```

### 9. `terminateSession` / `stopSession` — fix no-pane case

`terminateSession`: if no pane in PaneMap, skip `KillPane` and go straight to
`UpdateSessionStatus(id, "terminated", "Terminate")`. Don't return `no_tmux_pane`.

`stopSession`: if no pane, return success (session is already not running).

### 10. New endpoint: GET /api/sessions/unbound-panes

Returns Claude panes that are running but not bound to any session in PaneMap.
Used by the mobile "Attach to existing pane" sub-sheet.

```json
{
  "panes": [
    {
      "pane_id": "%3",
      "cwd": "/Users/user/workspace/myapp",
      "project": "myapp",
      "last_user_message": "add auth middleware"
    }
  ]
}
```

Logic:
1. `ListClaudePanes()` — all panes running Claude
2. Filter out panes whose pane_id is already a value in PaneMap
3. For each remaining pane, look up DB session by CWD for `last_user_message`

### 11. New endpoint: POST /api/sessions/<id>/attach

Binds an existing pane to a session.

```json
{ "pane_id": "%3" }
```

Logic:
- Validate pane exists (`HasPane`)
- `PaneMap.Set(id, paneID)`
- `SetPaneSessionID(paneID, id)`
- SSE broadcast `tmux_pane` updated

---

## Mobile Changes

### 1. `Session` model

Add `managed` field:

```dart
final bool managed;
// fromJson: managed: json['managed'] == true || json['managed'] == 1,
// getter:
bool get isHeliosManaged => managed;
```

### 2. `canTerminate` / `canStop` — remove hasTmux requirement

```dart
bool get canStop => isActive;         // was: hasTmux && isActive
bool get canTerminate => isActive || isIdle;  // was: hasTmux && (isActive || isIdle)
```

### 3. Session card — replace ⚠ with link_off icon

```
Before: Icon(Icons.warning_amber, ...)  tooltip: 'No tmux pane attached'
After:  Icon(Icons.link_off, ...)       tooltip: 'No tmux pane — tap to recover'
```

Show `link_off` when: `!session.hasTmux && !session.isTerminated && !session.isHeliosManaged`

### 4. Session detail app bar — replace ⚠ with link_off, open recovery sheet

```dart
if (!session.hasTmux && !session.isTerminated && !session.managed)
  IconButton(
    icon: Icon(Icons.link_off, color: Colors.amber.shade700),
    tooltip: 'No tmux pane',
    onPressed: _showRecoverySheet,
  ),
```

### 5. Recovery bottom sheet (`_showRecoverySheet`)

```
╔═════════════════════════════════════════════════════╗
║  🔗  No tmux pane attached                          ║
║  This session has no tmux pane. Recovery options:   ║
║                                                     ║
║  ┌─────────────────────────────────────────────┐   ║
║  │  🛡  Hand off to Helios                     │   ║
║  │  Helios will manage and auto-recover this    │   ║
║  │  session automatically from now on           │   ║
║  └─────────────────────────────────────────────┘   ║
║                                                     ║
║  ┌─────────────────────────────────────────────┐   ║
║  │  🔗  Attach to existing pane                │   ║
║  │  Bind a running Claude pane to this session  │   ║
║  └─────────────────────────────────────────────┘   ║
║                                                     ║
║  ┌─────────────────────────────────────────────┐   ║
║  │  ▶  Continue in new tmux pane               │   ║
║  │  Resume conversation in a fresh pane         │   ║
║  └─────────────────────────────────────────────┘   ║
║                                                     ║
║                               [ Cancel ]            ║
╚═════════════════════════════════════════════════════╝
```

Actions:
- **Hand off to Helios**: `PATCH /api/sessions/<id>` `{managed: true}` → close sheet
- **Attach to existing pane**: fetch `GET /api/sessions/unbound-panes` → show pane picker sub-sheet → `POST /api/sessions/<id>/attach`
- **Continue in new tmux pane**: `POST /api/sessions/<id>/resume` → close sheet

### 6. Unbound pane picker sub-sheet

```
╔═════════════════════════════════════════════════════╗
║  🔗  Select a running Claude pane                   ║
║                                                     ║
║  ┌─────────────────────────────────────────────┐   ║
║  │  ● myapp        ~/workspace/myapp            │   ║
║  │    "add auth middleware"                     │   ║
║  └─────────────────────────────────────────────┘   ║
║  ┌─────────────────────────────────────────────┐   ║
║  │  ● helios       ~/workspace/helios           │   ║
║  │    "fix the reaper logic"                    │   ║
║  └─────────────────────────────────────────────┘   ║
║                                                     ║
║  [empty state: "No unbound Claude panes found"]     ║
║                                                     ║
║                               [ Cancel ]            ║
╚═════════════════════════════════════════════════════╝
```

### 7. Session settings / context menu — managed toggle

Long-press context menu gains:

```
  ─────────────────
  🛡  Hand off to Helios     (when managed=false)
  — or —
  ⎋  Detach from Helios      (when managed=true)
```

---

## Unit Tests

### Go

**`internal/store/sessions_test.go`**
- `TestManagedFlag_DefaultFalse` — newly inserted session via `InsertDiscoveredSession` has `managed=false`
- `TestManagedFlag_SetOnUpsert` — session upserted with `managed=true` persists correctly
- `TestListManagedOrphanedSessions_ExcludesTerminated` — terminated sessions not returned
- `TestListManagedOrphanedSessions_ExcludesHasPaneInMap` — sessions with known pane not returned
- `TestListManagedOrphanedSessions_IncludesAllActiveStatuses` — starting, active, waiting_permission, compacting, idle all included

**`internal/daemon/reaper_test.go`** (new file)
- `TestRecoverManagedSessions_NoOp_WhenTmuxUnavailable` — `recoverManagedSessions` does nothing when `!tc.Available()`
- `TestRecoverManagedSessions_SpawnsPane_WhenManagedOrphaned` — spawns new pane and updates PaneMap for managed orphaned session
- `TestRecoverManagedSessions_SkipsAlreadyBound` — sessions already in PaneMap are not re-spawned
- `TestRecoverManagedSessions_BroadcastsSSE` — SSE event fired after recovery

**`internal/server/api_test.go`**
- `TestTerminateSession_NoPane_MarksTerminated` — terminate succeeds without a pane in PaneMap
- `TestStopSession_NoPane_ReturnsSuccess` — stop succeeds without a pane
- `TestPatchSession_ManagedField` — PATCH with `{managed: true}` persists correctly
- `TestUnboundPanes_FiltersAlreadyMapped` — unbound-panes endpoint excludes panes in PaneMap
- `TestAttachSession_BindsPane` — attach endpoint sets PaneMap and broadcasts SSE

### Dart / Flutter

**`test/models/session_test.dart`**
- `managed field parsed from JSON`
- `isHeliosManaged returns true when managed=true`
- `canStop is true when active regardless of hasTmux`
- `canTerminate is true when idle regardless of hasTmux`
- `link_off icon shown when not managed and no pane and not terminated`
- `link_off icon hidden when managed=true even with no pane`
- `link_off icon hidden when terminated`

---

## Implementation Order

1. DB migration + `store.Session` struct + queries
2. Set `managed` on all session creation paths (Go)
3. `recoverManagedSessions` function + wire into startup and reaper
4. Fix `terminateSession` / `stopSession` no-pane case
5. `PATCH /api/sessions/<id>` — add managed field
6. `GET /api/sessions/unbound-panes` endpoint
7. `POST /api/sessions/<id>/attach` endpoint
8. Mobile: `Session` model + `canTerminate`/`canStop` fix
9. Mobile: ⚡ icon in card and app bar
10. Mobile: recovery bottom sheet + pane picker sub-sheet
11. Mobile: managed toggle in context menu and session settings
