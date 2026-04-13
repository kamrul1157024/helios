# AI Narration for Voice Mode

## Context

Voice mode currently uses hardcoded sentence graphs (templates) to narrate tool calls, notifications, and session events. While functional, templates sound repetitive and can't summarize assistant responses — they either read the full markdown-stripped text verbatim (tedious for long responses) or skip it entirely.

This spec replaces template-based narration with AI-generated narration using the provider's CLI. The provider interface exposes a `CallSmallModel(system, prompt string) (string, error)` method. For the Claude provider, this runs `claude -p --bare --model haiku --tools "" --no-session-persistence --output-format json` as a subprocess. Each provider implements this method using its own CLI or tooling.

Templates are removed entirely. The speaker button on individual messages still reads the full original text directly via TTS (no AI call) for users who want verbatim playback.

## Architecture

```
Events arrive (tool_use, assistant, notification, status change)
       ↓
Mobile: 2s debounce window — collect events
       ↓
POST /api/narrate  { events[], session_context, system_prompt? }
       ↓
Backend: provider.CallSmallModel(system, prompt)
       ↓
Provider impl: claude -p --bare --model haiku --tools "" --no-session-persistence --output-format json
       ↓
Response: { "narration": "Found the bug in auth — fixed the redirect, running tests now." }
       ↓
VoiceService.speak(narration)
       ↓
flutter_tts engine
```

## Provider Interface

Each provider registers a `SmallModelCaller` — a function that takes a system prompt and user prompt, runs the provider's CLI with its cheapest/fastest model, and returns the text response.

```go
// In provider/registry.go

// SmallModelCaller runs a provider's cheapest model for short text generation tasks.
// Used for narration, summarization, and other lightweight AI calls.
// Implementations should use the provider's CLI (not direct API calls) to respect
// the user's existing auth setup (OAuth, API keys, managed accounts, etc.).
type SmallModelCaller func(ctx context.Context, system, prompt string) (string, error)

var smallModelCallers = map[string]SmallModelCaller{}

func RegisterSmallModelCaller(providerID string, caller SmallModelCaller)
func GetSmallModelCaller(providerID string) SmallModelCaller
```

### Claude provider implementation

```go
// In provider/claude/register.go

provider.RegisterSmallModelCaller("claude", func(ctx context.Context, system, prompt string) (string, error) {
    ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx, "claude",
        "-p",
        "--bare",
        "--model", "haiku",
        "--tools", "",
        "--no-session-persistence",
        "--output-format", "json",
        "--system-prompt", system,
    )
    cmd.Stdin = strings.NewReader(prompt)

    output, err := cmd.Output()
    if err != nil {
        return "", fmt.Errorf("claude cli: %w", err)
    }

    // Parse JSON response: {"type":"result","result":"..."}
    var result struct {
        Result string `json:"result"`
    }
    if err := json.Unmarshal(output, &result); err != nil {
        return "", fmt.Errorf("parse response: %w", err)
    }

    return result.Result, nil
})
```

### Benchmarks (tested)

| Scenario | Time | Notes |
|----------|------|-------|
| Cold start (first call) | ~4.5s | Node.js + CLI startup + Haiku inference |
| Subsequent calls | ~2.5-3s | CLI startup + Haiku inference |
| With `--bare --tools ""` | ~2.5s | Skips hooks, LSP, plugins, CLAUDE.md |
| Token usage per call | ~136 in / ~21 out | $0.00024 per call |

The 2s debounce absorbs most of the latency. Total time from first event to speech: ~5s.

### Why CLI, not API

- Users may not have `ANTHROPIC_API_KEY` (OAuth, managed accounts, SSO)
- Claude CLI handles its own auth — we don't need to know how
- Provider-agnostic — each provider implements `CallSmallModel` using its own CLI
- No new dependencies (no Anthropic SDK in Go)
- `--bare` flag makes it fast enough (~2.5s)

### Future providers

