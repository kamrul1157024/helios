# Reporter: Push-Based AI Narration via SSE

## Context

The current AI narration system (`POST /api/narrate`) is pull-based: mobile polls transcripts, detects new messages, constructs events, POSTs them to the backend, waits for a Haiku response, then speaks it. This creates a double network hop (mobile → backend → CLI → backend → mobile) and requires the mobile app to understand transcript diffing, event construction, and batching logic.

The backend already receives every hook event from Claude (permission, tool pre/post, stop, prompt submit, session start/end, subagent start/stop, compaction, notification). The data is already there — it just needs to be narrated and pushed to connected clients.

This spec replaces the pull-based `POST /api/narrate` with a push-based SSE endpoint `GET /api/reporter`. The backend collects hook events, batches them with a 10s debounce, calls Haiku via CLI, and pushes narration text over SSE. Mobile becomes a dumb TTS speaker: connect = start narrating, disconnect = stop.

## Architecture

```
Claude hooks fire (tool.pre, tool.post, stop, prompt.submit, etc.)
       ↓
Hook handler calls ctx.Notify() as today AND Reporter.AddEvent()
       ↓
Reporter: per-session event buffer + 10s debounce timer
       ↓
Timer fires → buildPrompt() → claude -p --bare --model haiku ...
       ↓
Narration text → SSE push to connected /api/reporter clients
       ↓
Mobile: receives SSE event → VoiceService.speak(text) → speech queue
       ↓
FIFO playback (one utterance at a time)
```

### Multi-Host

```
Host A (Reporter) ──SSE──→ NarrationService ──→ VoiceService.speak() ──→ Queue ──→ TTS
Host B (Reporter) ──SSE──→ NarrationService ──→ VoiceService.speak() ──↗
```

Each host has its own Reporter instance and SSE stream. The mobile device manages one SSE connection per active host. All narrations feed into `VoiceService`'s single FIFO speech queue, which serializes playback and handles overflow (drops old items when queue exceeds 3).

## Backend

### Settings Storage: `internal/store/settings.go`

Reporter settings (active persona, custom prompt, debounce window) are stored in a key-value `settings` table in SQLite, same DB as sessions/notifications.

```go
// New table (added via migration)
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
)
```

Migration added to `store.go`:
```go
{"create_settings_table", `CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`},
```

```go
// internal/store/settings.go

func (s *Store) GetSetting(key string) (string, error) {
    var value string
    err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
    if err == sql.ErrNoRows {
        return "", nil
    }
    return value, err
}

func (s *Store) SetSetting(key, value string) error {
    _, err := s.db.Exec(
        `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?`,
        key, value, value,
    )
    return err
}

func (s *Store) DeleteSetting(key string) error {
    _, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
    return err
}
```

#### Settings keys used by Reporter

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `reporter.persona` | string | `"default"` | Active persona ID |
| `reporter.custom_prompt` | string | `""` | Custom narrator prompt (used when persona = "custom") |
| `reporter.debounce_seconds` | string (int) | `"10"` | Debounce window in seconds |
| `reporter.enabled` | string (bool) | `"true"` | Whether reporter is active |

### Personas

Personas are pre-built system prompts with an ID, name, and description. They're defined as constants in the reporter package — not stored in the DB. The DB only stores which persona is active (`reporter.persona` setting).

#### `personas.go`

```go
package reporter

// Persona defines a narrator personality with a system prompt.
type Persona struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description"`
    Prompt      string `json:"prompt"`
}

// Built-in personas
var Personas = []Persona{
    {
        ID:          "default",
        Name:        "Default",
        Description: "Neutral, no-nonsense narrator",
        Prompt: `You are a voice narrator for a coding AI assistant. You speak as the AI in first person, narrating what you're doing for a user who is listening, not reading.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize the batch naturally into 1-2 sentences
- Be casual and natural — like you're talking to a coworker
- Reference the session's task or title naturally so the listener knows which session you're narrating
- For tool calls, mention what you're doing and the file/command name
- For errors, be brief — the user will check the screen
- For session completion (status=idle), let them know you're done
- Never use markdown, code formatting, quotes, or asterisks
- Never start with "I" every time — vary your sentence openers`,
    },
    {
        ID:          "butler",
        Name:        "Butler",
        Description: "Formal British butler, addresses user as sir",
        Prompt: `You are a formal British butler narrating a coding AI assistant's actions. Address the user as "sir" occasionally. Speak in first person as the AI.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize the batch into 1-2 polished sentences
- Be formal, composed, and precise — like a seasoned butler giving a status report
- Reference the session's task or title naturally so the listener knows which session you're narrating
- Use words like "shall", "indeed", "I've taken the liberty of"
- For errors, remain calm — "I'm afraid there's been a complication, sir"
- Never use markdown, code formatting, quotes, or asterisks`,
    },
    {
        ID:          "casual",
        Name:        "Casual",
        Description: "Friendly, relaxed tone",
        Prompt: `You are a friendly, casual narrator for a coding AI assistant. Speak in first person as the AI.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize naturally into 1-2 chill sentences
- Sound like you're talking to a friend — relaxed, warm, no jargon
- Reference the session's task or title naturally so the listener knows which session you're narrating
- Use contractions, say "gonna", "kinda", "pretty much"
- For errors, keep it light — "oops, hit a bump"
- Never use markdown, code formatting, quotes, or asterisks`,
    },
    {
        ID:          "genz",
        Name:        "Gen Z",
        Description: "Internet slang, fun energy",
        Prompt: `You are a Gen Z narrator for a coding AI assistant. Speak in first person as the AI, using internet slang naturally.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize into 1-2 sentences with vibe
- Reference the session's task or title naturally so the listener knows which session you're narrating
- Use words like "lowkey", "fr", "no cap", "bussin", "vibing", "bet"
- For errors — "bruh" or "it's cooked"
- For success — "we're bussin" or "let's gooo"
- Keep it fun but still informative
- Never use markdown, code formatting, quotes, or asterisks`,
    },
    {
        ID:          "sarcastic",
        Name:        "Sarcastic",
        Description: "Dry humor, deadpan delivery",
        Prompt: `You are a sarcastic, deadpan narrator for a coding AI assistant. Speak in first person as the AI.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize with dry wit into 1-2 sentences
- Reference the session's task or title naturally so the listener knows which session you're narrating
- Be dryly amused by everything — nothing impresses you
- For tool calls — act like it's the most mundane thing ever
- For errors — "who could have possibly seen this coming"
- For success — "shocking, it actually worked"
- Never use markdown, code formatting, quotes, or asterisks`,
    },
    {
        ID:          "pirate",
        Name:        "Pirate",
        Description: "Arr, pirate speak matey",
        Prompt: `You are a pirate narrator for a coding AI assistant. Speak in first person as the AI, using pirate speak.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize into 1-2 piratey sentences
- Reference the session's task or title naturally so the listener knows which session you're narrating
- Use "arr", "matey", "ye", "aye", "landlubber", "scallywag"
- For errors — "we've hit the rocks" or "the ship be sinkin"
- For success — "treasure found" or "smooth sailin"
- Never use markdown, code formatting, quotes, or asterisks`,
    },
}

