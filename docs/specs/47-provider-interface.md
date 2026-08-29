# The Provider Interface

## Status

Supersedes [18-provider-interface.md](18-provider-interface.md), which
describes an interface Helios never built.

This revision replaces an earlier draft of this spec. That draft argued to keep
the registry of free functions and not define an interface. It was wrong on the
point that matters: it optimised for the two providers in the tree, and Helios
is going to accept providers it did not write.

[46-codex-provider.md](46-codex-provider.md) is the first consumer.

---

## The problem, stated correctly

The earlier draft said the registry is clean and only the code around it is
coupled. Half right. The registry holds *function types*, so a provider cannot
be handed to anything as a value. The daemon therefore reaches into provider
packages by name:

```go
internal/daemon/hooks.go:11    claude "github.com/.../internal/provider/claude"
internal/daemon/daemon.go:87   return claude.ResumeArgs(sessionID, mode)
internal/daemon/hooks.go:19    blocking := claude.HookTimeoutSeconds
internal/server/api.go:19      claudeprovider "github.com/.../internal/provider/claude"
internal/server/api.go:672     if !claudeprovider.ValidPermissionMode(mode)
internal/server/api.go:1028    claudeprovider.RegenerateTitle(...)
internal/server/api.go:1112    return claudeprovider.LaunchPermissionMode(spec)
```

Three packages import one provider by name. That is the scattering. A registry
of functions cannot fix it, because there is no value to pass around. An
interface can.

## What this actually touches

Counted, not estimated. An earlier draft said "sixteen call sites" and meant Go
only. The clients are the larger half.

| Where | Files | The coupling |
|---|---|---|
| Go daemon | ~10 | `Source: "claude"` on new rows, source gates, three by-name imports, `reporter.New("claude")` |
| Desktop (`desktop/src`) | 8 | `ALERT_TYPES` list, alert-settings groups, two card switches, notify routing, provider default |
| Desktop tests | 2 | hardcoded type lists |
| Mobile (`mobile/lib`) | 9 | card registry, `isBlocking`, home-screen branch chain, default-prefs map, settings list, `source == 'claude'` gates, direct `providers/claude/*` imports |
| **Total** | **~29** | |

### What each client does with an unknown provider

Read in full. Desktop was also run: built, launched under Xvfb against the live
daemon, driven over CDP, and given a real `claude.permission` raised through
`POST /hooks/claude/permission`.

**Desktop works, and degrades visibly.** The app connects, lists real sessions,
and a blocking notification opens a HUD window rendering
`PERMISSION REQUEST / Bash / Approve / Deny / Edit before approving`.
Abandoning the hook request retracts the card, so the retraction path is sound.

The gap is `isBlocking` (`desktop/src/shared/notifications.ts:8`), a literal
allowlist of `claude.*` strings. A `codex.permission` returns false, so it is
classed as news: a banner instead of the HUD card that can answer it. The
notification is visible and the agent waits for an answer no desktop surface
offers.

**Mobile is worse: it raises no OS notification at all.** The dispatch in
`home_screen.dart:121-175` is an if/else chain over seven literal `claude.*`
types **with no final else**. An unrecognised type falls out of the chain in
silence.

In-app it degrades: `dashboard_screen.dart:104` does
`card ?? _buildStatusCard(...)`, so the notification is visible but not
answerable. The phone is the surface Helios exists for — the Claude provider's
own comment calls a session that stops on a permission question "a session the
user cannot finish from the lock screen". A Codex session would do exactly
that, with no buzz.

One more, worth quoting because the comment is already ahead of the code:

```dart
/// Whether this notification needs user action (checks all registered providers).
bool needsAction(HeliosNotification n) {
  return n.needsClaudeAction;      // card_registry.dart:63
}
```

**Two things already generalise, and are cheaper than they look.**
`newsession.tsx` fetches the provider list and falls back to the first entry,
so its `useState('claude')` is harmless. And `detail.tsx:538`'s
`if (session.source !== 'claude') return null` guards code that already
resolves modes per provider through `providers.find(p => p.id ===
session.source)` — deleting the line is the whole fix.

**The one good default, in both clients.** Alert lookup falls back to enabled
(`shouldSound`'s `?? true`, `isAlertEnabled`'s `?? true`), so an unknown
provider is noisy rather than silent. Silence would be the dangerous
direction. Keep that shape when the catalogue moves to the daemon.

