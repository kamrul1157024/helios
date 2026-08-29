# The Provider Interface

## Status

This spec supersedes [18-provider-interface.md](18-provider-interface.md).

Spec 18 describes an interface Helios never built. It is a design from before
the daemon existed. Read this one instead.

| Spec 18 promised | What exists |
|---|---|
| A Go `Provider` interface with methods | Seven global maps and seven register functions |
| Providers configured in `config.yaml` | Providers compiled in; no config |
| Pane scraping for agents without hooks | Never built |
| A `Version` field per provider | Never built |
| `is waiting for input?` polling | Hooks only; status is pushed, never polled |
| Per-provider `stop method` | One path: `Backend.Kill` |

Spec 18 got one thing right, and this spec keeps it: *"resume_id —
provider-specific, opaque to daemon."* Part 3 builds it.

This spec defines the interface. [46-codex-provider.md](46-codex-provider.md)
is the first consumer of the new parts.

---

## Why it is not a Go interface

Helios has one `Register()` per provider package, not one `Provider` interface.
Keep it that way. Three reasons.

**The surfaces have different lifetimes.** A session builder runs on the create
request. A hook handler runs on the agent's schedule. A discovery scan runs at
daemon start. An interface would bundle three clocks into one type.

**Most surfaces are optional.** A provider that cannot resume, has no
transcript on disk and offers no slash commands is still useful. It appears in
the list, it starts, and its status is live. A fat interface would force it to
stub nine methods that return "not supported".

**It matches the house rule.** `CLAUDE.md` says to define interfaces at the
consumer site and keep them to one or two methods. The registry does exactly
that: each entry is a single function type, named for the one thing the daemon
wants.

The cost is that the compiler cannot tell a provider it forgot something. Part
6 answers that with a conformance test instead.

---

## Part 1 — the interface as built

Everything is in `internal/provider/registry.go`.
`internal/provider/claude/register.go:180` is the only caller today.

### The seven surfaces

| Register call | Function type | Required |
|---|---|---|
| `RegisterProvider(ProviderInfo, SessionBuilder, ModelsFetcher)` | — | yes |
| `RegisterHook(hookType string, HookHandler)` | `func(*HookContext, http.ResponseWriter, *http.Request, json.RawMessage)` | no |
| `RegisterAction(notifType string, ActionHandler)` | `func(*store.Notification, json.RawMessage) (notifications.Decision, error)` | no |
| `RegisterSmallModelCaller(id string, SmallModelCaller)` | `func(context.Context, system, prompt string) (string, error)` | no |
| `RegisterEventTypes(id string, []EventTypeInfo)` | — | no |
| `RegisterCommands([]Command)` | — | no |

Note that `RegisterCommands` takes no provider id. It appends to one global
list. That is a bug the moment a second provider registers. Part 3 fixes it.

### Identity and launch

```go
type ProviderInfo struct {
    ID              string
    Name            string
    Icon            string
    Capabilities    ProviderCapabilities
    PermissionModes []string   // most restrictive first, or nil
}

type SessionBuilder func(SessionSpec) []string
type ModelsFetcher  func() ([]ModelInfo, error)
```

`SessionSpec` carries `SessionID`, `Prompt`, `Model`, `CWD`, `PermissionMode`
and `SkipPermissions`.

**The builder returns argv, not a command line.** The terminal executes it
directly. A prompt full of quotes and backticks reaches the agent as typed. A
provider must never join the argv into a string.

`PermissionModes` is served to the clients rather than hardcoded in them. The
vocabulary belongs to the CLI, and it changes between releases.

### The hook contract

One route serves every provider. `internal/server/hooks.go:18`:

```
POST /hooks/{a}/{b}/{c}  →  handler key "a.b.c"
```

The path segments join with dots. The body passes through as
`json.RawMessage`, unparsed. The provider owns the payload shape, so a provider
whose CLI sends a different schema needs no daemon change.

A handler receives a `HookContext`:

| Field | Type | Use |
|---|---|---|
| `DB` | `*store.Store` | session and notification rows |
| `Mgr` | `*notifications.Manager` | create a notification, wait for the answer |
| `Terminal` | `backend.Backend` | rename, kill, send keys |
| `HITL` | `*hitl.Controller` | paint a prompt over the session's terminal |
| `Notify` | `func(string, interface{})` | broadcast an SSE event |
| `SessionStarted` | `func(string)` | the agent reported in; stop the trust watcher |
| `PromptSubmitted` | `func(string)` | a typed prompt landed |
| `Report` | `func(ReportEvent)` | push a narration event |

`HITL` is nil where no terminal can be painted. `Report` may be nil. A handler
must check both. `internal/provider/claude/hooks.go:150` and `:249` show the
guard.

**Every handler must write a response.** A hook that returns nothing hangs the
agent until its own timeout. The non-blocking handlers all end with
`fmt.Fprint(w, "{}")`.

### The blocking-hook contract

A hook that waits for a human does four things, in this order. The order is not
cosmetic.

1. `Mgr.CreateNotification(notif)` — the row exists.
2. `Mgr.Register(notifID)` — reserve the answer slot. **Before** publishing, so
   a client that answers instantly cannot beat the handler to `WaitForDecision`.
3. `defer showPrompt(...)()` — paint the terminal overlay, and take it down
   whichever surface answers.
4. `waitForDecision(ctx, notifID, r)` — block.

The first surface to answer wins. `Mgr.Resolve` is the single place that
decides, so the phone and the terminal race from the same line.

`waitForDecision` returns nil when the agent hangs up. A nil means write
nothing and return: nobody is listening.

### The timeout contract

One number, two names.

| Constant | Value | Who reads it |
|---|---|---|
| `decisionTimeout` | 5 min | the daemon's own wait |
| `HookTimeoutSeconds` | 5 min 30 s | given to the CLI as the hook timeout |

**Helios must give up first.** If the CLI times out first, it abandons a prompt
that is still on screen, and the two race to decide what happened. Any provider
that installs hooks must apply the same margin.

### The action contract

```go
type ActionHandler func(*store.Notification, json.RawMessage) (notifications.Decision, error)
```

An action turns a client's answer into a `Decision{Status, Response}`. The
statuses are `approved`, `denied`, `answered`, `dismissed` and `timeout`.
`Response` is opaque: the blocking hook that created the notification is the
only thing that reads it.

The registry keys actions by **notification type**, not by provider. So the type
string is the contract between the hook and the action. Name it
`<provider>.<kind>` and keep the two in step.

An action must not assume a live terminal. `handleErrorAction` returns an error
rather than resolving when the terminal is gone
(`internal/provider/claude/actions.go:135`); a notification consumed by a send
that went nowhere cannot be recovered.

---

## Part 2 — where the interface stops

The registry is clean. The code around it is not. Sixteen sites name Claude
directly, and each one is a bug the moment a second provider registers.

| Where | What is hardcoded | Effect on provider two |
|---|---|---|
| `server/api.go:1177`, `:1223`, `:1792` | `Source: "claude"` on every new row | a Codex session is filed as Claude |
| `server/api.go:1109` | mode gate on `providerID != "claude"` | no mode is ever recorded |
| `server/api.go:363` | `ListNotifications("claude", ...)` | pending count always zero |
| `server/api.go:395` | transcript lookup gated on source | no history panel |
| `server/api.go:682` | "provider %s has no permission modes" | mode switch rejected |
| `server/api.go:1028` | `claudeprovider.RegenerateTitle` | Claude titles a Codex session |
| `server/server.go:105` | `reporter.New("claude", db)` | narration filtered by the wrong list |
| `server/trust_watcher.go:178` | `Source: "claude"` | wrong card type |
| `daemon/daemon.go:79` | resume gated on source | never wakes cold |
| `daemon/hooks.go` | writes `~/.claude/settings.json` only | no hooks installed |
| `cmd/helios/ptyhost.go:78` | fallback resolves the `claude` binary | wakes as the wrong agent |
| `discovery/claude.go` | scans `~/.claude/projects` only | hand-started sessions invisible |
| `transcript/reader.go:87` | `ParseClaudeTranscript` is the only parser | history panel empty |
| `notifications/manager.go:194` | resolves with `resolved_by = "claude"` | wrong attribution |
| `mobile/.../card_registry.dart` | switch on `claude.*` types | no card renders |