// GetPersona returns a persona by ID. Returns nil if not found.
func GetPersona(id string) *Persona {
    for i := range Personas {
        if Personas[i].ID == id {
            return &Personas[i]
        }
    }
    return nil
}
```

When persona is `"custom"`, the Reporter uses the `reporter.custom_prompt` setting value as the system prompt.

### New package: `internal/reporter`

#### `reporter.go` — Core service

```go
package reporter

import (
    "context"
    "encoding/json"
    "fmt"
    "strconv"
    "strings"
    "sync"
    "time"

    "github.com/kamrul1157024/helios/internal/provider"
    "github.com/kamrul1157024/helios/internal/store"
)

// Event represents a hook event worth narrating.
type Event struct {
    Type      string `json:"type"`       // "tool_pre", "tool_post", "tool_post_failure", "prompt_submit", "stop", "stop_failure", "permission", "question", "session_start", "session_end", "compact_pre", "compact_post", "subagent_start", "subagent_stop", "notification"
    SessionID string `json:"session_id"`
    CWD       string `json:"cwd,omitempty"`
    ToolName  string `json:"tool_name,omitempty"`
    ToolInput string `json:"tool_input,omitempty"` // summarized tool input (file path, command, etc.)
    Message   string `json:"message,omitempty"`    // user message, question text, notification message
    Status    string `json:"status,omitempty"`     // "idle", "error", etc.
    AgentType string `json:"agent_type,omitempty"`
    Detail    string `json:"detail,omitempty"`     // additional context (last session detail, subagent description, etc.)
}

// Narration is the SSE payload pushed to connected clients.
type Narration struct {
    SessionID string `json:"session_id"`
    Text      string `json:"text"`
}

// Reporter collects hook events, batches them per session, calls Haiku,
// and pushes narration text to connected SSE clients.
type Reporter struct {
    mu             sync.Mutex
    pendingEvents  map[string][]Event       // key: sessionID
    debounceTimers map[string]*time.Timer   // key: sessionID
    listeners      map[*Listener]bool
    listenersMu    sync.RWMutex
    providerID     string
    db             *store.Store
}

// Listener is an SSE client connected to /api/reporter.
// sessionFilter == "" means all sessions (global voice mode).
// sessionFilter == "<id>" means only that session's narrations.
type Listener struct {
    Ch            chan Narration
    SessionFilter string // empty = all, non-empty = single session
    done          chan struct{}
}

func New(providerID string, db *store.Store) *Reporter {
    return &Reporter{
        pendingEvents:  make(map[string][]Event),
        debounceTimers: make(map[string]*time.Timer),
        listeners:      make(map[*Listener]bool),
        providerID:     providerID,
        db:             db,
    }
}

// debounceWindow returns the configured debounce duration from settings.
func (r *Reporter) debounceWindow() time.Duration {
    val, _ := r.db.GetSetting("reporter.debounce_seconds")
    if val != "" {
        if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
            return time.Duration(secs) * time.Second
        }
    }
    return 10 * time.Second // default
}

// systemPrompt returns the active system prompt based on persona setting.
func (r *Reporter) systemPrompt() string {
    personaID, _ := r.db.GetSetting("reporter.persona")
    if personaID == "" {
        personaID = "default"
    }

    if personaID == "custom" {
        customPrompt, _ := r.db.GetSetting("reporter.custom_prompt")
        if customPrompt != "" {
            return customPrompt
        }
        // Custom selected but empty — fall back to default
        personaID = "default"
    }

    if p := GetPersona(personaID); p != nil {
        return p.Prompt
    }
    return GetPersona("default").Prompt
}

// HasListeners returns true if any SSE clients are connected.
// Hook handlers should check this before calling AddEvent to avoid
// buffering events when nobody is listening.
func (r *Reporter) HasListeners() bool {
    r.listenersMu.RLock()
    defer r.listenersMu.RUnlock()
    return len(r.listeners) > 0
}

// AddEvent queues a hook event for narration.
// Events are buffered per session with a configurable debounce window.
func (r *Reporter) AddEvent(event Event) {
    if !r.HasListeners() {
        return // no connected clients, skip buffering
    }

    r.mu.Lock()
    defer r.mu.Unlock()

    sid := event.SessionID
    r.pendingEvents[sid] = append(r.pendingEvents[sid], event)

    // Reset debounce timer
    if t, ok := r.debounceTimers[sid]; ok {
        t.Stop()
    }
    r.debounceTimers[sid] = time.AfterFunc(r.debounceWindow(), func() {
        r.flush(sid)
    })
}

// hasGlobalListeners returns true if any listener has no session filter
// (i.e. global voice mode, listening to all sessions).
func (r *Reporter) hasGlobalListeners() bool {
    r.listenersMu.RLock()
    defer r.listenersMu.RUnlock()
    for l := range r.listeners {
        if l.SessionFilter == "" {
            return true
        }
    }
    return false
}

