# 23 — Generic Rich HITL (Human-in-the-Loop)

## Overview

Upgrade Helios from a Claude-specific binary approve/deny system to a **generic HITL platform** that can orchestrate any AI agent source. The storage layer, notification model, and API are source-agnostic. Source-specific hook handlers act as translators between the agent's wire format and the generic model. Mobile cards render by `type` (e.g. `claude.permission`, `claude.question`) — each card type owns its internal action implementation and may call different APIs per source.

### Design principles

1. **Generic storage** — `source`, `source_session`, source-prefixed `type`, opaque `payload`/`response` JSON blobs
2. **Single generic action API** — `POST /api/notifications/:id/action` with a freeform body. The backend looks up the notification's `type`, dispatches to a type-specific action handler. The mobile just POSTs to one endpoint
3. **Cards keyed by type** — mobile renders `ClaudePermissionCard`, `ClaudeQuestionCard`, `ClaudeElicitationCard`, etc. Each card knows its layout and constructs the action body for its type
4. **Type-based action handlers** — each `type` registers its own handler. `claude.permission` handles approve/deny/updatedInput. `claude.question` handles answer submission. A future `hermes.approval` registers its own handler
5. **Hook handlers as translators** — receive source-specific input → create generic notification → translate generic response back to source-specific hook output

### Supported interactions (Claude source)

| Interaction | Hook | Type | Blocking? | Currently supported? |
|---|---|---|---|---|
| Permission approval (binary) | `PermissionRequest` | `claude.permission` | Yes (5 min) | Yes (as `permission`) |
| Permission with input editing | `PermissionRequest` | `claude.permission` | Yes | **No** — `updatedInput` not sent |
| Permission with "always allow" | `PermissionRequest` | `claude.permission` | Yes | **No** — `permission_suggestions` ignored |
| Claude asks questions | `PreToolUse` (`AskUserQuestion`) | `claude.question` | Yes | **No** — hook not installed |
| MCP elicitation (form) | `Elicitation` | `claude.elicitation.form` | Yes | **No** — hook not installed |
| MCP elicitation (url) | `Elicitation` | `claude.elicitation.url` | Yes | **No** — hook not installed |
| Session completed | `Stop` | `claude.done` | No | Yes (as `done`) |
| Session error | `StopFailure` | `claude.error` | No | Yes (as `error`) |

---

## Generic Storage Layer

### Notification model

Replace the current Claude-specific `Notification` struct with a generic one.

**File: `internal/store/notifications.go`**

```go
type Notification struct {
    ID             string  `json:"id"`
    Source         string  `json:"source"`          // "claude", "hermes", "codex", ...
    SourceSession  string  `json:"source_session"`  // agent's session ID
    CWD            string  `json:"cwd"`
    Type           string  `json:"type"`            // source-prefixed: "claude.permission", "claude.question", ...
    Status         string  `json:"status"`          // "pending", "approved", "denied", "answered", "dismissed", "timeout"
    Title          *string `json:"title,omitempty"`  // human-readable title
    Detail         *string `json:"detail,omitempty"` // human-readable summary
    Payload        *string `json:"payload,omitempty"`   // opaque JSON blob — all source-specific input data
    Response       *string `json:"response,omitempty"`  // opaque JSON blob — user's response data
    ResolvedAt     *string `json:"resolved_at,omitempty"`
    ResolvedSource *string `json:"resolved_source,omitempty"` // "device:kid", "browser", "system", "claude"
    CreatedAt      string  `json:"created_at"`
}
```

**What goes in `payload`** (per Claude type):
- `claude.permission`: `{"tool_name":"Bash","tool_input":{"command":"npm test"},"permission_suggestions":[...]}`
- `claude.question`: `{"questions":[{"question":"Which DB?","options":[...],"multiSelect":false}]}`
- `claude.elicitation.form`: `{"mcp_server_name":"my-mcp","message":"Provide creds","requested_schema":{...}}`
- `claude.elicitation.url`: `{"mcp_server_name":"my-mcp","message":"Please authenticate","url":"https://..."}`
- `claude.done`: `{"last_action":"npm test"}`
- `claude.error`: `{"last_action":"npm test"}`

**What goes in `response`** (per Claude type):
- `claude.permission` (approve): `{"updated_input":{"command":"npm lint"},"apply_permission":0}` or `{}` for plain approve
- `claude.permission` (deny): `null`
- `claude.question`: `{"answers":{"Which DB?":"PostgreSQL"}}`
- `claude.elicitation.form`: `{"action":"accept","content":{"username":"alice","remember":true}}`
- `claude.elicitation.url`: `{"action":"accept"}` or `{"action":"decline"}`

### DB migration

**File: `internal/store/store.go`**

No migration — just replace the `CREATE TABLE` in `migrate()`. Old data is disposable (pre-release). If the schema doesn't match, delete `helios.db` and restart.

```sql
CREATE TABLE IF NOT EXISTS notifications (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT 'claude',
    source_session TEXT NOT NULL,
    cwd TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    title TEXT,
    detail TEXT,
    payload TEXT,
    response TEXT,
    resolved_at TEXT,
    resolved_source TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status);
CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications(type);
CREATE INDEX IF NOT EXISTS idx_notifications_source_session ON notifications(source_session);
```

### Updated CRUD

**File: `internal/store/notifications.go`**

```go
func (s *Store) CreateNotification(n *Notification) error {
    _, err := s.db.Exec(
        `INSERT INTO notifications (id, source, source_session, cwd, type, status, title, detail, payload)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        n.ID, n.Source, n.SourceSession, n.CWD, n.Type, n.Status, n.Title, n.Detail, n.Payload,
    )
    return err
}

