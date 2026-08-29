# Codex Provider

## What this spec covers

Helios runs one agent today: Claude Code. This spec adds a second one: the
OpenAI Codex CLI.

The spec has three parts. Part 1 states what the provider interface is now.
Part 2 states what Codex gives us. Part 3 states the design and the cost.

[47-provider-interface.md](47-provider-interface.md) defines the interface this
spec extends. Read it first. Part 1 below repeats enough of it to make this
spec readable on its own.

[18-provider-interface.md](18-provider-interface.md) is superseded. It
describes a provider interface Helios never built.

---

## Part 1 — the interface as built

### The registry

`internal/provider/registry.go` is the whole interface. It is not a Go
interface. It is seven global maps and seven register functions. A provider
package calls them from one `Register()` function at daemon start.

| Register function | Type | What it gives the daemon |
|---|---|---|
| `RegisterProvider` | `ProviderInfo`, `SessionBuilder`, `ModelsFetcher` | identity, capabilities, permission modes, launch argv, model list |
| `RegisterHook` | `HookHandler` per hook type | one HTTP handler per agent event |
| `RegisterAction` | `ActionHandler` per notification type | how a client's answer becomes a `Decision` |
| `RegisterSmallModelCaller` | `SmallModelCaller` | a cheap model for titles and narration |
| `RegisterEventTypes` | `[]EventTypeInfo` | the reporter's filter list |
| `RegisterCommands` | `[]Command` | slash commands the clients offer |

`internal/provider/claude/register.go:180` is the only caller.

### SessionBuilder

```go
type SessionBuilder func(SessionSpec) []string
```

`SessionSpec` carries `SessionID`, `Prompt`, `Model`, `CWD`, `PermissionMode`
and `SkipPermissions`. The builder returns argv. The terminal host executes the
argv directly, so a prompt with quotes reaches the agent as typed.

**The daemon mints `SessionID` before it launches anything.**
`internal/server/api.go:1155` calls `uuid.New()`. It passes that id to
`Backend.Start`, to the session row, and to the builder. Claude accepts it with
`--session-id`. This is the assumption Codex breaks. See Part 3.

### The hook interface

A hook is an HTTP POST to the daemon's internal port. The path names the
handler.

```
POST /hooks/claude/permission  →  "claude.permission"  →  handlePermission
```

`internal/server/hooks.go:18` joins the path segments with dots and looks the
key up in the registry. It passes the raw body through untouched. The provider
package owns the payload shape.

A handler receives a `HookContext`:

| Field | Use |
|---|---|
| `DB` | session and notification rows |
| `Mgr` | create a notification, wait for the answer |
| `Terminal` | rename, kill, send keys to the session's terminal |
| `HITL` | paint a prompt over the session's terminal |
| `Notify` | broadcast an SSE event |
| `SessionStarted` | the agent reported in; stop the trust watcher |
| `PromptSubmitted` | a typed prompt landed |
| `Report` | push a narration event to the reporter |

A blocking hook does four things, in order: it creates a notification, it calls
`Mgr.Register` to reserve the answer slot, it paints a terminal prompt with
`showPrompt`, then it blocks in `waitForDecision`. The first surface to answer
wins. `internal/provider/claude/hooks.go:88` is the reference implementation.

The timeout is one number in two places. `decisionTimeout` is 5 minutes.
`HookTimeoutSeconds` is 5 minutes 30 seconds and goes to the CLI. Helios must
give up first, or the CLI abandons a prompt that is still on screen.

### Hook installation

`internal/daemon/hooks.go` writes the hook table into
`~/.claude/settings.json`. It hashes the table so it can detect an outdated
install after an upgrade. Most hooks use `"type": "http"`. Four lifecycle hooks
use `"type": "command"` and pipe stdin through curl, because Claude Code
v2.1.101 does not fire HTTP hooks for them:

```
cat | curl -s -X POST -H 'Content-Type: application/json' -d @- <url>
```

This curl pattern is the one Codex needs everywhere. Part 3 uses it.

### Where the interface stops

The registry is clean. The code around it is not. Sixteen places name Claude
directly.

