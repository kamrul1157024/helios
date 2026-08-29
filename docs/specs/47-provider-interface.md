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
```

### Capabilities are derived, never declared

```go
func CapabilitiesOf(p Provider) Capabilities {
    _, resume := p.(Resumer)
    _, hooks  := p.(Hooker)
    _, queue  := p.(Queuer)
    // ...
}
```

The earlier draft carried a hand-written `ProviderCapabilities` struct and then
warned that a provider declaring `Questions: true` with no question hook is a
lie the compiler cannot catch. Deriving the flags removes the lie. A provider
cannot claim resume without a `Resume` method.

For the finer-grained flags a client needs — can this provider raise a
permission card, a question, an elicitation — derive from the registered hook
and action routes rather than from a boolean:

```go
_, ok := p.(Actor); ok && routes["<id>.permission"] != nil
```

### The registry shrinks to three functions

```go
func Register(p Provider) error       // rejects a duplicate or empty ID
func Get(id string) (Provider, bool)
func All() []Provider
```

Everything else the daemon needs is a type assertion at the call site. The
seven maps go away, and so do the seven `Get*` accessors.

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

## Part 3 — three kinds of provider

The same interface, three ways to satisfy it. This is what makes "bring your
own harness" real rather than aspirational.

```
                   provider.Provider
                          |
      +-------------------+-------------------+
      |                   |                   |
   native             manifest             external
   (Go, in-tree)      (a TOML file)        (a subprocess)
   claude, codex      no code at all       any language
```

| | native | manifest | external |
|---|---|---|---|
| Written in | Go | TOML | anything |
| Ships with Helios | yes | no | no |
| Can parse a transcript | yes | no | yes |
| Can call a small model | yes | no | yes |
| Needs a Helios rebuild | yes | no | no |
| Expected share of providers | 2 | most | few |

**Do not use Go's `plugin` package.** It requires the host and the plugin to be
built with the identical Go toolchain and identical versions of every shared
dependency. It cannot unload. It has no Windows support. Helios ships a
codesigned macOS binary, and `dlopen` of an unsigned object there is its own
problem. The failure mode is a panic at load with an unreadable message. Rule
it out explicitly so nobody proposes it again.

---

## Part 4 — manifest providers

Most agent CLIs need no code. Look at what the Claude provider actually does:
build argv, write a hook table, map hook fields onto Helios status, and reply
to blocking hooks with a decision. Only the transcript parser and the
small-model caller are algorithms.

So a declarative file covers most of it.

```toml
# ~/.helios/providers/aider.toml
id   = "aider"
name = "Aider"
icon = "terminal"

[launch]
command = "aider"
args    = ["--model", "{{.Model}}"]
# Positional last: anything after it reads as more prompt.
prompt  = { position = "last" }
env     = { HELIOS_SESSION = "{{.SessionID}}" }

[resume]
args = ["--restore-chat-history"]
# resume_id travels as {{.ResumeID}} when the agent mints its own.

[modes]
values  = ["ask", "auto"]
default = "ask"
ask     = []                  # extra argv per mode
auto    = ["--yes-always"]

[hooks.install]
kind     = "file"             # or "none"
path     = "~/.aider.conf.yml"
format   = "yaml"
template = "hooks.yml.tmpl"

# One block per event the agent can send.
[[hooks.event]]
path       = "session/start"  # POST /hooks/aider/session/start
status     = "idle"
session_id = "{{.session_id}}"
transcript = "{{.transcript_path}}"
started    = true             # fires SessionStarted

[[hooks.event]]
path   = "tool/pre"
status = "active"
report = { type = "tool_pre", tool = "{{.tool_name}}" }

[[hooks.event]]
path     = "permission"
blocking = true
status   = "waiting_permission"
timeout  = "inherit"          # uses HookTimeoutSeconds
notification = { type = "aider.permission",
                 title = "{{.tool_name}}",
                 detail = "{{.tool_input.command}}" }
choices = ["Allow once", "Deny"]        # painted over the terminal too
reply.approved = '{"decision":"allow"}'
reply.denied   = '{"decision":"deny","message":"Denied via helios"}'

[[hooks.event]]
path    = "stop"
status  = "idle"
resolve = "session"           # clear this session's pending notifications
reply   = "{}"
```

A `manifest.Provider` value implements `Provider`, `Hooker`, `HookInstaller`,
`Actor`, `Moder` and `Commander` by interpreting that file. It implements
`Transcriber`, `SmallModel` and `Titler` never — and the tier model in Part 6
already says what a session then loses.

The template data is the hook payload, decoded to `map[string]any`. Use Go's
`text/template`, which is already in the binary.

**Validate the manifest at load, not at use.** A field that names a status
Helios does not have, or a template that will not parse, must fail when the
file is read. The Codex lesson applies to Helios itself: a silent skip is worse
than a loud refusal.

---

## Part 5 — external providers

For the parts a manifest cannot express. An external provider is a subprocess.
Helios speaks to it over stdin and stdout in line-delimited JSON.

```
→ {"id":1,"method":"info"}
← {"id":1,"result":{"id":"myagent","name":"My Agent","icon":"robot"}}