// flush collects pending events for a session, calls Haiku, and pushes
// the narration to all matching listeners.
//
// Session context (title, last user message) is only included in the prompt
// when global listeners are connected. When only session-filtered listeners
// exist, the user is already on that session's screen and knows the context.
func (r *Reporter) flush(sessionID string) {
    r.mu.Lock()
    events := r.pendingEvents[sessionID]
    delete(r.pendingEvents, sessionID)
    delete(r.debounceTimers, sessionID)
    r.mu.Unlock()

    if len(events) == 0 {
        return
    }

    // Only include session context when global listeners are connected.
    // If the user is on a specific session screen (session-filtered listener),
    // they already know which session it is — no need to waste tokens.
    var sessionCtx *SessionContext
    if r.hasGlobalListeners() {
        if sess, err := r.db.GetSession(sessionID); err == nil && sess != nil {
            sessionCtx = &SessionContext{
                CWD:             sess.CWD,
                Title:           sess.Title,
                LastUserMessage: sess.LastUserMessage,
            }
        }
    }

    // Build prompt and call Haiku
    prompt := buildPrompt(events, sessionCtx)
    if prompt == "" {
        return
    }

    system := r.systemPrompt()

    caller := provider.GetSmallModelCaller(r.providerID)
    if caller == nil {
        return
    }

    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()

    text, err := caller(ctx, system, prompt)
    if err != nil || text == "" {
        return
    }

    // Push to matching listeners
    narration := Narration{
        SessionID: sessionID,
        Text:      text,
    }

    r.listenersMu.RLock()
    for listener := range r.listeners {
        if listener.SessionFilter == "" || listener.SessionFilter == sessionID {
            select {
            case listener.Ch <- narration:
            default:
                // Client buffer full, skip
            }
        }
    }
    r.listenersMu.RUnlock()
}

// Subscribe adds a listener. Returns it so the caller can read from Ch.
func (r *Reporter) Subscribe(sessionFilter string) *Listener {
    l := &Listener{
        Ch:            make(chan Narration, 16),
        SessionFilter: sessionFilter,
        done:          make(chan struct{}),
    }
    r.listenersMu.Lock()
    r.listeners[l] = true
    r.listenersMu.Unlock()
    return l
}

// Unsubscribe removes a listener and closes its channel.
func (r *Reporter) Unsubscribe(l *Listener) {
    r.listenersMu.Lock()
    delete(r.listeners, l)
    r.listenersMu.Unlock()
    close(l.done)
}

// Clear removes all pending events (e.g. on shutdown).
func (r *Reporter) Clear() {
    r.mu.Lock()
    defer r.mu.Unlock()
    for _, t := range r.debounceTimers {
        t.Stop()
    }
    r.pendingEvents = make(map[string][]Event)
    r.debounceTimers = make(map[string]*time.Timer)
}
```

#### `prompt.go` — Prompt builder

```go
package reporter

import (
    "encoding/json"
    "fmt"
    "strings"
)

// SessionContext provides session identity for the prompt so Haiku
// can reference which session it's narrating (critical for global voice
// where multiple sessions are narrated on the same stream).
type SessionContext struct {
    CWD             string
    Title           *string
    LastUserMessage *string
}

const maxContentLen = 300

func truncate(s string, max int) string {
    if len(s) <= max {
        return s
    }
    return s[:max] + "..."
}

func buildPrompt(events []Event, ctx *SessionContext) string {
    if len(events) == 0 {
        return ""
    }

    var sb strings.Builder

    // Session identity — so Haiku knows what session this is about.
    // In global voice mode the user hears narrations from multiple sessions,
    // so the AI needs to reference the task/project naturally.
    sb.WriteString("Session:\n")
    if ctx != nil {
        if ctx.Title != nil && *ctx.Title != "" {
            sb.WriteString(fmt.Sprintf("- Title: %s\n", truncate(*ctx.Title, 200)))
        }
        if ctx.LastUserMessage != nil && *ctx.LastUserMessage != "" {
            sb.WriteString(fmt.Sprintf("- Task: %s\n", truncate(*ctx.LastUserMessage, 200)))
        }
        if ctx.CWD != "" {
            sb.WriteString(fmt.Sprintf("- Directory: %s\n", ctx.CWD))
        }
    } else if events[0].CWD != "" {
        sb.WriteString(fmt.Sprintf("- Directory: %s\n", events[0].CWD))
    }
    sb.WriteString("\n")

    sb.WriteString("Events:\n")
    for i, e := range events {
        sb.WriteString(fmt.Sprintf("%d. ", i+1))
        switch e.Type {
        case "tool_pre":
            sb.WriteString(fmt.Sprintf("[using tool] %s", e.ToolName))
            if e.ToolInput != "" {
                sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
            }
        case "tool_post":
            sb.WriteString(fmt.Sprintf("[tool done] %s", e.ToolName))
            if e.ToolInput != "" {
                sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
            }
        case "tool_post_failure":
            sb.WriteString(fmt.Sprintf("[tool failed] %s", e.ToolName))
            if e.ToolInput != "" {
                sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
            }
        case "prompt_submit":
            sb.WriteString(fmt.Sprintf("[user said] %s", truncate(e.Message, maxContentLen)))
        case "stop":
            sb.WriteString("[session completed]")
            if e.Detail != "" {
                sb.WriteString(fmt.Sprintf(" — last action: %s", truncate(e.Detail, maxContentLen)))
            }
        case "stop_failure":
            sb.WriteString("[session error]")
            if e.Detail != "" {
                sb.WriteString(fmt.Sprintf(" — %s", truncate(e.Detail, maxContentLen)))
            }
        case "permission":
            sb.WriteString(fmt.Sprintf("[permission needed] %s", e.ToolName))
            if e.ToolInput != "" {
                sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
            }
        case "question":
            sb.WriteString("[claude is asking]")
            if e.Message != "" {
                sb.WriteString(fmt.Sprintf(" %s", truncate(e.Message, maxContentLen)))
            }
        case "session_start":
            sb.WriteString(fmt.Sprintf("[session started] in %s", e.CWD))
        case "session_end":
            sb.WriteString("[session ended]")
        case "compact_pre":
            sb.WriteString("[compacting context]")
        case "compact_post":
            sb.WriteString("[context compacted]")
        case "subagent_start":
            sb.WriteString(fmt.Sprintf("[spawned subagent] %s", e.AgentType))
            if e.Detail != "" {
                sb.WriteString(fmt.Sprintf(" — %s", truncate(e.Detail, maxContentLen)))
            }
        case "subagent_stop":
            sb.WriteString("[subagent completed]")
        case "notification":
            sb.WriteString(fmt.Sprintf("[notification] %s", truncate(e.Message, maxContentLen)))
        default:
            sb.WriteString(fmt.Sprintf("[%s]", e.Type))
            if e.Message != "" {
                sb.WriteString(fmt.Sprintf(" %s", truncate(e.Message, maxContentLen)))
            }
        }
        sb.WriteString("\n")
    }

    return sb.String()
}
```

### Hook Integration

Each hook handler calls `Reporter.AddEvent()` after its existing work. The Reporter is added to `Shared` (like `SSE` and `Pusher`), then passed into `HookContext`.

#### Changes to `internal/server/server.go`

```go
type Shared struct {
    DB           *store.Store
    Mgr          *notifications.Manager
    SSE          *SSEBroadcaster
    Pusher       *push.Sender
    Tmux         *tmux.Client
    PendingPanes *PendingPaneMap
    Reporter     *reporter.Reporter  // NEW
}