| Where | What is hardcoded |
|---|---|
| `internal/server/api.go:1177`, `:1223`, `:1792` | `Source: "claude"` on every new session row, whatever provider was asked for |
| `internal/server/api.go:1109` | `launchPermissionMode` returns "" unless the provider is Claude |
| `internal/server/api.go:363` | `ListNotifications("claude", ...)` for the pending count |
| `internal/server/api.go:395` | transcript lookup gives up unless the source is Claude |
| `internal/server/api.go:682` | "provider %s has no permission modes" for anything else |
| `internal/server/api.go:1028` | `claudeprovider.RegenerateTitle` |
| `internal/server/server.go:105` | `reporter.New("claude", db)` |
| `internal/server/trust_watcher.go:178` | `Source: "claude"` |
| `internal/daemon/daemon.go:79` | resume argv returns nil unless the source is Claude |
| `internal/daemon/hooks.go` | installs to `~/.claude/settings.json` only |
| `cmd/helios/ptyhost.go:78` | the no-`--cmd` fallback resolves the `claude` binary |
| `internal/discovery/claude.go` | scans `~/.claude/projects` only |
| `internal/transcript/reader.go:60` | parses the Claude `.jsonl` shape only |
| `internal/notifications/manager.go:194` | resolves with `resolved_by = "claude"` |
| `mobile/lib/providers/card_registry.dart` | a switch on `claude.*` notification types |

None of these is hard to fix. All of them must be fixed. A second provider
turns each one from a shortcut into a bug.

---

## Part 2 — what Codex gives us

Codex CLI **0.150.1** is installed at `~/.local/bin/codex`. It was released
2026-08-27. Everything below marked ✓ was checked against that binary on
2026-08-29. Two claims still need a logged-in session; they are marked and
listed in "Still open".

### Codex has a hook engine

This is the finding that makes the port cheap. Codex ships lifecycle hooks with
the same three-level shape as Claude: event → matcher group → handler list. It
reads them from `~/.codex/hooks.json`, from `<repo>/.codex/hooks.json`, or from
an inline `[hooks]` table in either `config.toml`.

✓ `codex features list` reports `hooks  stable  true`. The engine is stable and
on by default. `[features] hooks = false` turns it off.

⚠ **A malformed `hooks.json` is ignored in silence.** A file with an unknown
event name and an unknown handler type produced no warning, no error and no
exit code. Compare `config.toml`, which fails loudly and names the bad key.
Helios must therefore validate its own hook install rather than trust Codex to
complain. Add that check to the health check that already reports hook trust.

Events: `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`,
`PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`,
`SubagentStart`, `SubagentStop`, `Stop`.

The payload matches Claude's closely. Every event carries `session_id`,
`transcript_path`, `cwd`, `hook_event_name` and `model`. Most carry
`permission_mode`, and the values are Claude's own vocabulary: `default`,
`acceptEdits`, `plan`, `dontAsk`, `bypassPermissions`. Tool events carry
`tool_name`, `tool_use_id` and `tool_input`.

`PermissionRequest` answers with the shape Helios already writes:

```json
{"hookSpecificOutput": {"hookEventName": "PermissionRequest",
 "decision": {"behavior": "allow"}}}
```

### Four Codex limits that change the design

**1. There is no `http` handler type.** Only `command` and `mcp_tool` work.
Every Helios hook must go through curl.

**2. ✓ There is no `--session-id` flag.** Confirmed against `codex --help`,
`codex exec --help` and `codex resume --help`. Codex generates the id. The CLI
takes it as a positional argument to `resume`, `fork`, `archive`, `unarchive`,
`delete` and `queue`, and nowhere as an input.

**3. `PermissionRequest` cannot write a rule.** `updatedInput`,
`updatedPermissions` and `interrupt` are reserved and fail closed. Only
`allow` and `deny` with a message work.

**4. `PreToolUse` cannot ask.** `permissionDecision: "ask"` is not supported. It
marks the run failed and the tool runs anyway.

### The permission model is two axes, not one

Claude has one flag: `--permission-mode`. Codex has two. ✓ Both checked against
`codex --help`.

| Flag | Values |
|---|---|
| `--sandbox` / `-s` | `read-only`, `workspace-write`, `danger-full-access` |
| `--ask-for-approval` / `-a` | `on-request`, `never` |

`--ask-for-approval` takes **two** values in 0.150.1, not three. The docs still
list `untrusted`; the binary rejects it. Do not offer it.

`--dangerously-bypass-approvals-and-sandbox` turns both off.

Two more flags matter and are not in the docs page:

- ✓ `--approve-for-me` routes approval requests through automatic review under
  the `workspace-write` sandbox. It is a third answer to the approval question
  and worth a Helios mode of its own later.
- ✓ `--dangerously-bypass-hook-trust` runs enabled hooks without persisted
  trust, for one invocation. This *does* exist, so the trust step is skippable
  from the launch argv. See the trust decision below for why we still do not
  use it.