Other providers implement the same interface with their own CLI:

```go
// Hypothetical OpenAI provider
provider.RegisterSmallModelCaller("openai", func(ctx context.Context, system, prompt string) (string, error) {
    // Use openai CLI or curl with OPENAI_API_KEY
})

// Hypothetical local model provider
provider.RegisterSmallModelCaller("ollama", func(ctx context.Context, system, prompt string) (string, error) {
    // Use ollama CLI
})
```

## Backend

### New file: `internal/narration/narrate.go`

Stateless narration service. Builds the prompt from events and calls the provider.

```go
package narration

import (
    "context"
    "fmt"
    "strings"

    "github.com/kamrul1157024/helios/internal/provider"
)

type Event struct {
    Type    string `json:"type"`              // "tool_use", "tool_result", "assistant", "notification", "status"
    Tool    string `json:"tool,omitempty"`
    Target  string `json:"target,omitempty"`
    Summary string `json:"summary,omitempty"`
    Content string `json:"content,omitempty"` // assistant response text (truncated to 500 chars by mobile)
    Success *bool  `json:"success,omitempty"` // tool_result
    Status  string `json:"status,omitempty"`  // "idle", "error" for status events
}

type NarrateRequest struct {
    Events         []Event `json:"events"`
    SessionContext string  `json:"session_context,omitempty"` // user's prompt / session title
    SystemPrompt   string  `json:"system_prompt,omitempty"`   // custom narrator prompt override
}

type NarrateResponse struct {
    Narration string `json:"narration"`
}

const DefaultSystemPrompt = `You are a voice narrator for a coding AI assistant. You speak as the AI in first person, narrating what you're doing for a user who is listening, not reading.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize the batch naturally into 1-2 sentences
- Be casual and natural — like you're talking to a coworker
- For assistant responses, extract the key point — don't repeat the whole thing
- For tool calls, mention what you're doing and the file/command name
- For errors, be brief — the user will check the screen
- For session completion (status=idle), let them know you're done
- Never use markdown, code formatting, quotes, or asterisks
- Never start with "I" every time — vary your sentence openers`

func Narrate(ctx context.Context, req NarrateRequest, providerID string) (*NarrateResponse, error) {
    caller := provider.GetSmallModelCaller(providerID)
    if caller == nil {
        return &NarrateResponse{}, nil
    }

    system := DefaultSystemPrompt
    if req.SystemPrompt != "" {
        system = req.SystemPrompt
    }

    prompt := buildPrompt(req)
    if prompt == "" {
        return &NarrateResponse{}, nil
    }

    result, err := caller(ctx, system, prompt)
    if err != nil {
        return &NarrateResponse{}, nil // silent failure
    }

    return &NarrateResponse{Narration: result}, nil
}

func buildPrompt(req NarrateRequest) string {
    var sb strings.Builder

    if req.SessionContext != "" {
        sb.WriteString(fmt.Sprintf("Session context: %s\n\n", req.SessionContext))
    }

    sb.WriteString("Events:\n")
    for i, e := range req.Events {
        sb.WriteString(fmt.Sprintf("%d. ", i+1))
        switch e.Type {
        case "assistant":
            sb.WriteString(fmt.Sprintf("[assistant] %s", e.Content))
        case "tool_use":
            sb.WriteString(fmt.Sprintf("[tool_use] %s", e.Tool))
            if e.Target != "" {
                sb.WriteString(fmt.Sprintf(" %s", e.Target))
            }
            if e.Summary != "" {
                sb.WriteString(fmt.Sprintf(" — %s", e.Summary))
            }
        case "tool_result":
            sb.WriteString(fmt.Sprintf("[tool_result] %s", e.Tool))
            if e.Success != nil {
                if *e.Success {
                    sb.WriteString(" — success")
                } else {
                    sb.WriteString(" — failed")
                }
            }
        case "notification":
            sb.WriteString(fmt.Sprintf("[notification] %s", e.Tool))
            if e.Summary != "" {
                sb.WriteString(fmt.Sprintf(" — %s", e.Summary))
            }
        case "status":
            sb.WriteString(fmt.Sprintf("[status] %s", e.Status))
        }
        sb.WriteString("\n")
    }

    return sb.String()
}
```

