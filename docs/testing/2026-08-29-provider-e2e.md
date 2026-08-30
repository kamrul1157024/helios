# E2E: both providers, 2026-08-29

Ran the provider work against real agents in real repositories, on `main` at
`a08b8c1`. Method in [README.md](README.md).

**Headline: both providers work end to end.** Codex answered a prompt through
Helios, its hooks reached the daemon, its session resumed cold with the
conversation intact. Six defects were found on the way. Three are fixed here.
Three concern custom ports or a wording judgement and are deferred, marked
below.

The suite is fully green for the first time — the two `TestE2EClaude*` failures
that had been red all along were a stale assertion, not a regression.

## What was exercised

Repositories used as working directories: `~/workspace/opal-app/primary-agent`
and `~/workspace/opal-app/hypatia`. Real code, real `AGENTS.md`, real
`.claude/settings.local.json` with 154 pre-approved permissions — which is why
the Claude trust dialog showed its long form.

| | Claude | Codex |
|---|---|---|
| Session created via API, filed under the right provider | pass | pass |
| Launch mode recorded | pass (`auto`) | pass (`workspace-write`) |
| Trust dialog raised as a notification | pass | pass |
| Trust dialog answerable | **was broken** — see BUG-2 | pass |
| Hooks delivered to the daemon | pass | pass |
| Status reaches `idle` | pass | pass |
| Transcript recorded and parsed | pass | pass |
| `resume_id` captured | n/a | pass |
| Cold session resumed with history | not reached | pass |
| Agent completed a turn | **blocked** — see L-1 | pass |
| First run from an empty HOME | not reached | pass, after BUG-5 |

Codex's turn, in full:

```
› say exactly CODEX-E2E-OK
• CODEX-E2E-OK
```

and its hooks, from the daemon log:

```
1 codex.session.start
1 codex.prompt.submit
1 codex.stop
```

Session row afterwards:

```
source=codex status=idle mode=workspace-write
resume_id=01a04f65-928 transcript=True
```

After terminate and resume, the earlier turn was still on screen. That is the
`HELIOS_SESSION` correlation working: Helios minted one id, Codex minted
another, the hook carried both, and the wake used the right one.

## Defects

### BUG-1 — hooks installed against the wrong port. Not fixed, deferred.

**Severity: high.** Silent. Every session sits at `starting` with no error.

`RegisterDefaultProviders` read `DefaultConfig()`, which is the compiled-in
default rather than `config.yaml`. On any machine whose `internal_port` had
been changed, `helios hooks install` wrote every hook URL at 7654 while the
daemon listened elsewhere.

Reproduce, on the build before the fix:

```bash
# config says 18654
HOME=/tmp/hx/home helios hooks install
python3 -c "
import json,re
d=json.load(open('/tmp/hx/home/.claude/settings.json'))
print(sorted(set(re.findall(r'localhost:(\d+)', json.dumps(d['hooks'])))))"
# before: ['7654']   after: ['18654']
```

Observed: zero hooks in the daemon log across three sessions and several
minutes. Reading the configured port instead of the compiled-in one made the
full lifecycle arrive within seconds.

**Left out of the fix branch deliberately.** Anyone on the default port is
unaffected — the two numbers are the same, so the wrong code path and the
right one produce identical output. It only bites someone who has changed
`internal_port`, which is the same case as FINDING-7, and that is being
decided as one thing. If custom ports go away, this disappears with them.

### BUG-2 — approving workspace trust quit the agent. Fixed.

**Severity: high.** Destructive, and squarely on the demo path.

`handleTrustAction` sent a bare Return, on a belief written in the comment
beside it: *"The trust dialog opens with 'Yes, proceed' selected."* Claude has
since made the safe option the default. Captured live:

```
❯ No, exit
  Yes, I trust this folder
```

So tapping **Trust folder** on the phone selected *No, exit*.

Reproduce:

```bash
# A folder Claude has not seen before
curl -sS -X POST localhost:18654/internal/sessions -H 'Content-Type: application/json' \
  -d '{"provider":"claude","cwd":"/path/to/fresh/repo"}'
/tmp/hx/keyprobe "$SOCK"          # ❯ is on "No, exit"
/tmp/hx/keyprobe "$SOCK" down     # ❯ moves to "Yes, I trust this folder"
```

Both providers now read the screen, move the highlight onto the option they
want, verify the move, and only then press Return. Codex's dialog happens to
highlight its affirmative option today; the same code handles both, because a
default belongs to the agent and will change again.

### BUG-3 — the Claude e2e tests failed on a copy change. Fixed.

**Severity: medium.** Two red tests all session, hiding whatever else might
break in that package.

They waited for `"Welcome to Claude Code"`. Onboarding stopped printing it and
opens on the theme picker, so both timed out after 45 seconds each.

Underneath sat a real one: the host declares `TERM=xterm-256color` but said
nothing about `COLORTERM`, so an agent started where `COLORTERM` was unset — a
service manager, a GUI launcher, a CI shell — fell back to 256 colours. Every
Helios viewer renders truecolor, so the agent was drawing duller output than
the terminal showing it could carry. Now declared, for the same reason `TERM`
is.

The snapshot test no longer asserts any Claude wording: it takes a line the
desktop has already scrolled past and requires the late viewer's snapshot to
contain it.

### BUG-5 — Codex's second dialog was invisible. Fixed.

**Severity: high for a first run**, which is exactly what a demo is.

A fresh install shows **two** blocking dialogs back to back. Helios surfaced
only the first, so after answering directory trust the session stopped again
on a prompt no client showed:

```
11 hooks are new or changed.
Hooks can run outside the sandbox after you trust them.
› 1. Review hooks
  2. Trust all and continue
  3. Continue without trusting (hooks won't run)
```

Now raised as its own card, with its own wording, and the action tries both
affirmatives so one card answers either dialog.

Half of this was a second defect in the watcher. `PendingSession.NotifSent`
was a single flag, so once *any* dialog had been surfaced for a session no
further one could be. Even with the pattern added, the hook card never
appeared. It tracks a set of dialog keys now. Verified on a clean rig: card
one, answer it, card two appears, answer it, the session runs.

The health text was wrong about this too. It said to run `/hooks`; Codex asks
on its own at the start of the next session, and `/hooks` is only for a
session already running. Reworded.

### FINDING-6 — "Session error / primary-agent". Not fixed.

**Severity: low.** A `claude.error` notification whose detail is just the
project name, because `lastAPIError` found nothing in the transcript and the
fallback is `sessionContext()`.

It reads as noise. A detail of "primary-agent" tells nobody anything. Left
alone because the right answer is a judgement call: either say "the turn
failed and left no reason", or suppress the notification when there is nothing
to report. Worth deciding before it is seen on stage.

### Not a defect — AGENTS.md appears in the transcript

Codex prepends the project's `AGENTS.md` to the conversation as a user-role
record. It shows up in the history panel above the first real prompt.

Raised here as a defect and **rejected by the owner**: that record is genuinely
part of what the model was sent, and showing it is the transparent answer. A
filter was written and reverted. Recorded so the same argument is not made
twice.

One thread left hanging: the parser still drops a user record that is wholly
one XML-ish element, which is how `<environment_context>` and
`<recommended_plugins>` are hidden. By the same reasoning those should probably
show as well. Left as-is because it predates this run and nobody has objected
to it.

## Limitations of this run

**L-1 — Claude turns did not complete.** The borrowed credentials resolved to
Vertex AI and returned `PERMISSION_DENIED` for `claude-opus-5` in project
`cmp-opal-dev-tbon`. Everything up to the model call was exercised; the model
call was not. Codex's turns completed normally, so the pipeline is proven —
just not through Claude in this rig.