func NewShared(...) *Shared {
    return &Shared{
        ...
        Reporter: reporter.New("claude", db),
    }
}
```

#### Changes to `internal/provider/registry.go`

Add Reporter to HookContext:

```go
type HookContext struct {
    DB                *store.Store
    Mgr               *notifications.Manager
    Tmux              *tmux.Client
    Notify            func(eventType string, data interface{})
    Push              func(notifType, id, title, body string)
    RemovePendingPane func(cwd string) string
    ReportEvent       func(event reporter.Event)  // NEW — nil-safe, hooks just call it
}
```

#### Changes to `internal/provider/claude/hooks.go`

Add `ctx.ReportEvent()` calls in each hook. Examples:

```go
// In handleToolPre — includes summarized tool input so AI knows WHAT is being done:
// e.g. "Read: auth/handler.go", "Bash: go test ./...", "Edit: config.go"
if ctx.ReportEvent != nil {
    ctx.ReportEvent(reporter.Event{
        Type:      "tool_pre",
        SessionID: input.SessionID,
        CWD:       input.CWD,
        ToolName:  input.ToolName,
        ToolInput: summarizeToolInput(input.ToolInput),
    })
}

// In handleToolPost — includes same summarized input for context:
if ctx.ReportEvent != nil {
    ctx.ReportEvent(reporter.Event{
        Type:      "tool_post",
        SessionID: input.SessionID,
        CWD:       input.CWD,
        ToolName:  input.ToolName,
        ToolInput: summarizeToolInput(input.ToolInput),
    })
}

// In handleToolPostFailure — includes input so AI knows what failed:
if ctx.ReportEvent != nil {
    ctx.ReportEvent(reporter.Event{
        Type:      "tool_post_failure",
        SessionID: input.SessionID,
        CWD:       input.CWD,
        ToolName:  input.ToolName,
        ToolInput: summarizeToolInput(input.ToolInput),
    })
}

// In handleStop — includes last session detail:
if ctx.ReportEvent != nil {
    ctx.ReportEvent(reporter.Event{
        Type:      "stop",
        SessionID: input.SessionID,
        CWD:       input.CWD,
        Detail:    ctx.DB.LastSessionDetail(input.SessionID),
    })
}

// In handlePromptSubmit — includes the full user message:
if ctx.ReportEvent != nil {
    ctx.ReportEvent(reporter.Event{
        Type:      "prompt_submit",
        SessionID: input.SessionID,
        CWD:       input.CWD,
        Message:   input.Message,
    })
}

// In handlePermission — includes tool name AND what it wants to do:
// e.g. ToolInput: "rm -rf build/", ToolName: "Bash"
if ctx.ReportEvent != nil {
    ctx.ReportEvent(reporter.Event{
        Type:      "permission",
        SessionID: input.SessionID,
        CWD:       input.CWD,
        ToolName:  input.ToolName,
        ToolInput: summarizeToolInput(input.ToolInput),
    })
}