### New endpoint: `POST /api/narrate`

Added to `PublicServer` (auth-protected).

```go
func (s *PublicServer) handleNarrate(w http.ResponseWriter, r *http.Request) {
    var req narration.NarrateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        jsonError(w, "invalid request body", http.StatusBadRequest)
        return
    }

    if len(req.Events) == 0 {
        jsonResponse(w, http.StatusOK, narration.NarrateResponse{})
        return
    }

    // Use default provider
    resp, _ := narration.Narrate(r.Context(), req, "claude")

    jsonResponse(w, http.StatusOK, resp)
}
```

Route registration in `server.go`:
```go
protectedMux.HandleFunc("POST /api/narrate", s.handleNarrate)
```

### Request shape

```json
{
  "events": [
    {
      "type": "tool_use",
      "tool": "Read",
      "target": "auth handler",
      "summary": "reading auth/handler.go"
    },
    {
      "type": "tool_result",
      "tool": "Read",
      "success": true
    },
    {
      "type": "assistant",
      "content": "I found the bug. The redirect URL was missing the trailing slash which caused the OAuth callback to fail. The fix is straightforward — I need to append a slash to the redirect URL in the validateCallback function. Let me make that change now."
    },
    {
      "type": "tool_use",
      "tool": "Edit",
      "target": "auth handler",
      "summary": "fix redirect URL"
    }
  ],
  "session_context": "fix the OAuth redirect bug",
  "system_prompt": "You are a sarcastic British butler narrating code changes."
}
```

### Response shape

```json
{
  "narration": "Found the redirect bug — missing trailing slash. Just fixed it in the auth handler."
}
```

### Example narrations (from live testing with Haiku)

| Input | Narration |
|-------|-----------|
| read auth_handler.go, found bug in token validation | "I just spotted a bug lurking in that token validation logic." |
| editing config.go to fix database connection string | "I'm tweaking the database connection string over in config.go right now." |
| running go test, all 47 tests passed | "All forty-seven tests just passed, looking pretty solid." |
| searched 12 files, found endpoint in routes.go | "Found that API endpoint hiding in routes.go after digging through a dozen files." |
| fixed OAuth redirect, edited 3 files, tests pass | "I wrapped up that OAuth redirect bug, touched three files and tests are all green." |
| deployed version 2.3, no errors | "I just shipped version 2.3 live and everything's running smooth." |

### Custom system prompt examples

**Sarcastic:**
```json
{"system_prompt": "You are a sarcastic narrator. Be dry and deadpan."}
→ "Oh wonderful, editing the auth handler again. What could possibly go wrong."
```

**Butler:**
```json
{"system_prompt": "You are a formal British butler. Address the user as sir."}
→ "Sir, I have taken the liberty of reviewing the auth handler for any irregularities."
```

**Pirate:**
```json
{"system_prompt": "You are a pirate. Use pirate speak."}
→ "Arrr, found a nasty bug in the token validation, matey!"
```

## Mobile

### New file: `lib/services/narration_service.dart`