`desktop/test/provider-coupling.test.ts` pins all of the above. Each assertion
states today's behaviour, so the ones marked `GAP` fail when the gap closes —
that failure is the signal to update the file.

**Mobile could not be run.** Flutter is not installed on this machine, so every
mobile claim here is from reading. It is the least-verified part of the plan.

### The clients own a catalogue they should be served

Both clients hardcode the same thing four times each: the list of notification
types, their labels and descriptions, which are blocking, and their default
alert preference.

```ts
// desktop/src/shared/notifications.ts:23
export const ALERT_TYPES = [
  'claude.permission', 'claude.question', 'claude.elicitation.form',
  'claude.elicitation.url', 'claude.trust', 'claude.done', 'claude.error',
] as const
```

```dart
// mobile/lib/providers/card_registry.dart:71
type == 'claude.permission' || type == 'claude.question' || ...
```

The daemon serves `event_types` for the reporter (`api.go:1620`) — a different
list, for a different purpose. Nothing serves the notification catalogue.

So adding a provider means editing both clients in four places each, and
forgetting one is silent. Desktop half-anticipates this: `settings.tsx:54`
computes `MISSING` for alert types absent from its own groups. A Codex type
would be in neither list, so it would not even be missing.

**This is the same mistake `PermissionModes` already avoided.** That list is
served rather than hardcoded, precisely because the vocabulary belongs to the
provider. The notification catalogue belongs to the provider too.

Add to the daemon, from what providers register:

```go
GET /api/notification-types
→ [{"type":"claude.permission","provider":"claude","label":"Permission requests",
    "detail":"...","blocking":true,"group":"action_required","default_alert":true}]
```

Clients render the catalogue instead of enumerating it, and gain nothing to
edit when a provider is added. This means `Actor` needs metadata alongside
each route:

```go
type ActionRoute struct {
    Handler  ActionHandler
    Label    string
    Detail   string
    Blocking bool
    Group    string
}

type Actor interface {
    ActionRoutes() map[string]ActionRoute
}
```

That is a bigger change than a rename, and it is the honest cost of a second
provider. Doing it as part of stage 1 is cheaper than doing it twice.

---

## The house already has the pattern

`internal/tunnel` solved this shape of problem already:

```go
type Tunnel interface {          // small core, every provider implements it
    Start(localPort int) error
    Stop() error
    URL() string
    Provider() string
    PID() int
}

type Reconciler interface {      // optional, found by type assertion
    Reconcile(ctx context.Context) (url string, active bool, err error)
}
```

Five methods everyone implements, one optional interface for the providers that
can do more. No registry of function pointers. No package reaching into another
by name.

This spec applies that idiom to agents. It is not a new convention — it is the
one Helios already uses.

---

## Part 1 — the core interface

All of it in `internal/provider/provider.go`. One file, one place.

```go
// Provider is an agent harness Helios can drive.
type Provider interface {
    // Info identifies the provider. It carries no capability flags: what a
    // provider supports is discovered from the interfaces it implements.
    Info() Info

    // Launch returns everything needed to start a session.
    Launch(SessionSpec) (Launch, error)
}

type Info struct {
    ID   string `json:"id"`
    Name string `json:"name"`
    Icon string `json:"icon"`
    // Kind is how this provider reached the registry: native, manifest or
    // external. Clients show it; the daemon does not branch on it.
    Kind Kind `json:"kind"`
}

// Launch is the result of asking a provider to start something.
type Launch struct {
    // Argv is executed directly, never through a shell and never joined into
    // a string. A prompt full of quotes reaches the agent as typed.
    Argv []string
    // Env is merged over the daemon's agent environment.
    Env map[string]string
    // Mode is the permission mode the session will actually run under, or ""
    // when the provider has no such concept. Recorded at launch so a wake can
    // replay it rather than guess.
    Mode string
}
```

Two methods. That is the whole requirement. A provider that implements only
these appears in the list and starts sessions.

`Launch` returning `Env` is new and load-bearing. The Codex provider needs
`HELIOS_SESSION` in the session's environment so its hooks can identify
themselves — measured in [46](46-codex-provider.md). Today `agentEnv()` in
`cmd/helios/ptyhost.go` is fixed for every provider.

## Part 2 — capability interfaces

Optional. Each is one or two methods. All in the same file.