✓ `codex doctor` reports the shipped default as `approval policy OnRequest`
with a restricted sandbox. So `PermissionRequest` fires out of the box.

`PermissionRequest` fires only when Codex would ask a human. So
`--ask-for-approval never` silences the hook. **For Helios the useful default is
the opposite of Claude's.** Claude wants `auto`, so the agent stops asking.
Codex wants `on-request`, so the agent keeps asking and Helios answers from the
phone.

### Hook trust

Codex refuses to run a hook until a human trusts it. Trust binds to the hook's
hash, so an edit re-flags it. The `/hooks` command in the TUI does the
approval. Managed hooks skip this, but only enterprise tooling can install
them.

So installing Helios hooks takes one manual step per machine, and one more
after any upgrade that changes the hook table. Helios already hashes its hook
table for the outdated check, so it can tell the user when the step is due.

✓ `--dangerously-bypass-hook-trust` exists and Helios controls the launch argv,
so Helios *could* skip the step. It should not. The flag disables trust for
every hook in the file, not only Helios's own, so a Helios launch would silently
run a third-party hook the user never approved. One prompt per machine is the
cheaper price.

### Where a Codex session lives

Transcripts are JSONL rollout files under
`~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<id>.jsonl`. The id is a
UUID. `~/.codex/session_index.jsonl` maps ids to names, directories, models and
status. Archived sessions move to `~/.codex/archived_sessions/`.

`codex resume <id>` brings a session back. `codex resume --last` takes the most
recent one in the current directory. Resume accepts the same global flags, so
the sandbox and approval flags replay the same way Claude's mode does.

`-c key=value` sets any config key for one invocation, above every config file.
`-c 'mcp_servers.helios={url="...", http_headers={...}}'` is how the Helios MCP
server reaches a Codex session.

### Four more findings from the binary

✓ **`codex queue --thread <UUID> --message <TEXT>`** queues a message for a
running session. This is a real prompt-queue path. Helios types into the PTY
today (`SendText`). For Codex it can call the CLI instead, which is what
`ProviderCapabilities.PromptQueue` should mean here. The `--thread` argument is
the Codex id, so it needs `resume_id`.

✓ **`-p` / `--profile <name>`** layers `$CODEX_HOME/<name>.config.toml` over the
user config. This is a cleaner injection point than a long `-c` list: Helios
writes one `helios.config.toml` and passes `-p helios`. Consider it for the MCP
block.

✓ **`CODEX_HOME`** relocates the entire config directory. It gives the tests a
throwaway Codex install, and it is how the facts in this spec were checked.

✓ **`codex exec` prints `session id: <uuid>`** on its startup banner. Helios
does not use `exec` for sessions, but the small-model caller can.

---

## Part 3 — the design

### Problem 1: session identity

Helios mints the id. Codex will not take it. Three ways out:

| Option | Cost |
|---|---|
| A. Adopt the Codex id as the primary key when `SessionStart` arrives | rewrites a primary key mid-flight; the terminal socket path, the clients and the SSE stream all hold the old one |
| B. Key on the Helios id and store the Codex id beside it | one new column; two ids to keep straight |
| C. Do not resume; treat every Codex session as one-shot | throws away cold sessions, which is the feature [42-cold-sessions.md](42-cold-sessions.md) exists for |

**Take B.** Add a `resume_id` column to `sessions`. It holds whatever string
the provider needs to wake the session, and the daemon never reads it. Spec 18
already asked for this: *"resume_id — provider-specific, opaque to daemon."*
For Claude, `resume_id` equals `session_id`, so nothing changes.

Correlation works through the environment. `cmd/helios/ptyhost.go` already sets
the session's environment with `agentEnv()`. Add `HELIOS_SESSION=<helios-id>`.
Codex hooks inherit it, so the curl command sends it as a header:

```
cat | curl -sS -f -X POST \
  -H 'Content-Type: application/json' \
  -H "X-Helios-Session: $HELIOS_SESSION" \
  -d @- http://localhost:PORT/hooks/codex/session/start
```

The handler reads the header. When it is set, it looks the row up by the Helios
id and writes `resume_id` from the payload's `session_id`. When it is empty —
a Codex session the user started by hand — it falls back to the Codex id as the
key. Hand-started sessions then appear in Helios for free.

### Problem 2: hook transport

Use the curl pattern for every hook. It already works for four Claude hooks
(`internal/daemon/hooks.go:159`).