→ {"id":2,"method":"launch","params":{"session_id":"...","prompt":"...","cwd":"..."}}
← {"id":2,"result":{"argv":["myagent","--session","..."],"env":{},"mode":"ask"}}

→ {"id":3,"method":"parse_transcript","params":{"path":"...","limit":50,"offset":0}}
← {"id":3,"result":{"messages":[...],"total":120}}
```

The method set is the interface, one method per capability method. A provider
answers `method not found` for anything it does not implement, and Helios
records that as the capability being absent. So the same discovery rule holds:
capabilities are found, not declared.

Line-delimited JSON over stdio rather than gRPC. It needs no schema compiler,
no new dependency, and it can be implemented in twenty lines of Python or
Node — which is the point of the feature. Helios already speaks JSON to
everything else it talks to.

A manifest may name a helper for the parts it cannot express:

```toml
[external]
command = ["python3", "~/.helios/providers/myagent/parse.py"]
methods = ["parse_transcript", "complete"]
```

That mixes the two kinds: declarative for the common parts, a subprocess for
the algorithms. It is the shape most third-party providers should take.

---

## Part 6 — tiers

A provider does not have to implement everything. Each tier adds function.

| Tier | Implements | The user gets |
|---|---|---|
| 0 | `Provider` | it appears; sessions start |
| 1 | `+ Hooker, HookInstaller` | live status, labels, the session list |
| 2 | `+ Actor` and a blocking route | approvals answered from a phone |
| 3 | `+ Resumer` | cold sessions that wake |
| 4 | `+ Transcriber, Discoverer` | the history panel; hand-started sessions |
| 5 | `+ SmallModel, Titler, Narrator` | auto-titles and narration |

Tier 0 is the only requirement. Every `p.(SomeInterface)` at a call site must
handle the failed assertion by degrading, never by erroring. That property is
what lets a twenty-line manifest be a legitimate provider.

---

## Part 7 — trust

A manifest specifies argv. Dropping a file into `~/.helios/providers/` is
arbitrary code execution on the next session start. An external provider is a
subprocess Helios spawns. Both need a trust gate.

Take the design Codex got right and skip the part it got wrong:

- **Right:** trust binds to a hash of the file. Editing it re-flags it.
- **Wrong:** failing silently. Codex skips untrusted hooks and tells nobody,
  which is the single worst finding in [46](46-codex-provider.md).

So:

1. A manifest or external provider loads only after the user trusts its hash.
2. An untrusted provider still appears in `helios providers`, marked
   **untrusted**, with the command to trust it. It never launches.
3. `helios providers trust <id>` records the hash. A changed file returns to
   untrusted.
4. Native providers are trusted by definition. They are the binary.

Say it loudly, in the list and in the health check. Silence is the bug.

---

## Part 8 — what this costs

Three honest costs.

**A manifest engine is a small language, and it will grow.** Every provider
that nearly fits will ask for one more field. The counter-pressure is Part 5:
when a manifest is not enough, the answer is a subprocess, not a new keyword.
Hold that line or the TOML becomes a programming language with no debugger.

**Declarative mappings are harder to debug than Go.** A wrong template
produces an empty notification detail, not a stack trace. Manifest providers
need `helios providers test <id> --event permission --payload file.json`, which
renders the mapping against a recorded payload and prints what Helios would
do. Build it with the engine, not after.

**Type assertions move errors from compile time to run time.** The compiler
will not tell a native provider it forgot `Resumer`. Part 9 answers that with a
conformance test.

---

## Part 9 — conformance

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

Manifest and external providers run the same test through a fixture, which is
the point of them satisfying the same interface.

---

## Part 10 — what the daemon keeps

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
Make `claude` a value implementing them. Delete the seven maps and the three
by-name imports. No behaviour change; the existing Claude tests are the proof
and must pass unedited.

**Stage 2 — Codex** as a second native provider. See
[46](46-codex-provider.md). This is what proves the interface holds for a
harness with different assumptions about session identity and hook transport.

**Stage 3 — manifest providers.** The loader, the template engine, the trust
gate, and `helios providers test`. Prove it by rewriting a native provider as a
manifest and showing the conformance test passes for both.

**Stage 4 — external providers.** The stdio protocol and the manifest
`[external]` block.

Stages 1 and 2 ship together, as already agreed. Stages 3 and 4 are separate
and need their own specs. Nothing in stages 1 and 2 should be shaped around
them beyond what is written here — the interface is the commitment, the plugin
kinds are implementations of it.