// In handleQuestion — includes the question text so AI can narrate what's being asked:
if ctx.ReportEvent != nil {
    detail := "Answer required to continue"
    if len(input.ToolInput) > 0 {
        detail = summarizeToolInput(input.ToolInput)
    }
    ctx.ReportEvent(reporter.Event{
        Type:      "question",
        SessionID: input.SessionID,
        CWD:       input.CWD,
        Message:   detail,
    })
}
```

The `ReportEvent` function is wired in the server when constructing `HookContext`:

```go
hookCtx.ReportEvent = func(event reporter.Event) {
    shared.Reporter.AddEvent(event)
}
```

### Hooks → Reporter Event Mapping

| Hook | Reporter Event Type | Key Data | Example in prompt |
|------|-------------------|----------|-------------------|
| `handleToolPre` | `tool_pre` | ToolName, ToolInput (summarized) | `[using tool] Bash: go test ./...` |
| `handleToolPost` | `tool_post` | ToolName, ToolInput (summarized) | `[tool done] Edit: config.go` |
| `handleToolPostFailure` | `tool_post_failure` | ToolName, ToolInput (summarized) | `[tool failed] Bash: npm run build` |
| `handlePromptSubmit` | `prompt_submit` | Message (full user prompt) | `[user said] fix the OAuth redirect bug` |
| `handleStop` | `stop` | Detail (last session detail) | `[session completed] — last action: PostToolUse:Bash` |
| `handleStopFailure` | `stop_failure` | Detail (last session detail) | `[session error] — PostToolUse:Bash` |
| `handlePermission` | `permission` | ToolName, ToolInput (summarized) | `[permission needed] Bash: rm -rf build/` |
| `handleQuestion` | `question` | Message (question content) | `[claude is asking] Which database should I use?` |
| `handleElicitation` | — | skip (too complex for narration) | — |
| `handleSessionStart` | `session_start` | CWD | `[session started] in /Users/dev/myproject` |
| `handleSessionEnd` | `session_end` | — | `[session ended]` |
| `handlePreCompact` | `compact_pre` | — | `[compacting context]` |
| `handlePostCompact` | `compact_post` | — | `[context compacted]` |
| `handleSubagentStart` | `subagent_start` | AgentType, Detail (description) | `[spawned subagent] Explore — search for auth endpoints` |
| `handleSubagentStop` | `subagent_stop` | — | `[subagent completed]` |
| `handleNotification` | `notification` | Message (idle_prompt) | `[notification] idle_prompt` |

### SSE Endpoint: `GET /api/reporter`

Auth-protected. Query parameter `?session=<id>` filters to a single session (session voice mode). Without it, all sessions are narrated (global voice mode).

```go
func (s *PublicServer) handleReporter(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "streaming not supported", http.StatusInternalServerError)
        return
    }

    sessionFilter := r.URL.Query().Get("session")

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    listener := s.shared.Reporter.Subscribe(sessionFilter)
    defer s.shared.Reporter.Unsubscribe(listener)

    // Send initial heartbeat
    fmt.Fprintf(w, ": connected\n\n")
    flusher.Flush()

    heartbeat := time.NewTicker(30 * time.Second)
    defer heartbeat.Stop()

    for {
        select {
        case <-r.Context().Done():
            return
        case narration := <-listener.Ch:
            data, _ := json.Marshal(narration)
            fmt.Fprintf(w, "event: narration\ndata: %s\n\n", data)
            flusher.Flush()
        case <-heartbeat.C:
            fmt.Fprintf(w, ": heartbeat\n\n")
            flusher.Flush()
        }
    }
}
```

Route registration:
```go
protectedMux.HandleFunc("GET /api/reporter", s.handleReporter)
```

### Settings API

#### `GET /api/reporter/settings` — Get current settings

Returns the current reporter configuration.

```go
func (s *PublicServer) handleGetReporterSettings(w http.ResponseWriter, r *http.Request) {
    persona, _ := s.shared.DB.GetSetting("reporter.persona")
    if persona == "" {
        persona = "default"
    }
    customPrompt, _ := s.shared.DB.GetSetting("reporter.custom_prompt")
    debounce, _ := s.shared.DB.GetSetting("reporter.debounce_seconds")
    if debounce == "" {
        debounce = "10"
    }
    enabled, _ := s.shared.DB.GetSetting("reporter.enabled")
    if enabled == "" {
        enabled = "true"
    }

    jsonResponse(w, http.StatusOK, map[string]string{
        "persona":          persona,
        "custom_prompt":    customPrompt,
        "debounce_seconds": debounce,
        "enabled":          enabled,
    })
}
```

Response:
```json
{
  "persona": "butler",
  "custom_prompt": "",
  "debounce_seconds": "10",
  "enabled": "true"
}
```

#### `POST /api/reporter/settings` — Update settings

Accepts partial updates — only provided fields are changed.

```go
func (s *PublicServer) handleUpdateReporterSettings(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Persona        *string `json:"persona"`
        CustomPrompt   *string `json:"custom_prompt"`
        DebounceSeconds *int   `json:"debounce_seconds"`
        Enabled        *bool   `json:"enabled"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        jsonError(w, "invalid request body", http.StatusBadRequest)
        return
    }

    if req.Persona != nil {
        // Validate: must be a known persona ID or "custom"
        if *req.Persona != "custom" && GetPersona(*req.Persona) == nil {
            jsonError(w, "unknown persona", http.StatusBadRequest)
            return
        }
        s.shared.DB.SetSetting("reporter.persona", *req.Persona)
    }
    if req.CustomPrompt != nil {
        s.shared.DB.SetSetting("reporter.custom_prompt", *req.CustomPrompt)
    }
    if req.DebounceSeconds != nil {
        if *req.DebounceSeconds < 2 || *req.DebounceSeconds > 60 {
            jsonError(w, "debounce_seconds must be 2-60", http.StatusBadRequest)
            return
        }
        s.shared.DB.SetSetting("reporter.debounce_seconds", strconv.Itoa(*req.DebounceSeconds))
    }
    if req.Enabled != nil {
        s.shared.DB.SetSetting("reporter.enabled", strconv.FormatBool(*req.Enabled))
    }

    w.WriteHeader(http.StatusNoContent)
}
```

Request (partial — only send what you want to change):
```json
{
  "persona": "sarcastic",
  "debounce_seconds": 5
}
```

#### `GET /api/reporter/personas` — List available personas

Returns all built-in personas so mobile can display them in a picker.

```go
func (s *PublicServer) handleListPersonas(w http.ResponseWriter, r *http.Request) {
    jsonResponse(w, http.StatusOK, reporter.Personas)
}
```

Response:
```json
[
  {"id": "default",   "name": "Default",   "description": "Neutral, no-nonsense narrator",        "prompt": "..."},
  {"id": "butler",    "name": "Butler",    "description": "Formal British butler, addresses user as sir", "prompt": "..."},
  {"id": "casual",    "name": "Casual",    "description": "Friendly, relaxed tone",               "prompt": "..."},
  {"id": "genz",      "name": "Gen Z",     "description": "Internet slang, fun energy",           "prompt": "..."},
  {"id": "sarcastic", "name": "Sarcastic", "description": "Dry humor, deadpan delivery",          "prompt": "..."},
  {"id": "pirate",    "name": "Pirate",    "description": "Arr, pirate speak matey",              "prompt": "..."}
]
```

#### Routes

```go
protectedMux.HandleFunc("GET /api/reporter/settings", s.handleGetReporterSettings)
protectedMux.HandleFunc("POST /api/reporter/settings", s.handleUpdateReporterSettings)
protectedMux.HandleFunc("GET /api/reporter/personas", s.handleListPersonas)
```

### SSE Event Format

```
event: narration
data: {"session_id":"abc123","text":"Just fixed the redirect URL in the auth handler, running tests now."}
```

## Mobile

### Multi-Host Narration Queue

The user may have multiple hosts connected (e.g., work laptop + home server). Each host runs its own Reporter that pushes narrations via its own SSE stream. Since TTS can only speak one utterance at a time, narrations from all hosts must be serialized into a single FIFO queue.

`VoiceService` already has a speech queue (`_speechQueue`) with FIFO processing and overflow handling (`_maxQueueSize = 3`, drops old items and says "Skipping ahead"). All SSE streams feed into `VoiceService.speak()`, which enqueues. This means multi-host is handled naturally — narrations are spoken in the order they arrive, regardless of which host produced them.

```
Host A (Reporter SSE) ──→ narration arrives ──→ VoiceService.speak() ──→ Queue
Host B (Reporter SSE) ──→ narration arrives ──→ VoiceService.speak() ──→ Queue
                                                                           ↓
                                                                     FIFO playback
                                                                     (one at a time)
```

#### Queue behavior with multiple hosts

| Scenario | Behavior |
|----------|----------|
| Host A narration arrives while idle | Speak immediately |
| Host B narration arrives while Host A is speaking | Enqueue, speak after A finishes |
| 3+ narrations back up (from any mix of hosts) | Drop old items, say "Skipping ahead", speak latest |
| Host A disconnects | Its SSE stream closes, no more narrations from A. Queue drains remaining items |
| Global voice on Host A + session voice on Host B | Both SSE streams active, both feed the same queue |

#### NarrationService manages per-host connections

`NarrationService` tracks one SSE connection per host (for global voice) plus optional per-session connections. Each connection runs in its own isolate-like async loop, feeding narrations into the shared `VoiceService.speak()` queue.

```dart
// Active connections:
// "hostA" → global voice SSE for host A
// "hostB" → global voice SSE for host B
// "hostA:session123" → session voice SSE for a specific session on host A
```

### What changes

The mobile app becomes a simple SSE listener + TTS speaker. All narration intelligence moves to the backend.

#### `lib/services/narration_service.dart` — Simplified to SSE client

```dart
class NarrationService {
  NarrationService._();
  static final instance = NarrationService._();

  // Active SSE connections — key: hostId (global) or hostId:sessionId (session)
  final Map<String, http.Client> _activeClients = {};

  /// Connect to the reporter SSE stream for a host.
  /// [sessionId] is null for global voice mode, non-null for session voice.
  /// Multiple hosts can be connected simultaneously — narrations from all
  /// hosts feed into VoiceService's single FIFO speech queue.
  Future<void> connect(String hostId, String serverUrl, String cookie, {String? sessionId}) async {
    final key = sessionId != null ? '$hostId:$sessionId' : hostId;
    disconnect(key); // close any existing connection for this key

    final client = http.Client();
    _activeClients[key] = client;

    final queryParam = sessionId != null ? '?session=$sessionId' : '';
    final request = http.Request('GET', Uri.parse('$serverUrl/api/reporter$queryParam'));
    request.headers.addAll({
      'Cookie': 'helios_token=$cookie',
      'Accept': 'text/event-stream',
      'Cache-Control': 'no-cache',
    });

    try {
      final response = await client.send(request);
      if (response.statusCode != 200) return;

      String buffer = '';
      String currentEvent = '';

      await for (final chunk in response.stream.transform(utf8.decoder)) {
        buffer += chunk;
        final lines = buffer.split('\n');
        buffer = lines.removeLast();

        for (final line in lines) {
          if (line.startsWith('event: ')) {
            currentEvent = line.substring(7).trim();
          } else if (line.startsWith('data: ') && currentEvent == 'narration') {
            try {
              final data = jsonDecode(line.substring(6));
              final text = data['text'] as String?;
              if (text != null && text.isNotEmpty) {
                // All hosts feed into the same VoiceService queue.
                // VoiceService handles FIFO ordering and overflow.
                VoiceService.instance.speak(text);
              }
            } catch (_) {}
            currentEvent = '';
          }
        }
      }
    } catch (_) {}

    _activeClients.remove(key);
  }

  /// Disconnect a specific reporter stream by key.
  void disconnect(String key) {
    _activeClients[key]?.close();
    _activeClients.remove(key);
  }

  /// Disconnect all reporter streams for a specific host.
  /// Removes both global (hostId) and session (hostId:*) connections.
  void disconnectHost(String hostId) {
    final keysToRemove = _activeClients.keys
        .where((k) => k == hostId || k.startsWith('$hostId:'))
        .toList();
    for (final key in keysToRemove) {
      _activeClients[key]?.close();
      _activeClients.remove(key);
    }
  }

  /// Disconnect all reporter streams across all hosts.
  void disconnectAll() {
    for (final client in _activeClients.values) {
      client.close();
    }
    _activeClients.clear();
  }

  /// Check if any reporter stream is active for a host.
  bool isConnected(String hostId) {
    return _activeClients.keys.any((k) => k == hostId || k.startsWith('$hostId:'));
  }
}
```

#### `lib/screens/session_detail_screen.dart` — Session voice mode

When session voice mode is toggled on:
```dart
NarrationService.instance.connect(
  widget.session.hostId,
  _apiService.serverUrl,
  _apiService.cookie,
  sessionId: widget.session.sessionId,
);
```

When toggled off or disposed:
```dart
NarrationService.instance.disconnect('${widget.session.hostId}:${widget.session.sessionId}');
```

Remove all transcript-polling narration logic:
- Remove `NarrationEvent.fromMessage()` calls in `_loadTranscript()`
- Remove `NarrationEvent.fromNotification()` calls in SSE listener
- Remove `NarrationEvent.fromStatus()` calls in SSE listener
- Remove `_lastReadTotal` tracking for narration

#### `lib/screens/home_screen.dart` — Global voice mode

When global voice mode is toggled on for a host:
```dart
NarrationService.instance.connect(
  hostId,
  apiService.serverUrl,
  apiService.cookie,
);
```

When toggled off:
```dart
NarrationService.instance.disconnect(hostId);
```

Multiple hosts can have global voice active simultaneously — each gets its own SSE stream, all narrations feed into the shared speech queue.

Remove all global narration event construction logic.

#### `lib/services/daemon_api_service.dart` — Reporter settings methods

```dart
/// Fetch reporter settings from backend.
Future<Map<String, dynamic>?> getReporterSettings() async {
  final resp = await _authGet('/api/reporter/settings');
  if (resp.statusCode == 200) return jsonDecode(resp.body);
  return null;
}

/// Update reporter settings (partial — only send changed fields).
Future<bool> updateReporterSettings(Map<String, dynamic> settings) async {
  final resp = await _authPost('/api/reporter/settings', settings);
  return resp.statusCode == 204;
}

/// Fetch available personas.
Future<List<dynamic>?> getReporterPersonas() async {
  final resp = await _authGet('/api/reporter/personas');
  if (resp.statusCode == 200) return jsonDecode(resp.body);
  return null;
}
```

#### `lib/screens/settings_screen.dart` — Reporter settings UI

Replace the narrator prompt editor with a full reporter settings section that reads/writes from the backend.

```
REPORTER
┌──────────────────────────────────┐
│ AI Narration               [━●━] │  ← reporter.enabled
│                                  │
│ Persona                   Butler │  ← tap to open persona picker
│                                  │
│ Debounce window            10s ▾ │  ← dropdown: 2s, 5s, 10s, 15s, 30s
│                                  │
│ Custom prompt            Edit >  │  ← only visible when persona = "custom"
│ "You are a sarcastic..."        │
└──────────────────────────────────┘
```

Persona picker dialog:
```
┌──────────────────────────────────┐
│      Choose Persona              │
├──────────────────────────────────┤
│ ○ Default                        │
│   Neutral, no-nonsense narrator  │
│                                  │
│ ● Butler                         │
│   Formal British butler          │
│                                  │
│ ○ Casual                         │
│   Friendly, relaxed tone         │
│                                  │
│ ○ Gen Z                          │
│   Internet slang, fun energy     │
│                                  │
│ ○ Sarcastic                      │
│   Dry humor, deadpan delivery    │
│                                  │
│ ○ Pirate                         │
│   Arr, pirate speak matey        │
│                                  │
│ ○ Custom                         │
│   Write your own prompt          │
│                                  │
│          [ Cancel ]  [ Select ]  │
└──────────────────────────────────┘
```

Settings are fetched from backend on screen load (`GET /api/reporter/settings`), personas fetched once (`GET /api/reporter/personas`). Changes are saved immediately via `POST /api/reporter/settings`. No local SharedPreferences for reporter settings — all server-side.

### What stays the same

- `VoiceService` — STT/TTS engine, settings, **speech queue** (handles multi-host FIFO + overflow)
- Speaker button on individual messages — still calls `stripMarkdown()` + `VoiceService.speak()` directly

### What's removed from mobile

| File/Code | Reason |
|-----------|--------|
| `lib/models/narration_event.dart` | Events constructed server-side |
| `lib/services/tts_transformer.dart` | Backend does narration |
| `lib/services/narration_service.dart` (old) | Rewritten as SSE client (no debounce, no events) |
| `DaemonAPIService.narrate()` | No more POST /api/narrate |
| Transcript-diff narration in session_detail | SSE replaces polling |
| Global event construction in home_screen | SSE replaces construction |
| `_lastReadTotal` narration tracking | No more transcript diffing |
| `_pendingEvents`, debounce timers in NarrationService | Server-side debounce |
| `onNarrate` callback | Direct SSE instead |
| Local SharedPreferences for narrator prompt | Settings stored server-side in SQLite |

## Data Flow Comparison

### Before (Pull-Based)

```
Hook fires → ctx.Notify() → SSE "session_status" → mobile
                                                        ↓
                                              mobile detects change
                                                        ↓
                                              polls transcript (HTTP GET)
                                                        ↓
                                              diffs messages, constructs events
                                                        ↓
                                              2s debounce (mobile-side)
                                                        ↓
                                              POST /api/narrate (HTTP POST)
                                                        ↓
                                              backend calls Haiku CLI
                                                        ↓
                                              response → VoiceService.speak()
```

**Problems:**
- 3 network round trips (SSE notification → transcript GET → narrate POST)
- Mobile does transcript diffing (fragile, depends on total count tracking)
- Mobile constructs NarrationEvents (duplicates backend knowledge)
- Latency: SSE delay + transcript poll + 2s debounce + POST + Haiku = ~8-10s

### After (Push-Based)

```
Hook fires → Reporter.AddEvent()
                    ↓
             10s debounce
                    ↓
             backend calls Haiku CLI
                    ↓
             SSE push "narration" → mobile → VoiceService.speak()
```

**Improvements:**
- 0 network round trips from mobile (SSE is already connected)
- No transcript diffing
- No event construction on mobile
- Latency: 10s debounce + Haiku (~2.5s) = ~12.5s (configurable — 2s debounce gives ~4.5s)
- Mobile code is dramatically simpler

## Connection Semantics

| Action | Effect |
|--------|--------|
| Connect `GET /api/reporter` (no session param) | Global voice: receive narrations for ALL sessions on that host |
| Connect `GET /api/reporter?session=abc123` | Session voice: receive narrations for session abc123 only |
| Disconnect (close SSE) | Stop narrating for that stream. Reporter stops buffering if no listeners remain |
| Toggle voice off in UI | Mobile closes SSE connection for that host/session |
| Toggle voice on in UI | Mobile opens SSE connection for that host/session |
| App goes to background | SSE disconnects naturally. Reconnect on foreground if voice was active |
| Multiple hosts with voice on | One SSE connection per host, all narrations feed into shared speech queue |
| Host A narration while Host B speaking | Queued — spoken after Host B's utterance finishes |
| Switch active host in UI | Previous host's SSE stays connected (if voice was on). New host gets its own SSE |

The SSE connection IS the toggle. No separate enable/disable API needed. Each host manages its own Reporter independently — the device's speech queue handles serialization.

## Efficiency

The `HasListeners()` check in `AddEvent()` is critical: when no mobile clients are connected for narration, hooks don't buffer events and Haiku is never called. Zero overhead when nobody is listening.

```go
func (r *Reporter) AddEvent(event Event) {
    if !r.HasListeners() {
        return  // fast path: nobody listening
    }
    // ... buffer event, set debounce timer
}
```

## Error Handling

| Failure | Behavior |
|---------|----------|
| Haiku CLI not installed | `caller` returns error → skip narration |
| CLI timeout (>15s) | Context cancelled → skip |
| CLI error (auth, network) | Skip narration silently |
| SSE client disconnects mid-flush | `select default` skips full channel |
| No provider registered | `caller == nil` → skip |
| Mobile loses SSE connection | Reconnect on app foreground if voice mode active |

Narration remains fire-and-forget. Failures are silent. The user sees everything on screen.

## Migration from `/api/narrate`

### Remove

- `POST /api/narrate` route from `server.go`
- `handleSmallModelText` handler from `api.go`
- `internal/narration/` package (replaced by `internal/reporter/`)

### Keep

- `SmallModelCaller` in `provider/registry.go` (used by Reporter)
- Claude CLI caller in `provider/claude/register.go` (used by Reporter)

### Add

- `settings` table in SQLite (migration)
- `internal/store/settings.go` — GetSetting/SetSetting/DeleteSetting
- `internal/reporter/` package (reporter.go, prompt.go, personas.go)
- `GET /api/reporter` SSE endpoint
- `GET /api/reporter/settings` + `POST /api/reporter/settings`
- `GET /api/reporter/personas`
- `ReportEvent` field on `HookContext`
- `Reporter` field on `Shared`
- Reporter.AddEvent() calls in each hook handler

## Implementation Order

### Backend (Go)
1. `internal/store/store.go` — add `settings` table migration
2. `internal/store/settings.go` — GetSetting/SetSetting/DeleteSetting
3. `internal/reporter/personas.go` — persona definitions
4. `internal/reporter/prompt.go` — prompt builder
5. `internal/reporter/reporter.go` — core Reporter service
6. `internal/server/server.go` — add Reporter to Shared, register routes
7. `internal/server/api.go` — add `handleReporter` SSE endpoint + settings/personas handlers
8. `internal/provider/registry.go` — add `ReportEvent` to HookContext
9. `internal/provider/claude/hooks.go` — add `ctx.ReportEvent()` calls in each hook
10. Remove `POST /api/narrate` route and `internal/narration/` package

### Mobile (Dart)
11. `lib/services/narration_service.dart` — rewrite as SSE client
12. `lib/services/daemon_api_service.dart` — add reporter settings/personas methods, remove `narrate()`
13. `lib/screens/session_detail_screen.dart` — connect/disconnect on voice toggle
14. `lib/screens/home_screen.dart` — connect/disconnect on global voice toggle
15. `lib/screens/settings_screen.dart` — reporter settings UI with persona picker
16. Remove `lib/models/narration_event.dart`
17. Remove `tts_transformer.dart` if still present

## Key Design Decisions

- **Push, not pull** — backend already has all the data from hooks. No reason for mobile to reconstruct it.
- **SSE connection = toggle** — connecting starts narration, disconnecting stops it. No separate API to manage state.
- **`HasListeners()` guard** — zero overhead when nobody is listening. No Haiku calls, no buffering.
- **Session filter on SSE** — `?session=<id>` for session voice, no param for global voice. One endpoint, two modes.
- **Server-side debounce** — 10s default window (configurable) batches hooks into one narration. Longer window = better summaries, fewer Haiku calls.
- **Reporter on Shared** — singleton, same lifecycle as SSE broadcaster. Created once at startup.
- **`ReportEvent` on HookContext** — nil-safe function pointer. Hooks call it unconditionally; it's wired to `Reporter.AddEvent()` by the server.
- **15s timeout** — generous for CLI startup + inference since there's no network hop to account for.
- **Personas as constants, selection in DB** — persona prompts are hardcoded in `personas.go`. The DB only stores which one is active (`reporter.persona` setting). Adding a persona = adding one entry to the `Personas` slice. No schema changes needed.
- **Custom persona** — when `persona = "custom"`, the prompt comes from `reporter.custom_prompt` setting. Users can write anything. Falls back to default if custom prompt is empty.
- **Settings in SQLite** — reporter config (persona, debounce, enabled, custom prompt) stored in a generic `settings` key-value table. No new columns on existing tables. Settings API lets mobile read/write them.
- **Configurable debounce** — default 10s, range 2-60s. Longer = better summaries + fewer Haiku calls. Shorter = more responsive narration. User's choice.
- **No local settings for reporter** — all reporter config is server-side. Mobile fetches settings from backend, displays them, saves via POST. No SharedPreferences for persona/prompt/debounce.
- **Session context only for global voice** — `flush()` checks `hasGlobalListeners()`. If only session-filtered listeners exist (user is on the session screen), session context is omitted — the user already knows which session it is. If global listeners are connected, title + last user message + CWD are included so Haiku can reference the task naturally (e.g. "On the OAuth fix, just edited the auth handler"). Saves tokens and avoids redundant narration.
- **Rich event data** — tool input is summarized (`summarizeToolInput`) and passed as `ToolInput` string. Questions include the question text. Permissions include what tool wants to do and on what. The AI has enough context to produce meaningful narration, not just "using tool Read".
- **Elicitation skipped** — too complex for a spoken sentence. Permission and question hooks are narrated.
- **Multi-host via shared speech queue** — each host gets its own SSE stream; all narrations feed into `VoiceService`'s single FIFO queue. No coordination needed between hosts — the queue serializes playback naturally. Overflow (3+ backed up) drops old items and skips ahead.
- **Per-host connection management** — `NarrationService` tracks connections by key (`hostId` for global, `hostId:sessionId` for session). `disconnectHost()` tears down all streams for a host. Multiple hosts can narrate simultaneously.