```go
type Resumer interface {
    Resume(sessionID, resumeID, mode string) (Launch, error)
}

type Hooker interface {
    // HookRoutes maps a path suffix to a handler.
    // "session/start" serves POST /hooks/<provider-id>/session/start.
    HookRoutes() map[string]HookHandler
}

type HookInstaller interface {
    InstallHooks(Scope) error
    HookHealth() HookHealth
    RemoveHooks() error
}

type Actor interface {
    // ActionRoutes maps a notification type to the handler that answers it.
    ActionRoutes() map[string]ActionHandler
}

type Moder interface {
    PermissionModes() []string   // most restrictive first
    ValidMode(mode string) bool
}

type ModelLister interface {
    Models() ([]ModelInfo, error)
}

type Transcriber interface {
    LocateTranscript(sessionID string) string
    ParseTranscript(path string, limit, offset int) (*transcript.TranscriptResult, error)
}

type Discoverer interface {
    Discover(*store.Store)
}

type Titler interface {
    Title(db *store.Store, sessionID, cwd, transcriptPath string, notify Notify) string
}

type SmallModel interface {
    Complete(ctx context.Context, system, prompt string) (string, error)
}

type Narrator interface {
    EventTypes() []EventTypeInfo
}

type Commander interface {
    Commands() []Command
}

type Queuer interface {
    // Queue hands a prompt to a busy session without typing into its
    // terminal. Codex has `codex queue --thread`; Claude does not.
    Queue(sessionID, resumeID, text string) error
}

type ScreenWatcher interface {
    // MatchScreen inspects rendered terminal text for a modal the agent is
    // blocked on and that no hook reports. Returns the notification to raise,
    // or nil. Called on screen change for sessions that have not reported in.
    MatchScreen(screen string) *ScreenPrompt
}
```

`ScreenWatcher` exists because `internal/server/trust_watcher.go` is a provider
concern living in the daemon. It matches four Claude-specific phrases
(`trust_watcher.go:87`) and raises a hardcoded `claude.trust`.

Codex has the same class of dialog with different wording, measured in
[46](46-codex-provider.md), so the daemon's patterns miss it and the session
stalls unreported. The daemon should keep the watching — the polling, the TTL,
the screen source — and ask the provider what a screen means.

### Capabilities are derived, never declared

The earlier draft carried a hand-written `ProviderCapabilities` struct and then
warned that a provider declaring `Questions: true` with no question hook is a
lie the compiler cannot catch. Deriving the flags removes the lie. A provider
cannot claim resume without a `Resume` method.

### Resolve capabilities in the registry, not at the call site

The obvious form is a type assertion wherever the daemon needs one:

```go
if r, ok := p.(Resumer); ok { ... }   // do not do this
```

Do not. It works for a Go provider and fails for every other kind. A provider
loaded from a file or spoken to over a pipe is **one Go type serving many
different agents**, so it either implements `Resumer` for all of them or none.
Type assertion cannot express "this instance resumes, that one does not".

Resolve once, at registration:

```go
type registration struct {
    p Provider

    // Each is nil when the provider does not offer it.
    resume      Resumer
    hooks       Hooker
    installer   HookInstaller
    actor       Actor
    transcriber Transcriber
    // ...
}

func Register(p Provider) error       // rejects a duplicate or empty ID
func Get(id string) (Provider, bool)
func All() []Provider

// Capability accessors. Nil means the provider does not offer it.
func ResumerFor(id string) Resumer
func HookerFor(id string) Hooker
func TranscriberFor(id string) Transcriber
// ...
```

`Register` fills the fields by type assertion. That is the only place an
assertion appears. Call sites ask the registry and check for nil, exactly as
they check for nil today.

The gain is that a later provider kind fills the same fields from whatever it
knows — a list of methods a subprocess advertises, a set of sections present in
a file — and **not one call site changes**. This is the single most important
decision in the spec for keeping the next step cheap, and it costs nothing now.

### Registration must work after startup

Do not register from `init()`. Guard the registry with a mutex and let
`Register` be called at any time. Native providers still register at daemon
start; a kind that is discovered by scanning a directory cannot. Add
`Deregister(id)` at the same time — a reloaded provider needs to replace
itself, and it is three lines now versus a locking audit later.

### The health contract

```go
type HookHealth struct {
    Installed bool   // the table is on disk
    Current   bool   // and it matches this build's hash
    Effective bool   // and the agent will actually run it
    Detail    string // what to do when one of these is false
}
```