They group into six capabilities. Part 3 moves each behind the registry.

---

## Part 3 — the interface as it must be

Six additions. Each one deletes hardcoded sites from the table above.

### 1. Resume

```go
type ResumeBuilder func(sessionID, resumeID, mode string) []string
func RegisterResumeBuilder(providerID string, b ResumeBuilder)
func GetResumeBuilder(providerID string) ResumeBuilder
```

Replaces the gate at `daemon/daemon.go:79`. Returning nil means "no resume":
`ptyhost` keeps its fallback and the session stays cold.

**Session identity.** Add a `resume_id` column to `sessions`. It holds whatever
string the provider needs to wake a session. The daemon never reads it.

This exists because Helios mints the session id at `api.go:1155` and passes it
to `Backend.Start`, to the session row and to the builder. Claude accepts it
with `--session-id`. Codex has no such flag and mints its own — verified
against `codex-cli 0.150.1`. So the two ids must be allowed to differ.

For Claude, `resume_id` equals `session_id`. Nothing changes.

A provider whose agent mints the id learns it from its own `SessionStart`
handler and writes it with `DB.UpdateSessionResumeID`.

### 2. Hook installation

```go
type HookInstaller interface {
    Install(scope Scope) error   // Scope is User or Project
    Hash() string                // of the table this build would write
    Installed() (hash string, ok bool)
    Remove() error
}
func RegisterHookInstaller(providerID string, h HookInstaller)
```

This is the one place a real Go interface earns its keep. The four methods are
always implemented together, and `daemon/hooks.go` already has all four as
free functions for Claude.

`Hash` and `Installed` drive the outdated check that already exists. Keep it:
it is what tells a user their hooks are stale after an upgrade.

**Installers must validate their own writes.** Codex ignores a malformed
`hooks.json` in total silence — no warning, no error, no exit code. See
[46-codex-provider.md](46-codex-provider.md). A provider cannot rely on the CLI
to report a bad install.

### 3. Transcript

```go
type TranscriptLocator func(sessionID string) string
type TranscriptParser  func(path string, limit, offset int) (*transcript.TranscriptResult, error)
func RegisterTranscript(providerID string, l TranscriptLocator, p TranscriptParser)
```

`transcript.Message` is already provider-neutral: `Role`, `Content`, `Tool`,
`Summary`, `Success`, `Metadata`. Only the parser is Claude-shaped. Move
`ParseClaudeTranscript` behind the registry and the type needs no change.

The locator replaces `discovery.FindClaudeTranscript` at `api.go:395`. It exists
because a recorded path goes stale when a session enters a worktree. See
[44-transcript-path-relocation.md](44-transcript-path-relocation.md).

The caching layer in `transcript/store.go` stays provider-neutral. It keys on
path and hashes the tail. It does not care who wrote the file.

### 4. Discovery

```go
type Discoverer func(*store.Store)
func RegisterDiscoverer(providerID string, d Discoverer)
```

Moves `discovery/claude.go` behind the registry. Discovery finds sessions the
user started by hand. A provider without one simply shows fewer sessions.

### 5. Titles

```go
type Titler func(db *store.Store, sessionID, cwd, transcriptPath string,
    notify func(string, interface{})) string
func RegisterTitler(providerID string, t Titler)
```

Replaces the direct `claudeprovider.RegenerateTitle` call at `api.go:1028`.

### 6. Capabilities

```go
type ProviderCapabilities struct {
    PromptQueue     bool  // queue a prompt while the agent is mid-turn
    Resume          bool  // wake a cold session
    PermissionCards bool  // can raise an approval a client can answer
    Questions       bool  // can raise a multiple-choice question
    Elicitation     bool  // can raise an MCP server's form
    ErrorRetry      bool  // reports a failed turn as retryable
    Subagents       bool  // reports subagent lifecycle
    Transcript      bool  // has a readable history on disk
}
```