func (s *Store) GetNotification(id string) (*Notification, error) {
    n := &Notification{}
    err := s.db.QueryRow(
        `SELECT id, source, source_session, cwd, type, status, title, detail, payload, response,
                resolved_at, resolved_source, created_at
         FROM notifications WHERE id = ?`, id,
    ).Scan(&n.ID, &n.Source, &n.SourceSession, &n.CWD, &n.Type, &n.Status,
        &n.Title, &n.Detail, &n.Payload, &n.Response,
        &n.ResolvedAt, &n.ResolvedSource, &n.CreatedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    return n, err
}

func (s *Store) ListNotifications(source, status, nType string) ([]Notification, error) {
    query := `SELECT id, source, source_session, cwd, type, status, title, detail, payload, response,
                     resolved_at, resolved_source, created_at
              FROM notifications WHERE 1=1`
    args := []interface{}{}

    if source != "" {
        query += " AND source = ?"
        args = append(args, source)
    }
    if status != "" {
        query += " AND status = ?"
        args = append(args, status)
    }
    if nType != "" {
        query += " AND type = ?"
        args = append(args, nType)
    }

    query += " ORDER BY created_at DESC LIMIT 200"

    rows, err := s.db.Query(query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var result []Notification
    for rows.Next() {
        var n Notification
        if err := rows.Scan(&n.ID, &n.Source, &n.SourceSession, &n.CWD, &n.Type, &n.Status,
            &n.Title, &n.Detail, &n.Payload, &n.Response,
            &n.ResolvedAt, &n.ResolvedSource, &n.CreatedAt); err != nil {
            return nil, err
        }
        result = append(result, n)
    }
    return result, rows.Err()
}

func (s *Store) UpdateNotificationResponse(id, response string) error {
    _, err := s.db.Exec(`UPDATE notifications SET response = ? WHERE id = ?`, response, id)
    return err
}

func (s *Store) LastSessionDetail(sourceSession string) string {
    var detail string
    err := s.db.QueryRow(
        `SELECT detail FROM notifications WHERE source_session = ? AND type LIKE '%.permission' AND detail IS NOT NULL ORDER BY created_at DESC LIMIT 1`,
        sourceSession,
    ).Scan(&detail)
    if err != nil {
        return ""
    }
    return detail
}
```

---

## Notification Manager

### Decision struct

**File: `internal/notifications/manager.go`**

Replace `chan string` with `chan Decision`:

```go
type Decision struct {
    Status   string          `json:"status"`             // "approved", "denied", "answered", etc.
    Response json.RawMessage `json:"response,omitempty"`  // opaque — stored in notification.response
}

type Manager struct {
    db      *store.Store
    mu      sync.Mutex
    pending map[string]chan Decision
}

func NewManager(db *store.Store) *Manager {
    return &Manager{
        db:      db,
        pending: make(map[string]chan Decision),
    }
}

func (m *Manager) WaitForDecision(notifID string) (Decision, error) {
    ch := make(chan Decision, 1)

    m.mu.Lock()
    m.pending[notifID] = ch
    m.mu.Unlock()

    defer func() {
        m.mu.Lock()
        delete(m.pending, notifID)
        m.mu.Unlock()
    }()

    decision := <-ch
    return decision, nil
}

func (m *Manager) Resolve(notifID string, decision Decision, source string) error {
    // Store response in the notification record
    if len(decision.Response) > 0 {
        m.db.UpdateNotificationResponse(notifID, string(decision.Response))
    }

    if err := m.db.ResolveNotification(notifID, decision.Status, source); err != nil {
        return err
    }

    m.mu.Lock()
    ch, ok := m.pending[notifID]
    m.mu.Unlock()

    if ok {
        ch <- decision
    }

    return nil
}
```

`CancelPending` and `CancelPendingFromClaude` remain the same but use `Decision{Status: "timeout"}` / `Decision{Status: "dismissed"}`.

### Notification retention

```go
const maxNotifications = 200
const cleanupInterval = 5 * time.Minute

func (m *Manager) StartCleanup() {
    go func() {
        ticker := time.NewTicker(cleanupInterval)
        defer ticker.Stop()
        for range ticker.C {
            m.db.TruncateNotifications(maxNotifications)
        }
    }()
}
```

**File: `internal/store/notifications.go`**

```go
func (s *Store) TruncateNotifications(keep int) error {
    _, err := s.db.Exec(`
        DELETE FROM notifications
        WHERE id NOT IN (
            SELECT id FROM notifications
            ORDER BY created_at DESC
            LIMIT ?
        )
        AND status != 'pending'
    `, keep)
    return err
}
```

Rules:
- Never delete pending notifications — they have active blocking hooks
- Only delete resolved records beyond the latest 200
- Runs every 5 minutes
- Started in `internal/daemon/daemon.go` after creating the manager

---

## Claude Hook Handlers (Translators)

Hook handlers translate Claude's wire format into generic notifications and back. Each handler:
1. Parses Claude-specific input
2. Creates a generic `Notification` with source-prefixed type and opaque `payload`
3. Blocks waiting for a `Decision`
4. Translates the `Decision.Response` back into Claude's expected hook output format

### hookInput expansion

**File: `internal/server/hooks.go`**

The `hookInput` struct captures the raw body — we parse source-specific fields from it:

```go
type hookInput struct {
    SessionID            string          `json:"session_id"`
    CWD                  string          `json:"cwd"`
    ToolName             string          `json:"tool_name,omitempty"`
    ToolInput            json.RawMessage `json:"tool_input,omitempty"`
    PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
    HookEventName        string          `json:"hook_event_name,omitempty"`
    // Elicitation fields
    MCPServerName   string          `json:"mcp_server_name,omitempty"`
    Message         string          `json:"message,omitempty"`
    Mode            string          `json:"mode,omitempty"`
    RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
    URL             string          `json:"url,omitempty"`
    ElicitationID   string          `json:"elicitation_id,omitempty"`
}
```

### handlePermissionHook (updated)

**File: `internal/server/hooks.go`**

```go
func (s *InternalServer) handlePermissionHook(w http.ResponseWriter, r *http.Request) {
    var input hookInput
    if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
        http.Error(w, "invalid request body", http.StatusBadRequest)
        return
    }

    s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "PermissionRequest")

    notifID := notifications.GenerateNotificationID()
    detail := fmt.Sprintf("%s: %s", input.ToolName, summarizeToolInput(input.ToolInput))

    // Build payload — all Claude-specific data goes here
    payload := map[string]interface{}{
        "tool_name":  input.ToolName,
        "tool_input": json.RawMessage(input.ToolInput),
    }
    if len(input.PermissionSuggestions) > 0 {
        payload["permission_suggestions"] = json.RawMessage(input.PermissionSuggestions)
    }
    payloadJSON, _ := json.Marshal(payload)
    payloadStr := string(payloadJSON)

    notif := &store.Notification{
        ID:            notifID,
        Source:        "claude",
        SourceSession: input.SessionID,
        CWD:           input.CWD,
        Type:          "claude.permission",
        Status:        "pending",
        Title:         &input.ToolName,
        Detail:        &detail,
        Payload:       &payloadStr,
    }

    if err := s.shared.Mgr.CreateNotification(notif); err != nil {
        http.Error(w, "failed to create notification", http.StatusInternalServerError)
        return
    }

    // SSE + push + desktop notification (unchanged pattern)
    s.shared.SSE.Broadcast(SSEEvent{Type: "notification", Data: notif})
    go sendDesktopNotification(detail)
    if s.shared.Pusher != nil {
        go s.shared.Pusher.SendToAll(push.PushPayload{
            Type:  "claude.permission",
            ID:    notifID,
            Title: "Claude needs permission",
            Body:  detail,
            Actions: []push.PushAction{
                {Action: "approve", Title: "Approve"},
                {Action: "deny", Title: "Deny"},
            },
        })
    }

    // Block waiting for decision (5 min timeout)
    timer := time.NewTimer(5 * time.Minute)
    defer timer.Stop()

    decisionCh := make(chan notifications.Decision, 1)
    go func() {
        decision, err := s.shared.Mgr.WaitForDecision(notifID)
        if err != nil {
            decisionCh <- notifications.Decision{Status: "denied"}
            return
        }
        decisionCh <- decision
    }()

    var decision notifications.Decision
    select {
    case decision = <-decisionCh:
    case <-timer.C:
        s.shared.Mgr.CancelPending(notifID)
        decision = notifications.Decision{Status: "denied"}
    case <-r.Context().Done():
        s.shared.Mgr.CancelPendingFromClaude(notifID)
        s.shared.SSE.Broadcast(SSEEvent{
            Type: "notification_resolved",
            Data: map[string]string{"id": notifID, "action": "dismissed", "source": "claude"},
        })
        return
    }

    // Translate Decision back to Claude's PermissionRequest response
    type permResponse struct {
        HookSpecificOutput struct {
            HookEventName string `json:"hookEventName"`
            Decision      struct {
                Behavior           string                 `json:"behavior"`
                Message            string                 `json:"message,omitempty"`
                UpdatedInput       map[string]interface{} `json:"updatedInput,omitempty"`
                UpdatedPermissions json.RawMessage        `json:"updatedPermissions,omitempty"`
            } `json:"decision"`
        } `json:"hookSpecificOutput"`
    }

    resp := permResponse{}
    resp.HookSpecificOutput.HookEventName = "PermissionRequest"

    if decision.Status == "approved" {
        resp.HookSpecificOutput.Decision.Behavior = "allow"

        // Parse response blob for updatedInput / apply_permission
        if len(decision.Response) > 0 {
            var respData struct {
                UpdatedInput    map[string]interface{} `json:"updated_input,omitempty"`
                ApplyPermission *int                   `json:"apply_permission,omitempty"`
            }
            if json.Unmarshal(decision.Response, &respData) == nil {
                if respData.UpdatedInput != nil {
                    resp.HookSpecificOutput.Decision.UpdatedInput = respData.UpdatedInput
                }
                if respData.ApplyPermission != nil {
                    // Look up the original permission_suggestions from payload
                    var p map[string]json.RawMessage
                    if json.Unmarshal(payloadJSON, &p) == nil {
                        if sugRaw, ok := p["permission_suggestions"]; ok {
                            var suggestions []json.RawMessage
                            if json.Unmarshal(sugRaw, &suggestions) == nil {
                                idx := *respData.ApplyPermission
                                if idx >= 0 && idx < len(suggestions) {
                                    resp.HookSpecificOutput.Decision.UpdatedPermissions =
                                        json.RawMessage("[" + string(suggestions[idx]) + "]")
                                }
                            }
                        }
                    }
                }
            }
        }
    } else {
        resp.HookSpecificOutput.Decision.Behavior = "deny"
        resp.HookSpecificOutput.Decision.Message = "Denied via helios"
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

### handleAskUserQuestionHook (new)

**File: `internal/server/hooks.go`**

```go
func (s *InternalServer) handleAskUserQuestionHook(w http.ResponseWriter, r *http.Request) {
    var input hookInput
    if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
        http.Error(w, "invalid request body", http.StatusBadRequest)
        return
    }

    s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "AskUserQuestion")

    notifID := notifications.GenerateNotificationID()
    title := "Claude has a question"
    detail := "Answer required to continue"

    // Payload = the full tool_input (contains questions array)
    payloadStr := string(input.ToolInput)

    notif := &store.Notification{
        ID:            notifID,
        Source:        "claude",
        SourceSession: input.SessionID,
        CWD:           input.CWD,
        Type:          "claude.question",
        Status:        "pending",
        Title:         &title,
        Detail:        &detail,
        Payload:       &payloadStr,
    }

    if err := s.shared.Mgr.CreateNotification(notif); err != nil {
        http.Error(w, "failed to create notification", http.StatusInternalServerError)
        return
    }

    s.shared.SSE.Broadcast(SSEEvent{Type: "notification", Data: notif})
    go sendDesktopNotification(title)
    if s.shared.Pusher != nil {
        go s.shared.Pusher.SendToAll(push.PushPayload{
            Type:  "claude.question",
            ID:    notifID,
            Title: title,
            Body:  detail,
        })
    }

    // Block (5 min)
    timer := time.NewTimer(5 * time.Minute)
    defer timer.Stop()

    decisionCh := make(chan notifications.Decision, 1)
    go func() {
        decision, err := s.shared.Mgr.WaitForDecision(notifID)
        if err != nil {
            decisionCh <- notifications.Decision{Status: "denied"}
            return
        }
        decisionCh <- decision
    }()

    var decision notifications.Decision
    select {
    case decision = <-decisionCh:
    case <-timer.C:
        s.shared.Mgr.CancelPending(notifID)
        decision = notifications.Decision{Status: "denied"}
    case <-r.Context().Done():
        s.shared.Mgr.CancelPendingFromClaude(notifID)
        s.shared.SSE.Broadcast(SSEEvent{
            Type: "notification_resolved",
            Data: map[string]string{"id": notifID, "action": "dismissed", "source": "claude"},
        })
        return
    }

    // Translate back to PreToolUse response
    type preToolUseResponse struct {
        HookSpecificOutput struct {
            HookEventName      string                 `json:"hookEventName"`
            PermissionDecision string                 `json:"permissionDecision"`
            UpdatedInput       map[string]interface{} `json:"updatedInput,omitempty"`
        } `json:"hookSpecificOutput"`
    }

    resp := preToolUseResponse{}
    resp.HookSpecificOutput.HookEventName = "PreToolUse"

    if decision.Status == "answered" && len(decision.Response) > 0 {
        resp.HookSpecificOutput.PermissionDecision = "allow"
        // Parse answers from response, merge into original tool_input
        var answers map[string]interface{}
        json.Unmarshal(decision.Response, &answers)

        var toolInput map[string]interface{}
        json.Unmarshal(input.ToolInput, &toolInput)
        // Merge answer keys into tool_input
        for k, v := range answers {
            toolInput[k] = v
        }
        resp.HookSpecificOutput.UpdatedInput = toolInput
    } else {
        resp.HookSpecificOutput.PermissionDecision = "deny"
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

### handleElicitationHook (new)

**File: `internal/server/hooks.go`**

```go
func (s *InternalServer) handleElicitationHook(w http.ResponseWriter, r *http.Request) {
    var input hookInput
    if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
        http.Error(w, "invalid request body", http.StatusBadRequest)
        return
    }

    s.shared.DB.UpsertHookSession(input.SessionID, input.CWD, "Elicitation")

    notifID := notifications.GenerateNotificationID()
    title := fmt.Sprintf("%s needs input", input.MCPServerName)
    detail := input.Message

    // Determine type based on mode
    notifType := "claude.elicitation.form"
    if input.Mode == "url" {
        notifType = "claude.elicitation.url"
    }

    // Build payload
    payload := map[string]interface{}{
        "mcp_server_name": input.MCPServerName,
        "message":         input.Message,
        "mode":            input.Mode,
    }
    if len(input.RequestedSchema) > 0 {
        payload["requested_schema"] = json.RawMessage(input.RequestedSchema)
    }
    if input.URL != "" {
        payload["url"] = input.URL
    }
    payloadJSON, _ := json.Marshal(payload)
    payloadStr := string(payloadJSON)

    notif := &store.Notification{
        ID:            notifID,
        Source:        "claude",
        SourceSession: input.SessionID,
        CWD:           input.CWD,
        Type:          notifType,
        Status:        "pending",
        Title:         &title,
        Detail:        &detail,
        Payload:       &payloadStr,
    }

    if err := s.shared.Mgr.CreateNotification(notif); err != nil {
        http.Error(w, "failed to create notification", http.StatusInternalServerError)
        return
    }

    s.shared.SSE.Broadcast(SSEEvent{Type: "notification", Data: notif})
    go sendDesktopNotification(title)
    if s.shared.Pusher != nil {
        go s.shared.Pusher.SendToAll(push.PushPayload{
            Type:  notifType,
            ID:    notifID,
            Title: title,
            Body:  detail,
        })
    }

    // Block (5 min)
    timer := time.NewTimer(5 * time.Minute)
    defer timer.Stop()

    decisionCh := make(chan notifications.Decision, 1)
    go func() {
        decision, err := s.shared.Mgr.WaitForDecision(notifID)
        if err != nil {
            decisionCh <- notifications.Decision{Status: "denied"}
            return
        }
        decisionCh <- decision
    }()

    var decision notifications.Decision
    select {
    case decision = <-decisionCh:
    case <-timer.C:
        s.shared.Mgr.CancelPending(notifID)
        decision = notifications.Decision{Status: "denied"}
    case <-r.Context().Done():
        s.shared.Mgr.CancelPendingFromClaude(notifID)
        s.shared.SSE.Broadcast(SSEEvent{
            Type: "notification_resolved",
            Data: map[string]string{"id": notifID, "action": "dismissed", "source": "claude"},
        })
        return
    }

    // Translate back to Elicitation response
    type elicitationResponse struct {
        HookSpecificOutput struct {
            HookEventName string                 `json:"hookEventName"`
            Action        string                 `json:"action"`
            Content       map[string]interface{} `json:"content,omitempty"`
        } `json:"hookSpecificOutput"`
    }

    resp := elicitationResponse{}
    resp.HookSpecificOutput.HookEventName = "Elicitation"

    if len(decision.Response) > 0 {
        var respData struct {
            Action  string                 `json:"action"`
            Content map[string]interface{} `json:"content,omitempty"`
        }
        if json.Unmarshal(decision.Response, &respData) == nil {
            resp.HookSpecificOutput.Action = respData.Action
            resp.HookSpecificOutput.Content = respData.Content
        } else {
            resp.HookSpecificOutput.Action = "decline"
        }
    } else {
        resp.HookSpecificOutput.Action = "decline"
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

### Update existing handlers for generic model

`handleStopHook` and `handleStopFailureHook` update to use `source: "claude"`, `source_session`, `type: "claude.done"` / `type: "claude.error"`.

---

## Hook Installation

**File: `internal/daemon/hooks.go`**

Add `PreToolUse` and `Elicitation` hooks to `hookConfig`:

```go
func hookConfig(port int) map[string]interface{} {
    base := fmt.Sprintf("http://localhost:%d", port)
    return map[string]interface{}{
        "hooks": map[string]interface{}{
            "PermissionRequest": []interface{}{
                map[string]interface{}{
                    "matcher": "*",
                    "hooks": []interface{}{
                        map[string]interface{}{
                            "type":    "http",
                            "url":     base + "/hooks/permission",
                            "timeout": 300,
                        },
                    },
                },
            },
            "PreToolUse": []interface{}{
                map[string]interface{}{
                    "matcher": "AskUserQuestion",
                    "hooks": []interface{}{
                        map[string]interface{}{
                            "type":    "http",
                            "url":     base + "/hooks/ask-user-question",
                            "timeout": 300,
                        },
                    },
                },
            },
            "Elicitation": []interface{}{
                map[string]interface{}{
                    "matcher": "*",
                    "hooks": []interface{}{
                        map[string]interface{}{
                            "type":    "http",
                            "url":     base + "/hooks/elicitation",
                            "timeout": 300,
                        },
                    },
                },
            },
            // ... existing Stop, StopFailure, Notification, SessionStart, SessionEnd unchanged
        },
    }
}
```

---

## API — Unified Action Endpoint

### Single generic endpoint

Instead of separate `/approve`, `/deny`, `/answer`, `/elicitation` endpoints, there is **one endpoint**:

```
POST /api/notifications/:id/action
```

The body is freeform JSON. The backend looks up the notification by `id`, reads its `type`, and dispatches to a **type-specific action handler**. The mobile app always POSTs to this single URL — it just varies the body based on the card type.

### Action handler registry

**File: `internal/server/api.go`**

```go
// ActionHandler processes a user action for a specific notification type.
// It receives the raw body, returns a Decision to unblock the hook.
type ActionHandler func(notif *store.Notification, body json.RawMessage) (notifications.Decision, error)

// actionHandlers maps notification type → handler
var actionHandlers = map[string]ActionHandler{
    "claude.permission":       handleClaudePermissionAction,
    "claude.question":         handleClaudeQuestionAction,
    "claude.elicitation.form": handleClaudeElicitationAction,
    "claude.elicitation.url":  handleClaudeElicitationAction,
}
```

### Generic action endpoint

```go
func (s *PublicServer) handleNotificationAction(w http.ResponseWriter, r *http.Request) {
    id := extractPathParam(r.URL.Path, "/api/notifications/", "/action")
    if id == "" {
        jsonError(w, "missing notification id", http.StatusBadRequest)
        return
    }

    // Read body
    body, err := io.ReadAll(r.Body)
    if err != nil {
        jsonError(w, "invalid request body", http.StatusBadRequest)
        return
    }

    // Look up notification to get its type
    notif, err := s.shared.Mgr.GetNotification(id)
    if err != nil || notif == nil {
        jsonError(w, "notification not found", http.StatusNotFound)
        return
    }
    if notif.Status != "pending" {
        jsonResponse(w, http.StatusGone, map[string]interface{}{
            "success": false, "error": "already_resolved",
        })
        return
    }

    // Find handler for this type
    handler, ok := actionHandlers[notif.Type]
    if !ok {
        jsonError(w, fmt.Sprintf("no action handler for type: %s", notif.Type), http.StatusBadRequest)
        return
    }

    // Dispatch to type-specific handler
    decision, err := handler(notif, json.RawMessage(body))
    if err != nil {
        jsonError(w, err.Error(), http.StatusBadRequest)
        return
    }

    source := "browser"
    if kid, ok := r.Context().Value(deviceKIDKey).(string); ok {
        source = "device:" + kid
    }

    if err := s.shared.Mgr.Resolve(id, decision, source); err != nil {
        if _, ok := err.(*store.AlreadyResolvedError); ok {
            jsonResponse(w, http.StatusGone, map[string]interface{}{
                "success": false, "error": "already_resolved",
            })
            return
        }
        jsonError(w, "failed to process action", http.StatusInternalServerError)
        return
    }

    s.shared.SSE.Broadcast(SSEEvent{
        Type: "notification_resolved",
        Data: map[string]string{"id": id, "action": decision.Status, "source": source},
    })

    jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
}
```

### Type-specific action handlers

#### claude.permission

```go
func handleClaudePermissionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
    var req struct {
        Action          string                 `json:"action"`           // "approve" or "deny"
        UpdatedInput    map[string]interface{} `json:"updated_input,omitempty"`
        ApplyPermission *int                   `json:"apply_permission,omitempty"`
    }
    if err := json.Unmarshal(body, &req); err != nil {
        return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
    }

    if req.Action == "deny" {
        return notifications.Decision{Status: "denied"}, nil
    }

    // Build response blob with any rich data
    respData := map[string]interface{}{}
    if req.UpdatedInput != nil {
        respData["updated_input"] = req.UpdatedInput
    }
    if req.ApplyPermission != nil {
        respData["apply_permission"] = *req.ApplyPermission
    }
    var response json.RawMessage
    if len(respData) > 0 {
        response, _ = json.Marshal(respData)
    }

    return notifications.Decision{Status: "approved", Response: response}, nil
}
```

Mobile sends:
```json
// Simple approve
{"action": "approve"}

// Approve with edited input
{"action": "approve", "updated_input": {"command": "npm run lint"}}

// Approve with always-allow rule
{"action": "approve", "apply_permission": 0}

// Deny
{"action": "deny"}
```

#### claude.question

```go
func handleClaudeQuestionAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
    var req struct {
        Action  string            `json:"action"`  // "answer" or "skip"
        Answers map[string]string `json:"answers"`
    }
    if err := json.Unmarshal(body, &req); err != nil {
        return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
    }

    if req.Action == "skip" {
        return notifications.Decision{Status: "denied"}, nil
    }

    if len(req.Answers) == 0 {
        return notifications.Decision{}, fmt.Errorf("missing answers")
    }

    response, _ := json.Marshal(map[string]interface{}{"answers": req.Answers})
    return notifications.Decision{Status: "answered", Response: response}, nil
}
```

Mobile sends:
```json
{"action": "answer", "answers": {"Which database?": "PostgreSQL"}}
```

#### claude.elicitation (form + url)

```go
func handleClaudeElicitationAction(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
    var req struct {
        Action  string                 `json:"action"`  // "accept", "decline", "cancel"
        Content map[string]interface{} `json:"content,omitempty"`
    }
    if err := json.Unmarshal(body, &req); err != nil {
        return notifications.Decision{}, fmt.Errorf("invalid body: %w", err)
    }

    if req.Action != "accept" && req.Action != "decline" && req.Action != "cancel" {
        return notifications.Decision{}, fmt.Errorf("action must be accept/decline/cancel")
    }

    status := "answered"
    if req.Action == "decline" || req.Action == "cancel" {
        status = "denied"
    }

    response, _ := json.Marshal(map[string]interface{}{
        "action":  req.Action,
        "content": req.Content,
    })
    return notifications.Decision{Status: status, Response: response}, nil
}
```

Mobile sends:
```json
// Form accept
{"action": "accept", "content": {"username": "alice", "remember": true}}

// URL done
{"action": "accept"}

// Decline
{"action": "decline"}
```

### Batch actions

The existing batch endpoint stays but uses the action handler registry:

```go
func (s *PublicServer) handleBatchNotifications(w http.ResponseWriter, r *http.Request) {
    var req struct {
        NotificationIDs []string        `json:"notification_ids"`
        Action          json.RawMessage `json:"action"` // passed to each handler
    }
    // ... for each ID, look up type, dispatch to handler, resolve
}
```

### Dismiss (non-blocking notifications)

Dismiss stays as a separate thin endpoint since it applies to all types equally and doesn't go through type-specific logic:

```go
// POST /api/notifications/:id/dismiss — works for any type
func (s *PublicServer) handleDismissNotification(w http.ResponseWriter, r *http.Request) {
    // ... unchanged, resolves with Decision{Status: "dismissed"}
}
```

### Wire routes

**File: `internal/server/server.go`**

Internal mux — add:
```go
mux.HandleFunc("POST /hooks/ask-user-question", s.handleAskUserQuestionHook)
mux.HandleFunc("POST /hooks/elicitation", s.handleElicitationHook)
```

Protected mux dynamic handler — replace separate approve/deny/answer/elicitation with:
```go
protectedMux.HandleFunc("/api/notifications/", func(w http.ResponseWriter, r *http.Request) {
    path := r.URL.Path
    switch {
    case r.Method == "POST" && strings.HasSuffix(path, "/action"):
        s.handleNotificationAction(w, r)
    case r.Method == "POST" && strings.HasSuffix(path, "/dismiss"):
        s.handleDismissNotification(w, r)
    default:
        http.NotFound(w, r)
    }
})
```

---

## Mobile App — Type-Based Cards

### Core principle

The mobile app renders cards based on the notification `type` field, **not** a generic interaction pattern. Each card type:
- Knows its own layout and UI components
- Knows which API endpoint to call for actions
- Parses the `payload` JSON internally
- Constructs the `response` body for its specific API

This means `ClaudePermissionCard` and a future `HermesApprovalCard` can look and behave completely differently even though both are "approval" interactions.

### Updated notification model

**File: `mobile/lib/models/notification.dart`**

```dart
class HeliosNotification {
  final String id;
  final String source;
  final String sourceSession;
  final String cwd;
  final String type;       // "claude.permission", "claude.question", etc.
  final String status;
  final String? title;
  final String? detail;
  final Map<String, dynamic>? payload;   // parsed from JSON
  final Map<String, dynamic>? response;  // parsed from JSON
  final String? resolvedAt;
  final String? resolvedSource;
  final String createdAt;

  HeliosNotification({
    required this.id,
    required this.source,
    required this.sourceSession,
    required this.cwd,
    required this.type,
    required this.status,
    this.title,
    this.detail,
    this.payload,
    this.response,
    this.resolvedAt,
    this.resolvedSource,
    required this.createdAt,
  });

  factory HeliosNotification.fromJson(Map<String, dynamic> json) {
    Map<String, dynamic>? parseJson(String? raw) {
      if (raw == null) return null;
      try { return jsonDecode(raw) as Map<String, dynamic>; } catch (_) { return null; }
    }
    return HeliosNotification(
      id: json['id'] as String,
      source: json['source'] as String? ?? 'claude',
      sourceSession: json['source_session'] as String,
      cwd: json['cwd'] as String,
      type: json['type'] as String,
      status: json['status'] as String,
      title: json['title'] as String?,
      detail: json['detail'] as String?,
      payload: json['payload'] is String ? parseJson(json['payload']) : json['payload'] as Map<String, dynamic>?,
      response: json['response'] is String ? parseJson(json['response']) : json['response'] as Map<String, dynamic>?,
      resolvedAt: json['resolved_at'] as String?,
      resolvedSource: json['resolved_source'] as String?,
      createdAt: json['created_at'] as String,
    );
  }

  bool get isPending => status == 'pending';

  // Type checks — cards use these to decide which widget to render
  bool get isClaudePermission => type == 'claude.permission';
  bool get isClaudeQuestion => type == 'claude.question';
  bool get isClaudeElicitationForm => type == 'claude.elicitation.form';
  bool get isClaudeElicitationUrl => type == 'claude.elicitation.url';
  bool get isClaudeElicitation => type.startsWith('claude.elicitation.');
  bool get isClaudeDone => type == 'claude.done';
  bool get isClaudeError => type == 'claude.error';

  /// Whether this notification needs user action
  bool get needsAction => isPending && (isClaudePermission || isClaudeQuestion || isClaudeElicitation);

  String get displayTitle => title ?? _typeLabel;
  String get displayDetail => detail ?? 'No details';

  String get _typeLabel {
    switch (type) {
      case 'claude.permission': return 'Permission request';
      case 'claude.question': return 'Question';
      case 'claude.elicitation.form': return 'Input requested';
      case 'claude.elicitation.url': return 'Authentication required';
      case 'claude.done': return 'Session completed';
      case 'claude.error': return 'Session error';
      default: return type;
    }
  }

  // Payload accessors for claude.permission
  String? get toolName => payload?['tool_name'] as String?;
  String? get toolInput {
    final ti = payload?['tool_input'];
    if (ti is String) return ti;
    if (ti is Map) return jsonEncode(ti);
    return null;
  }
  List<dynamic>? get permissionSuggestions => payload?['permission_suggestions'] as List?;

  // Payload accessors for claude.question
  List<dynamic>? get questions => payload?['questions'] as List?;

  // Payload accessors for claude.elicitation
  String? get mcpServerName => payload?['mcp_server_name'] as String?;
  String? get elicitationMessage => payload?['message'] as String?;
  Map<String, dynamic>? get requestedSchema => payload?['requested_schema'] as Map<String, dynamic>?;
  String? get elicitationUrl => payload?['url'] as String?;

  String get timeAgo {
    // ... unchanged
  }
}
```

### Updated SSE service

**File: `mobile/lib/services/sse_service.dart`**

All actions go through one method:

```dart
/// Send an action for any notification type.
/// The body is type-specific — each card widget builds it.
Future<bool> sendAction(String id, Map<String, dynamic> body) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/notifications/$id/action', body: body);
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to send action: $e');
    }
    return false;
}
```

Each card calls `sendAction` with its own body shape:

```dart
// ClaudePermissionCard
_sse.sendAction(n.id, {'action': 'approve'});
_sse.sendAction(n.id, {'action': 'approve', 'updated_input': {'command': 'npm lint'}});
_sse.sendAction(n.id, {'action': 'approve', 'apply_permission': 0});
_sse.sendAction(n.id, {'action': 'deny'});

// ClaudeQuestionCard
_sse.sendAction(n.id, {'action': 'answer', 'answers': {'Which DB?': 'PostgreSQL'}});

// ClaudeElicitationFormCard
_sse.sendAction(n.id, {'action': 'accept', 'content': {'username': 'alice'}});
_sse.sendAction(n.id, {'action': 'decline'});
```

### Dashboard — card routing by type

**File: `mobile/lib/screens/dashboard_screen.dart`**

The dashboard routes to card widgets based on `type`:

```dart
Widget _buildNotificationCard(HeliosNotification n) {
    if (!n.isPending) return _buildHistoryCard(n);

    switch (n.type) {
      case 'claude.permission':
        return _buildClaudePermissionCard(n);
      case 'claude.question':
        return _buildClaudeQuestionCard(n);
      case 'claude.elicitation.form':
        return _buildClaudeElicitationFormCard(n);
      case 'claude.elicitation.url':
        return _buildClaudeElicitationUrlCard(n);
      case 'claude.error':
        return _buildStatusCard(n);
      default:
        return _buildStatusCard(n);
    }
}
```

Each card type is a separate method (or could be extracted to a separate widget file later).

### ClaudePermissionCard (enhanced)

Adds to the existing card:
1. **Quick rules section** — if `n.permissionSuggestions` is non-empty, show checkboxes for each rule. When approving with a checked rule, include `apply_permission: index` in the approve body
2. **Edit input** — "Edit before approving" expands a `TextField` pre-filled with `n.toolInput`. When edited, include `updated_input` in the approve body

```
┌──────────────────────────────────────┐
│  ☐  claude.permission    Bash        │
│                                      │
│  ┌────────────────────────────────┐  │
│  │ npm run test --coverage        │  │
│  └────────────────────────────────┘  │
│                                      │
│  ~/workspace/helios          2s ago  │
│                                      │
│  ┌─ Quick rules ─────────────────┐  │
│  │ ☐ Always allow 'npm run test' │  │
│  │ ☐ Always allow Bash(npm *)    │  │
│  └───────────────────────────────┘  │
│                                      │
│  ┌──────────┐    ┌──────────────┐   │
│  │  Approve  │    │    Deny     │   │
│  └──────────┘    └──────────────┘   │
│                                      │
│  [ Edit command before approving ]   │
└──────────────────────────────────────┘
```

### ClaudeQuestionCard (new)

Renders questions from `n.questions` array. Each question shows radio buttons (single-select) or checkboxes (multi-select). Submitting calls `_sse.answerQuestion(n.id, {"answers": {...}})`.

```
┌──────────────────────────────────────┐
│  ?  claude.question                  │
│                                      │
│  Which database should I use?        │
│                                      │
│  ○ PostgreSQL                        │
│  ○ SQLite                            │
│  ● MySQL                             │
│                                      │
│  ~/workspace/helios          5s ago  │
│                                      │
│  ┌──────────────────────────────┐    │
│  │         Submit Answer        │    │
│  └──────────────────────────────┘    │
└──────────────────────────────────────┘
```

Multi-question variant with headers:
```
┌──────────────────────────────────────┐
│  ?  claude.question                  │
│                                      │
│  Framework                           │
│  Which framework should I use?       │
│  ○ React  ○ Vue  ○ Angular           │
│                                      │
│  Features                            │
│  Which features to include?          │
│  ☐ Auth  ☐ Logging  ☐ Metrics       │
│                                      │
│  ┌──────────────────────────────┐    │
│  │         Submit Answers       │    │
│  └──────────────────────────────┘    │
└──────────────────────────────────────┘
```

### ClaudeElicitationFormCard (stub)

Dynamic JSON Schema form rendering is complex. For now, render a **"not supported yet"** placeholder with a decline button so the hook doesn't hang forever.

```
┌──────────────────────────────────────┐
│  ⟡  claude.elicitation.form         │
│      my-mcp-server                   │
│                                      │
│  Please provide your credentials     │
│                                      │
│  Form input not yet supported.       │
│  Decline to let the agent continue.  │
│                                      │
│  ┌──────────────────────────────┐   │
│  │           Decline            │   │
│  └──────────────────────────────┘   │
└──────────────────────────────────────┘
```

The method exists in the codebase (`_buildClaudeElicitationFormCard`) so the card router works. Full form rendering is a future iteration.

### ClaudeElicitationUrlCard (new)

Shows the MCP server's message and a button to open the URL. After the user completes the external flow, they tap "Done".

```
┌──────────────────────────────────────┐
│  ⟡  claude.elicitation.url          │
│      my-mcp-server                   │
│                                      │
│  Please authenticate                 │
│                                      │
│  ┌──────────────────────────────┐    │
│  │  Open in Browser             │    │
│  └──────────────────────────────┘    │
│                                      │
│  ┌──────────┐    ┌──────────────┐   │
│  │   Done    │    │   Decline   │   │
│  └──────────┘    └──────────────┘   │
└──────────────────────────────────────┘
```

### Push notifications

| Type | Title | Body | Actions |
|---|---|---|---|
| `claude.permission` | Claude needs permission | Bash: npm test | Approve, Deny |
| `claude.question` | Claude has a question | Answer required to continue | Open (in-app) |
| `claude.elicitation.form` | my-mcp-server needs input | Please provide your credentials | Open (in-app) |
| `claude.elicitation.url` | my-mcp-server needs auth | Please authenticate | Open (in-app) |

### Notification service update

**File: `mobile/lib/services/notification_service.dart`**

Add notification channels per type. The `_handleSSEEvent` in `dashboard_screen.dart` should route by `type` field:

```dart
void _handleSSEEvent(SSEEvent event) {
    if (event.type == 'notification' && event.data is Map) {
      final data = event.data as Map;
      final type = data['type']?.toString() ?? '';
      final id = data['id']?.toString() ?? '';

      if (type == 'claude.permission') {
        NotificationService.instance.showPermissionNotification(
          id: id,
          toolName: data['title']?.toString() ?? 'Unknown tool',
          detail: data['detail']?.toString() ?? 'Permission requested',
        );
      } else if (type == 'claude.question') {
        NotificationService.instance.showNotification(
          id: id,
          title: 'Claude has a question',
          body: data['detail']?.toString() ?? 'Answer required',
        );
      } else if (type.startsWith('claude.elicitation')) {
        NotificationService.instance.showNotification(
          id: id,
          title: data['title']?.toString() ?? 'Input requested',
          body: data['detail']?.toString() ?? 'Input required',
        );
      }
    }
}
```

---

## Future Source Extensibility

When adding a new source (e.g. hermes), the pattern is:

1. **Add hook/webhook handlers** — `handleHermesApprovalHook`, etc. in a new file or `hooks.go`. Each creates a notification with `source: "hermes"`, `type: "hermes.approval"`, and a hermes-specific `payload`
2. **Register action handler** — add `"hermes.approval": handleHermesApprovalAction` to `actionHandlers` map. The handler parses hermes-specific action body and returns a `Decision`
3. **Add mobile card widget** — `HermesApprovalCard` that knows hermes's payload schema and constructs the body for `sendAction`. Add `case 'hermes.approval':` to the dashboard card router

The API endpoint (`/api/notifications/:id/action`) requires **no changes**. The generic storage layer requires no changes. The notification manager requires no changes. Only the edges change: hook handlers, action handlers, and mobile cards.

---

## File Change Summary

### Go Backend

| File | Change |
|------|--------|
| `internal/store/store.go` | Migration: recreate `notifications` table with generic schema (`source`, `source_session`, `type`, `payload`, `response`) |
| `internal/store/notifications.go` | Replace `Notification` struct with generic version. Update all CRUD. Add `UpdateNotificationResponse`, `TruncateNotifications` |
| `internal/notifications/manager.go` | Add `Decision` struct (`Status` + `Response` blob). Change `pending` from `chan string` to `chan Decision`. Update `WaitForDecision`, `Resolve`. Add `StartCleanup` |
| `internal/server/hooks.go` | Expand `hookInput`. Update `handlePermissionHook` for generic model + `updatedInput`/`updatedPermissions` translation. Add `handleAskUserQuestionHook`, `handleElicitationHook` |
| `internal/server/api.go` | Remove separate approve/deny endpoints. Add `ActionHandler` registry, `handleNotificationAction` dispatcher, type-specific handlers (`handleClaudePermissionAction`, `handleClaudeQuestionAction`, `handleClaudeElicitationAction`) |
| `internal/server/server.go` | Wire `/hooks/ask-user-question`, `/hooks/elicitation`. Replace `/approve` `/deny` routes with single `/action` route |
| `internal/daemon/hooks.go` | Add `PreToolUse` (AskUserQuestion) and `Elicitation` hook configs |
| `internal/daemon/daemon.go` | Call `mgr.StartCleanup()` after creating manager |

### Flutter Mobile

| File | Change |
|------|--------|
| `mobile/lib/models/notification.dart` | Replace with generic model (`source`, `sourceSession`, `type`, `payload`, `response`). Type-based getters for Claude-specific payload fields |
| `mobile/lib/screens/dashboard_screen.dart` | Route cards by `type`. Add `_buildClaudePermissionCard` (enhanced with suggestions + edit), `_buildClaudeQuestionCard`, `_buildClaudeElicitationFormCard`, `_buildClaudeElicitationUrlCard` |
| `mobile/lib/services/sse_service.dart` | Replace `approveNotification`/`denyNotification` with single `sendAction` method. Remove old approve/deny methods |
| `mobile/lib/services/notification_service.dart` | Route local notifications by `type` prefix |

---

## Implementation Order

| Step | What | Files |
|------|------|-------|
| 1 | `Decision` struct + manager refactor + `StartCleanup` | `manager.go`, `daemon.go` |
| 2 | DB migration: generic `notifications` table + `TruncateNotifications` | `store.go`, `notifications.go` |
| 3 | Unified action endpoint + `ActionHandler` registry + `handleClaudePermissionAction` | `api.go`, `server.go` |
| 4 | Expand `hookInput` + update `handlePermissionHook` for generic model | `hooks.go` |
| 5 | Update `handleStopHook` / `handleStopFailureHook` for generic model | `hooks.go` |
| 6 | Mobile: generic notification model + `sendAction` method | `notification.dart`, `sse_service.dart` |
| 7 | Mobile: enhanced `ClaudePermissionCard` (suggestions + edit) | `dashboard_screen.dart` |
| 8 | `handleAskUserQuestionHook` + install hook + `handleClaudeQuestionAction` | `hooks.go`, `daemon/hooks.go`, `api.go` |
| 9 | Mobile: `ClaudeQuestionCard` | `dashboard_screen.dart` |
| 10 | `handleElicitationHook` + install hook + `handleClaudeElicitationAction` | `hooks.go`, `daemon/hooks.go`, `api.go` |
| 11 | Mobile: `ClaudeElicitationFormCard` + `ClaudeElicitationUrlCard` | `dashboard_screen.dart` |

---

## Verification

### Generic storage
1. Create a notification with `source: "claude"`, `type: "claude.permission"` → verify `payload` stores full JSON blob
2. Resolve with response → verify `response` column populated
3. List with `?source=claude` filter → only Claude notifications returned
4. Retention: create 250 resolved notifications + 5 pending → truncate keeps all pending + latest 200 resolved

### Phase 1 — Rich Permission Approvals
1. Claude requests `Bash: npm test` → permission card shows `permissionSuggestions` as checkboxes
2. Approve with "Always allow npm test" checked → verify `response` has `apply_permission: 0`, Claude gets `updatedPermissions` in hook response
3. Edit command → change to `npm run lint` → approve → verify Claude gets `updatedInput` in hook response
4. Plain approve (no body) still works
5. Deny still works

### Phase 2 — AskUserQuestion
1. Claude uses `AskUserQuestion` with options → `claude.question` card appears
2. Select option → Submit → verify `response` has `answers` map, Claude receives `updatedInput` with merged answers
3. Multi-select → checkboxes → answers joined correctly
4. Timeout → auto-deny
5. Claude disconnects → card shows "dismissed by Claude"

### Phase 3 — Elicitation
1. MCP triggers form elicitation → `claude.elicitation.form` card with dynamic form
2. Fill form → Submit → verify `response` has `action: "accept"` + `content`, Claude receives correct `hookSpecificOutput`
3. Decline → `action: "decline"` passed through
4. URL mode → `claude.elicitation.url` card with "Open in Browser" button
5. Timeout → auto-decline