```dart
class NarrationService {
  NarrationService._();
  static final instance = NarrationService._();

  // Persisted settings
  bool _aiNarrationEnabled = true;
  String _customPrompt = '';

  // Debounce state per session
  final Map<String, List<NarrationEvent>> _pendingEvents = {};
  final Map<String, Timer?> _debounceTimers = {};

  bool get aiNarrationEnabled => _aiNarrationEnabled;
  String get customPrompt => _customPrompt;

  Future<void> init() async { /* load from SharedPreferences */ }

  /// Queue an event for narration. Events are batched per session with 2s debounce.
  void addEvent(String hostId, String sessionId, NarrationEvent event) {
    final key = '$hostId:$sessionId';
    _pendingEvents.putIfAbsent(key, () => []);
    _pendingEvents[key]!.add(event);

    _debounceTimers[key]?.cancel();
    _debounceTimers[key] = Timer(const Duration(seconds: 2), () {
      _flush(hostId, sessionId);
    });
  }

  /// Flush pending events: call /api/narrate, speak result.
  Future<void> _flush(String hostId, String sessionId) async {
    final key = '$hostId:$sessionId';
    final events = _pendingEvents.remove(key);
    _debounceTimers.remove(key);
    if (events == null || events.isEmpty) return;

    final session = /* look up session for context */;
    final narration = await _callNarrate(hostId, events, session?.displayTitle);
    if (narration != null && narration.isNotEmpty) {
      VoiceService.instance.speak(narration);
    }
  }

  Future<String?> _callNarrate(String hostId, List<NarrationEvent> events, String? context) async {
    final service = /* get DaemonAPIService for hostId */;
    // POST /api/narrate
    // body: { events, session_context, system_prompt }
    // timeout: 10s (CLI startup + inference)
    // on error: return null (skip narration silently)
  }

  /// Clear all pending events (e.g., when voice mode is turned off).
  void clear() {
    for (final timer in _debounceTimers.values) {
      timer?.cancel();
    }
    _debounceTimers.clear();
    _pendingEvents.clear();
  }
}
```

### New model: `lib/models/narration_event.dart`

```dart
class NarrationEvent {
  final String type;      // "tool_use", "tool_result", "assistant", "notification", "status"
  final String? tool;
  final String? target;
  final String? summary;
  final String? content;  // assistant text (pre-truncated to 500 chars)
  final bool? success;
  final String? status;

  const NarrationEvent({
    required this.type,
    this.tool,
    this.target,
    this.summary,
    this.content,
    this.success,
    this.status,
  });

  Map<String, dynamic> toJson() => {
    'type': type,
    if (tool != null) 'tool': tool,
    if (target != null) 'target': target,
    if (summary != null) 'summary': summary,
    if (content != null) 'content': content,
    if (success != null) 'success': success,
    if (status != null) 'status': status,
  };

  /// Build from a transcript Message.
  factory NarrationEvent.fromMessage(Message msg) {
    switch (msg.role) {
      case 'assistant':
        final text = msg.content ?? '';
        return NarrationEvent(
          type: 'assistant',
          content: text.length > 500 ? text.substring(0, 500) : text,
        );
      case 'tool_use':
        return NarrationEvent(
          type: 'tool_use',
          tool: msg.tool,
          target: _shorten(msg.tool, msg.summary),
          summary: msg.summary,
        );
      case 'tool_result':
        return NarrationEvent(
          type: 'tool_result',
          tool: msg.tool,
          success: msg.success,
        );
      default:
        return NarrationEvent(type: msg.role);
    }
  }

  /// Build from a session status change.
  factory NarrationEvent.fromStatus(String status) =>
    NarrationEvent(type: 'status', status: status);

  /// Build from a notification.
  factory NarrationEvent.fromNotification(HeliosNotification n) =>
    NarrationEvent(
      type: 'notification',
      tool: n.toolName,
      summary: n.detail,
    );

  static String? _shorten(String? tool, String? raw) {
    if (raw == null) return null;
    switch (tool) {
      case 'Bash': return shortenCommand(raw);
      case 'Read':
      case 'Write':
      case 'Edit': return shortenFilePath(raw);
      default: return raw;
    }
  }
}
```

### Changes to `DaemonAPIService`

Add narrate method:

```dart
Future<String?> narrate(List<NarrationEvent> events, {String? sessionContext, String? systemPrompt}) async {
  // POST /api/narrate
  // body: { events: [...], session_context: ..., system_prompt: ... }
  // timeout: 10s (CLI needs ~3-5s)
  // returns narration string or null on failure
}
```