Today clients ask `source == "claude"` to decide what to draw. That is the
question they cannot answer for provider two. `enrichSession` already injects
`SupportsPromptQueue` from `GetCapabilities`; extend the same path.

**Set these from what is registered, not by hand.** A provider that declares
`Questions: true` and registers no question hook is a lie the compiler cannot
catch. Derive each flag in `RegisterProvider` where possible.

### Plus the plumbing

Not new surfaces, just the hardcoded sites:

- Write `req.Provider` into `Source` at the three creation sites.
- Drop the source filter at `api.go:363`.
- Give the reporter the session's provider, not the constant at
  `server.go:105`.
- Key `RegisterCommands` by provider id.
- Pass the resolving provider into `Mgr.Resolve` instead of the literal
  `"claude"` at `manager.go:194`.
- Key the mobile card registry on the type's provider prefix, and fall back to
  a generic permission card.

---

## Part 4 — required and optional

A provider does not have to supply everything. Each tier adds function.

| Tier | Register | The user gets |
|---|---|---|
| 0 | `ProviderInfo` + `SessionBuilder` | the provider appears; sessions start |
| 1 | `HookInstaller` + lifecycle hooks | live status, session list, terminal labels |
| 2 | a blocking hook + its action | approvals answered from the phone |
| 3 | `ResumeBuilder` + `resume_id` | cold sessions that wake |
| 4 | `TranscriptLocator` + `Parser`, `Discoverer` | the history panel; hand-started sessions |
| 5 | `SmallModelCaller`, `Titler`, `EventTypes` | auto-titles and narration |

Tier 0 is the only hard requirement. Everything above it degrades cleanly:
a missing registration means the feature is absent, never that the daemon
breaks.

That is the property to protect. Every `Get*` accessor returns nil for an
unregistered provider, and every caller must handle nil rather than assume.

---

## Part 5 — conformance

The compiler cannot check a registry. A test can.

Add `internal/provider/conformance_test.go`. For each registered provider,
assert:

1. `ProviderInfo.ID` is non-empty and unique.
2. A `SessionBuilder` is registered, and it returns non-empty argv for an empty
   `SessionSpec`.
3. The builder's argv[0] is a resolvable binary, or the bare command name.
4. Every hook type registered starts with `ID + "."`.
5. Every action type registered starts with `ID + "."`.
6. Every declared capability has the registration that backs it. Declaring
   `PermissionCards` with no blocking hook fails.
7. `PermissionModes`, if non-nil, has no duplicates and no empty strings.
8. If `Capabilities.Resume` is set, a `ResumeBuilder` is registered.

This is the check that keeps the registry honest without a fat interface.

---

## Part 6 — what the daemon keeps

State this so nobody moves it into a provider.

| Daemon owns | Why |
|---|---|
| Session rows, status, sort, pin | one vocabulary across providers |
| The terminal, through `backend.Backend` | a PTY is a PTY |
| Notification fan-out, SSE, channels | one delivery path |
| The decision race, `Mgr.Resolve` | first surface wins, decided once |
| The HITL overlay renderer | one look on every terminal |
| Eviction and the memory budget | a global budget, not a per-provider one |
| Auth, tunnels, discovery of hosts | nothing to do with agents |

A provider that reaches around any of these is doing something wrong.

---

## Migration

Stage 1 of [46-codex-provider.md](46-codex-provider.md) is this spec's
implementation. It ships in the same pull request as the Codex provider, in
its own commits.

**Stage 1 changes no behaviour.** That is the review contract. The existing
Claude tests are the proof, and they must pass unchanged — not adjusted, not
re-recorded. A Claude test that needs editing means the refactor changed
something it should not have.

Order the commits so each one is separately revertible:

1. `resume_id` column and its migration.
2. `Source` plumbing: `req.Provider` at the three creation sites.
3. `RegisterResumeBuilder`, and Claude registers one.
4. `RegisterHookInstaller`, and `daemon/hooks.go` becomes Claude's.
5. `RegisterTranscript`, `RegisterDiscoverer`, `RegisterTitler`.
6. Capability expansion, and clients read capabilities instead of `source`.
7. Per-provider commands and reporter.

Only then does `internal/provider/codex/` appear.