Blocking hooks work through it. A `command` hook is synchronous. Codex waits
for the process up to `timeout`, and curl's stdout is the hook's stdout, so the
daemon's JSON response becomes the decision.

Use `-sS -f`. `-f` makes curl exit non-zero without printing the body on an
HTTP error, so a daemon that is down or a path that 404s leaves stdout empty.
Codex treats that as a failed hook and proceeds. Fail-open is right here: a
dead daemon must not wedge the agent.

Set `timeout` to `HookTimeoutSeconds` on every blocking hook, the same number
Claude gets.

### Problem 3: permission modes

Expose four Helios-named modes for Codex. Each maps to a flag pair.

| Helios mode | `--sandbox` | `--ask-for-approval` | What it means |
|---|---|---|---|
| `read-only` | `read-only` | `on-request` | look, do not touch |
| `workspace-write` | `workspace-write` | `on-request` | the default; Helios answers every approval |
| `full-auto` | `workspace-write` | `never` | no approvals, no cards |
| `bypass` | — | — | `--dangerously-bypass-approvals-and-sandbox` |

Default to `workspace-write`. `SkipPermissions` maps to `bypass`.

`ProviderInfo.PermissionModes` already serves this list to the clients, so no
client learns the Codex vocabulary. `resume_id` plus the mode replay together
on wake, the same way Claude's mode does.

State the trap in the UI copy: `full-auto` silences the permission card. On
Claude the permissive mode keeps the phone working. On Codex it stops it.

### Registry changes

Six additions. All of them delete a hardcoded `"claude"`.

1. `RegisterResumeArgs(providerID, func(sessionID, resumeID, mode string) []string)`.
   Replaces the gate at `internal/daemon/daemon.go:79`.
2. `RegisterHookInstaller(providerID, HookInstaller)` with `Install`, `Hash`,
   `Outdated` and `Remove`. Replaces the Claude-only
   `internal/daemon/hooks.go`.
3. `RegisterTranscript(providerID, Locator, Parser)`. Replaces the Claude shape
   in `internal/transcript/reader.go` and the gate at
   `internal/server/api.go:395`.
4. `RegisterDiscovery(providerID, func(*store.Store))`. Moves
   `internal/discovery/claude.go` behind the registry.
5. `RegisterTitler(providerID, ...)`. Replaces the direct
   `claudeprovider.RegenerateTitle` call at `internal/server/api.go:1028`.
6. Grow `ProviderCapabilities`: `PermissionCards`, `Questions`, `Elicitation`,
   `ErrorRetry`, `Subagents`. Clients then key on a capability instead of
   `source == "claude"`.

Plus the plumbing: write `req.Provider` into `Source` at the three creation
sites, drop the source filter at `internal/server/api.go:363`, and give the
reporter the session's provider instead of the constant at
`internal/server/server.go:105`.

### What the codex package registers

```
internal/provider/codex/
  register.go   provider info, session builder, resume args, models, small model
  hooks.go      the handlers
  install.go    writes ~/.codex/hooks.json
  transcript.go rollout jsonl parser
  discovery.go  scans ~/.codex/sessions
```

Hook map:

| Codex event | Helios type | Handler |
|---|---|---|
| `SessionStart` | `codex.session.start` | upsert, bind `resume_id`, clear the trust watcher |
| `SessionEnd` | `codex.session.end` | terminate, forget the terminal |
| `UserPromptSubmit` | `codex.prompt.submit` | status active, record the message, fire `PromptSubmitted` |
| `PreToolUse` | `codex.tool.pre` | status active, report |
| `PostToolUse` | `codex.tool.post` | report, retract the tool's permission card |
| `PermissionRequest` | `codex.permission` | **blocking** — notification, HITL overlay, decision |
| `PreCompact` / `PostCompact` | `codex.compact.pre` / `.post` | status |
| `SubagentStart` / `SubagentStop` | `codex.subagent.start` / `.stop` | subagent rows |
| `Stop` | `codex.stop` | status idle, resolve pending, auto-title |

`Stop` needs care. Codex requires JSON on exit 0, and `{"decision": "block"}`
means *keep going*. Helios must return `{}`. Anything else restarts the turn.

Action handlers: `codex.permission` only. It maps approve/deny onto
`{"decision": {"behavior": "allow"}}` or `deny` with a message.

### What a Codex session will not have

State this plainly. Four cards do not port.

| Missing | Why |
|---|---|
| "Allow, and don't ask again" | `updatedPermissions` is reserved and fails closed |
| The question card | Codex has no `AskUserQuestion` tool |
| The elicitation card | Codex has no `Elicitation` hook event |
| The error-retry card | Codex has no `StopFailure` or `PostToolUseFailure` event |