`Effective` is separate because of a measurement. Codex refuses to run
untrusted hooks and reports nothing: the file is read, the hooks are skipped,
the turn succeeds. A daemon checking only "did I write the file" would report
healthy while receiving no events. See [46](46-codex-provider.md).

The rule, stated once: **a provider must never infer health from its own last
write.** Health is what the agent does, not what the daemon intended.

---

## Part 3 — third-party providers: designed for, not built

Helios will one day accept providers it did not write. **Nothing in that
direction is being built now.** This section exists so the interface does not
have to be reopened when it is.

The likely shapes, for context only:

| Kind | Written in | Status |
|---|---|---|
| native | Go, in-tree | **now** — claude, codex |
| declarative | a config file | later, own spec |
| external | a subprocess over stdio | later, own spec |

The bet behind both later kinds: most agent CLIs need no code. The Claude
provider is argv construction, a hook table, a field mapping, and decision
replies. Only the transcript parser and the small-model caller are algorithms.
Neither kind is designed here — that is a separate spec when the time comes.

### The seams that must exist now

Six decisions make the later work cheap. Every one is free today, and every one
is expensive to retrofit.

| Seam | Why it must be now |
|---|---|
| Capability accessors on the registry | a non-Go provider is one type serving many agents; type assertion at the call site cannot express per-instance capability. **The one that matters most.** |
| `Register` works after startup, with a mutex, plus `Deregister` | a scanned-from-disk provider cannot register at `init()` |
| `Launch` carries `Env` | a provider must inject its own environment; needed by Codex today anyway |
| `Info.Kind` exists and clients show it | later kinds need a trust state to hang off; native is trusted by definition |
| IDs namespace hook routes and notification types | third-party IDs must not collide with `claude.` or `codex.` |
| Hook dispatch goes through the registry, not a package-level map | a second dispatch path becomes one function rather than a rewrite |

Keep `Kind` even though it has one value today. A field nobody reads is
cheaper than a migration.

### One thing to avoid designing around

`HookHandler` takes a `*HookContext` full of concrete daemon types — a
`*store.Store`, a `*notifications.Manager`, a `backend.Backend`. None of that
crosses a pipe or comes out of a config file. A non-Go provider will eventually
need a different shape: it reports *what happened*, and the daemon decides what
to do.

**Do not build that now.** Native providers want the direct access, the
translation is guesswork until a second kind exists, and inventing an event
vocabulary before there is a consumer is how specs rot. The seam above — hook
dispatch through the registry — is enough to add a second path later beside
`Hooker` rather than in place of it.

**Do not use Go's `plugin` package**, whenever the time comes. It requires host
and plugin to share an identical Go toolchain and identical versions of every
dependency. It cannot unload. It has no Windows support. Helios ships a
codesigned macOS binary, where `dlopen` of an unsigned object is its own
problem. The failure mode is a panic at load with an unreadable message.
Recorded here so nobody proposes it later.

---

## Part 4 — tiers

A provider does not have to implement everything. Each tier adds function.

| Tier | Implements | The user gets |
|---|---|---|
| 0 | `Provider` | it appears; sessions start |
| 1 | `+ Hooker, HookInstaller` | live status, labels, the session list |
| 2 | `+ Actor` and a blocking route | approvals answered from a phone |
| 3 | `+ Resumer` | cold sessions that wake |
| 4 | `+ Transcriber, Discoverer` | the history panel; hand-started sessions |
| 5 | `+ SmallModel, Titler, Narrator` | auto-titles and narration |

Tier 0 is the only requirement. Every capability accessor returning nil must be
handled by degrading, never by erroring.

Enforce that now, while there are two providers and both are near the top of
the table. It is the property that later lets a very small provider be a
legitimate one, and it cannot be added retroactively — it has to be true at
every call site, and the only cheap time to make it true is when the call sites
are being touched anyway.

---

## Part 5 — trust

Nothing to build now. Native providers are the binary, so they are trusted by
definition.

Recorded because it constrains the seam. Any later kind decides argv from data
outside the binary, which is arbitrary code execution on the next session
start. So a trust gate is not optional then, and `Info.Kind` is where its state
will hang. That is the whole reason `Kind` exists today with one value.

When it is built, take the design Codex got right and refuse the part it got
wrong:

- **Right:** trust binds to a hash. Editing the source re-flags it.
- **Wrong:** failing silently. Codex skips untrusted hooks and tells nobody,
  which is the worst finding in [46](46-codex-provider.md). An untrusted
  provider must appear in the list, marked untrusted, with the command to fix
  it — and must never launch.