### Changes to `session_detail_screen.dart`

In `_loadTranscript()` auto-read section, replace direct `TTSTransformer.transformMessage()` + `VoiceService.speak()` with:

```dart
if (_isVoiceActive && VoiceService.instance.autoReadEnabled && _lastReadTotal > 0) {
  final newCount = result.total - _lastReadTotal;
  if (newCount > 0) {
    final startIdx = result.messages.length - newCount;
    if (startIdx >= 0) {
      for (var i = startIdx; i < result.messages.length; i++) {
        if (result.messages[i].role == 'user') continue;
        NarrationService.instance.addEvent(
          widget.session.hostId,
          widget.session.sessionId,
          NarrationEvent.fromMessage(result.messages[i]),
        );
      }
    }
  }
}
```

Same for SSE session_status events:
```dart
if (_isVoiceActive && (status == 'idle' || status == 'error')) {
  NarrationService.instance.addEvent(
    widget.session.hostId,
    widget.session.sessionId,
    NarrationEvent.fromStatus(status),
  );
}
```

And SSE notification events:
```dart
if (_isVoiceActive && event.type == 'notification') {
  NarrationService.instance.addEvent(
    widget.session.hostId,
    widget.session.sessionId,
    NarrationEvent.fromNotification(n),
  );
}
```

### Changes to `home_screen.dart`

Global voice mode uses the same `NarrationService.addEvent()` path. The `_speakGlobalNotification()` and session status handler feed events into narration service instead of calling `TTSTransformer` directly.

The notification batching timer (`_batchSpeakTimer`, `_pendingActionCount`) is removed — the 2s debounce in `NarrationService` handles batching naturally.

### Changes to `settings_screen.dart`

Replace persona picker with narrator prompt:

```
VOICE
┌──────────────────────────────────┐
│ Voice input              [━━●━━] │
│ Auto-read responses      [━━●━━] │
│ Read tool actions        [━━●━━] │
│                                  │
│ Speech rate                      │
│ ○─────────●──────────○           │
│ Slow              Fast           │
│                                  │
│ Narrator prompt            Edit >│  ← tap to open text editor dialog
│ "You are a sarcastic..."        │
└──────────────────────────────────┘
```

Narrator prompt dialog:
```
┌──────────────────────────────────┐
│      Narrator Prompt             │
├──────────────────────────────────┤
│ Customize how the AI narrator    │
│ speaks. Leave empty for default. │
│                                  │
│ ┌──────────────────────────────┐ │
│ │ You are a sarcastic British  │ │
│ │ butler narrating code        │ │
│ │ changes. Be dry and deadpan. │ │
│ │                              │ │
│ └──────────────────────────────┘ │
│                                  │
│   [ Reset ]         [ Save ]     │
└──────────────────────────────────┘
```

### Files removed

- `lib/models/tts_persona.dart` — sentence graphs, `Segment`, `SentenceGraph` removed
- `lib/services/tts_transformer.dart` — all template-based transformation removed

### Files kept (moved)

- `shortenFilePath()` and `shortenCommand()` → move to `lib/models/narration_event.dart` (still useful for pre-processing targets before sending to the model)
- `stripMarkdown()` in `lib/utils/markdown_stripper.dart` — still used by the speaker button for verbatim playback

### Speaker button (unchanged)

The speaker button on individual messages still calls `stripMarkdown()` + `VoiceService.speak()` directly — no AI narration. This is the "read me exactly what Claude said" path. Fast, free, verbatim.

## Debounce Flow