One more gap has no clean answer yet. Claude fires `Notification` with
`idle_prompt` when a user interrupts with Escape. Helios uses it to move a
session back to idle. Codex has no equivalent, so an interrupted Codex turn
stays `active` until the next hook fires. The options are a terminal-output
watcher, or accepting a stale status. Neither is good. Leave it open.

### Staging

| Stage | Scope | Done when |
|---|---|---|
| 1 | Registry generalisation. No Codex code. | Claude behaves identically and the tests prove it |
| 2 | Codex package: builder, resume, install, lifecycle and tool hooks | a Codex session appears in the list with a live status |
| 3 | `PermissionRequest` on both surfaces | a phone approves a Codex shell command |
| 4 | Transcript, discovery, auto-title, small model | the agent panel renders a Codex conversation |

All four stages ship in one pull request. Stage 1 changes no behaviour, so its
commits carry the Claude tests unchanged as the proof. Keep it in separate
commits inside that PR, so a reviewer can read the refactor apart from the new
code.

---

## Decisions

| Question | Decision |
|---|---|
| Staging | One pull request. Stage 1 in its own commits. |
| Hook install target | `~/.codex/hooks.json`, the same shape and hash check as the Claude install. Fires for every Codex session on the machine, Helios-started or not, which is what Claude does today. |
| Hook trust | Surface the pending `/hooks` approval in the health check. Helios already hashes its hook table, so it can tell the user after an install and after any upgrade that changes the table. |
| Default mode | `workspace-write` + `on-request`. It keeps the phone useful. It asks more often than a Claude session does. |

## Still open

**The `active`-forever gap.** Codex has no `idle_prompt` event, so an
interrupted turn keeps the `active` status until the next hook fires. Accept
the stale status, or build a terminal-output watcher. Decide this in stage 3,
when the permission card makes the cost visible.

**Two facts need a logged-in Codex.** `codex login status` reports *Not logged
in* on this machine, so no turn has run. Both of these gate stage 3:

1. That a blocking `command` hook returns its decision through curl's stdout,
   and that Codex waits for it.
2. The exact payload field names for `SessionStart` and `PermissionRequest`.

Run one Codex turn with a hook that logs its stdin to a file. That settles both
in a minute. Until then, stage 2 can proceed: it needs the argv and the install
path, which are checked.

---

## Appendix — Codex cannot reach Vertex AI

Asked during review: can a Codex session run against Vertex AI, and in
particular against Claude on Vertex?

**No, not without a translating proxy.** Codex 0.150.1 accepts exactly one
`wire_api` value. Measured:

```
wire_api = "telepathy"  →  unknown variant `telepathy`, expected `responses`
wire_api = "anthropic"  →  unknown variant `anthropic`, expected `responses`
wire_api = "chat"       →  `wire_api = "chat"` is no longer supported.
                           How to fix: set `wire_api = "responses"`
```

A custom `model_providers.<id>` therefore has to speak the OpenAI **Responses**
API. Neither Vertex endpoint does:

| Vertex surface | Wire format | Works with Codex |
|---|---|---|
| Claude on Vertex | Anthropic Messages, `:streamRawPredict` | no |
| Gemini OpenAI-compatible endpoint | OpenAI **chat completions** | no — `chat` was removed |

Note the second row. Even the OpenAI-compatible Vertex endpoint is the wrong
OpenAI API. Dropping `chat` support closed the one door that was open.

This has no bearing on Helios. Helios wraps whatever CLI the user has already
authenticated. It does not choose the model backend, and the Codex provider does
not need to care which one is configured.

## Sources

Documentation read 2026-08-29. Every ✓ claim was checked against
`codex-cli 0.150.1` on the same day.

- [Codex hooks reference](https://learn.chatgpt.com/docs/hooks)
- [Codex configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
- [Codex developer commands](https://learn.chatgpt.com/docs/developer-commands.md?surface=cli)
- [Codex advanced configuration](https://developers.openai.com/codex/config-advanced)
- [Session persistence and rollout files](https://codex.danielvaughan.com/2026/04/13/codex-cli-session-persistence-resume-fork-analytics/)
- [Session lifecycle: archive, resume, fork, compact](https://codex.danielvaughan.com/2026/06/05/codex-cli-session-lifecycle-archive-resume-fork-compact-management/)
- [The notify hook](https://backgrind.com/blog/codex-cli-notifications/)