---

## Part 6 — what this costs

Three honest costs.

**Optional interfaces move errors from compile time to run time.** The compiler
will not tell a provider it forgot `Resumer`; the feature is simply missing.
Part 7 answers that with a conformance test, which is now the only test in
`internal/provider` — the package has none today.

**Capability accessors add a layer.** `provider.ResumerFor(id)` is one hop more
than `p.(Resumer)`, and for the two native providers in the tree it buys
nothing. It is paid now so the third kind costs no call-site changes. If
third-party providers never happen, this was waste — a small, contained amount
of it, in one file.

**Two providers is a thin basis for an interface.** Claude and Codex are
similar: both are terminal CLIs with hook engines and JSONL transcripts. An
agent that is none of those will strain this design. The tiers are the hedge —
a provider that fits badly implements less rather than forcing a change — but
the hedge is untested until something strains it.

---

## Part 7 — conformance

`internal/provider/conformance_test.go` runs over every registered provider,
whatever its kind.

1. `Info().ID` is non-empty, lower-case, and unique.
2. `Launch` on an empty `SessionSpec` returns non-empty `Argv` and no error.
3. `Argv[0]` resolves on `PATH`, or is a bare command name.
4. Every key of `HookRoutes()` is a clean relative path — no leading slash, no
   `..`.
5. Every key of `ActionRoutes()` starts with `Info().ID + "."`.
6. If `Actor` is implemented, every blocking hook route has an action whose
   type it raises, and the reverse.
7. `PermissionModes()` has no duplicates and no empty strings.
8. `Resume` output argv is non-empty when `Resumer` is implemented.

The test is table-driven over `provider.All()`, so a later kind is covered by
registering it — no new test. That is deliberate and free.

---

## Part 8 — what the daemon keeps

| Daemon owns | Why |
|---|---|
| Session rows, status, sort, pin | one vocabulary across providers |
| Terminals, through `backend.Backend` | a PTY is a PTY |
| Notification fan-out, SSE, channels | one delivery path |
| The decision race, `Mgr.Resolve` | first surface wins, decided once |
| The HITL overlay renderer | one look on every terminal |
| The hook route and the timeout budget | Helios must give up before the CLI |
| Eviction and the memory budget | global, not per provider |
| Auth, tunnels, host discovery | nothing to do with agents |

A provider that reaches around any of these is doing something wrong.

---

## Staging

**Stage 1 — the interface.** Define `Provider` and the capability interfaces.
Make `claude` a value implementing them. Add the registry accessors. Delete the
seven maps and the three by-name imports. Write the conformance test, which is
the first test `internal/provider` will have.

No behaviour change. The contract is that **no test assertion changes**.

That contract is now costed rather than hoped for. Counting every call in
`internal/provider/claude/*_test.go` that would take a receiver:

| File | Calls to touch |
|---|---|
| `register_test.go` | 22 |
| `actions_test.go` | 13 |
| `hooks_test.go` | 1 |
| `apierror_test.go`, `question_test.go`, `autotitle_test.go` | 0 |

Thirty-six, plus five lines setting the `terminalBackend` and `mcpPort`
package variables, which become struct fields. Every one is mechanical: add a
receiver, or read `.Argv` off the returned `Launch`. No assertion moves.

Two details make it this cheap. `callHook` takes the handler as a **function
value** (`hooks_test.go:198`), so `p.handlePermission` substitutes for
`handlePermission` unchanged. And the three files with zero edits test pure
functions that do not belong to the provider value at all.

The baseline is `go test ./...`. Everything passes except
`internal/terminal`'s two Claude e2e cases, which fail on this machine for
unrelated reasons — the assertion `"Welcome to Claude Code"` no longer matches
what Claude Code renders. Worth fixing, but not in this change.

**Stage 2 — Codex** as a second native provider. See
[46](46-codex-provider.md). This is what proves the interface holds for a
harness with different assumptions about session identity and hook transport.
It is also the only real test of the design: an interface derived from one
provider is a description, not an abstraction.

Stages 1 and 2 ship together in one pull request, in separate commits.

**Not now:** third-party provider kinds. Part 3 lists the six seams that keep
them cheap. Build nothing else toward them — no loader, no config format, no
protocol. Each is its own spec when there is a reason.