**L-2 — mobile was not driven by hand.** It is 110 tests green with one
pre-existing deprecation, and read in full, but nobody has tapped a card on a
phone. Desktop *was* driven — see below.

**L-3 — one machine.** Linux, bash. The two most recent field bugs on this
project were both macOS-and-zsh specific.

## Clean-rig first run

Repeated from an empty `HOME` after every fix, because a first run is what a
demo is:

```
card 1  Directory trust required
        → answered
card 2  Approve helios hooks
        → answered
› say exactly FIRSTRUN-OK
• FIRSTRUN-OK

1 codex.session.start   1 codex.prompt.submit   1 codex.stop
status=idle mode=workspace-write resume_id=01a04f6f-81e transcript=True
```

## Driven through the desktop app

The fixes above were first verified at the terminal and in unit tests. That
leaves the join between "a notification exists" and "the button answers it"
untested, which is the part a demo actually uses. So the whole first run was
repeated through the real app: Electron under Xvfb, paired to the rig, driven
over CDP.

| Step | Result |
|---|---|
| HUD opens by itself when the session blocks | pass |
| First card renders | `WORKSPACE TRUST / Directory trust required` |
| **Click "Trust folder"** | **agent trusted, not killed** — this is BUG-2 |
| Second card renders, with its own wording | `Approve helios hooks` — this is BUG-5 |
| Click it | hooks approved, agent reaches its prompt |
| Prompt sent | `• DESKTOP-E2E-OK` |
| Hooks | `session.start`, `prompt.submit`, `stop` |
| Session row | `idle`, `workspace-write`, resume id, transcript |
| Session list | shows the session, Idle, `gpt-5.6-sol`, `workspace-write` |

Clicking the button is the only evidence that matters for BUG-2. Before the
fix that click sent a bare Return onto "No, exit".

Pairing was done by hand rather than by changing the app: `cmd/device/create`,
`/api/auth/pair`, `/api/device/activate`, then `hosts.json` written directly.
An earlier attempt added `HELIOS_LOCAL_URL` and `HELIOS_INTERNAL_URL`
overrides to `hosts.ts` and was reverted — a test hook does not belong in
shipped code, and see FINDING-7 for why the underlying limitation should
probably be closed the other way.

## FINDING-7 — custom ports are half-supported. Not fixed.

**Severity: medium.** Nothing is broken today unless someone changes a port,
and then several things are, silently.

The daemon fully supports custom ports: `internal_port` and `public_port` in
`config.yaml`, and `--internal-port` / `--public-port` on `daemon start`. But

- `desktop/src/main/hosts.ts` hardcodes `127.0.0.1:7654` and `:7655`, so
  "pair local" targets the wrong daemon and the app cannot reach the right one
- `HostRecord.url` is an absolute URL stored per paired device, so changing a
  port strands every device already paired against the old one

So the feature exists in the daemon and nowhere else. Two coherent answers:

1. **Remove it.** Fix the port, delete the config keys and the flags. Devices
   can never be stranded because the port can never move.
2. **Finish it.** Have the desktop read the same `config.yaml` the daemon
   reads, and re-derive local host URLs rather than storing them.

The first is smaller and matches how the product is actually used. It is the
recommendation, but it deletes a documented option, so it is the owner's call.

## Suites

| | Result |
|---|---|
| `go test ./...` | green, no exclusions |
| `go vet`, `gofmt` | clean |
| `cmd/apitest` against the rig | 22 passed, 0 failed |
| desktop | 166 passed, typecheck clean, builds |
| mobile | 110 passed, `flutter analyze` 1 pre-existing deprecation |

## Before the demo

1. `make install` and restart the daemon. Everything above was tested on a
   build that is not the one installed at `/usr/local/bin`.
2. Start one Codex session by hand and answer both dialogs. That trusts the
   directory and the hooks once, and neither will interrupt the demo.
3. Decide FINDING-6.
4. If anything must not fail live, do it with Codex. It is the path with a
   completed turn behind it here.