```
t=0.0s  tool_use (Read auth.go)       → addEvent → start 2s timer
t=0.3s  tool_result (success)         → addEvent → reset 2s timer
t=0.8s  assistant (200 word response)  → addEvent → reset 2s timer
t=1.2s  tool_use (Edit auth.go)       → addEvent → reset 2s timer
t=1.5s  tool_result (success)         → addEvent → reset 2s timer
t=3.5s  timer fires → POST /api/narrate (5 events)
t=3.5s  backend: claude -p --bare --model haiku ... (subprocess)
t=6.0s  response: "Checked auth handler, found the issue, and fixed it."
t=6.0s  VoiceService.speak(narration)
```

If events keep arriving, the debounce keeps resetting. Once there's a 2s gap (typical between tool batches), it flushes. Total latency from first event to speech: ~6s.

## Error Handling

| Failure                        | Behavior                                   |
|-------------------------------|--------------------------------------------|
| `claude` CLI not installed     | `CallSmallModel` returns error → empty     |
| CLI timeout (>10s)            | Context cancelled → empty narration        |
| CLI error (auth, network)     | Return empty narration, skip TTS           |
| Network error (mobile→daemon) | Catch, skip silently                       |
| Empty events array            | Return empty narration immediately         |
| No provider registered        | Return empty narration                     |

Narration is fire-and-forget. Failures are silent — the user sees everything on screen anyway. No error dialogs, no retries, no snackbars.

## Cost Estimate

Measured from live testing: **~136 input tokens, ~21 output tokens per call = $0.00024.**

Assumptions: active coding session, ~100 narration calls/hour (every 2s debounce window during active work).

| Metric | Value |
|--------|-------|
| Cost per call | $0.00024 |
| Cost per hour | $0.024 |
| Cost per 8hr day | $0.19 |
| Cost per month (20 days) | $3.84 |

## Implementation Order

### Backend (Go)
1. `internal/provider/registry.go` — add `SmallModelCaller` type + registry functions
2. `internal/provider/claude/register.go` — register Claude `SmallModelCaller` (CLI subprocess)
3. `internal/narration/narrate.go` — narration logic, prompt builder, `Narrate()` function
4. `internal/server/api.go` — add `handleNarrate` endpoint
5. `internal/server/server.go` — register `POST /api/narrate` route

### Mobile (Dart)
6. `lib/models/narration_event.dart` — event model with factory constructors + `shortenFilePath`/`shortenCommand`
7. `lib/services/narration_service.dart` — singleton, debounce + flush + API call
8. `lib/services/daemon_api_service.dart` — add `narrate()` HTTP method
9. `lib/screens/session_detail_screen.dart` — replace TTSTransformer calls with NarrationService
10. `lib/screens/home_screen.dart` — replace global notification speech with NarrationService
11. `lib/screens/settings_screen.dart` — narrator prompt editor
12. Remove `lib/models/tts_persona.dart` and `lib/services/tts_transformer.dart`

## Key Design Decisions

- **CLI subprocess, not API** — respects user's existing auth (OAuth, API keys, SSO, managed accounts). No new credentials needed.
- **Provider interface** — `CallSmallModel(ctx, system, prompt)` is provider-agnostic. Claude uses its CLI, others can use theirs.
- **`--bare --tools "" --no-session-persistence`** — strips CLI to minimum. No hooks, no file access, no session saved. ~2.5s response time.
- **`--output-format json`** — deterministic structured output. Parse `result` field, no terminal scraping.
- **Templates removed entirely** — AI narration is the only path. Simpler codebase, better output.
- **2s debounce** — batches rapid-fire events into one natural sentence. Long enough to catch related events, short enough to feel responsive.
- **Custom narrator prompt** — users can make the narrator sarcastic, formal, pirate-themed, whatever. Default is casual first-person.
- **Truncate assistant content to 500 chars** — keeps input tokens small. The model only needs the gist.
- **10s timeout** — generous enough for CLI startup + inference. Context cancellation handles cleanup.
- **Speaker button stays verbatim** — no AI call for on-demand playback. Fast, free, exact.
- **Silent failures** — narration is a nice-to-have overlay. Never block, never error, never retry.
