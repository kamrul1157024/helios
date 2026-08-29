# E2E: both providers, 2026-08-29

Ran the provider work against real agents in real repositories, on `main` at
`a08b8c1`. Method in [README.md](README.md).

**Headline: both providers work end to end.** Codex answered a prompt through
Helios, its hooks reached the daemon, its session resumed cold with the
conversation intact. Six defects were found on the way, five of them fixed
here; the sixth is a judgement call left open below.

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
| Transcript recorded and parsed | pass | pass, after BUG-4 |
| `resume_id` captured | n/a | pass |
| Cold session resumed with history | not reached | pass |
| Agent completed a turn | **blocked** — see L-1 | pass |

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

### BUG-1 — hooks installed against the wrong port. Fixed.

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
minutes. After the fix, the full lifecycle arrived within seconds.

Anyone running on default ports was never affected, which is why it survived.

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

### BUG-4 — AGENTS.md rendered as something the user said. Fixed.

**Severity: medium.** Cosmetic but bad in a demo — the history panel opens
with the user apparently reciting a config file.

Codex prepends the project's `AGENTS.md` as a user-role record. The existing
filter caught anything wholly wrapped in an XML-ish element; this is a
Markdown heading and went straight through.

Reproduce: run any Codex session in a repo with an `AGENTS.md`, then read the
transcript. Before, three messages, the first 4 KB of config. After, two:

```
[  8] user        "say exactly CODEX-E2E-OK"
[ 11] assistant   "CODEX-E2E-OK"
```

Matched by literal prefix rather than generalised, deliberately: hiding
something the user typed is much worse than showing something they did not.

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

## Limitations of this run

**L-1 — Claude turns did not complete.** The borrowed credentials resolved to
Vertex AI and returned `PERMISSION_DENIED` for `claude-opus-5` in project
`cmp-opal-dev-tbon`. Everything up to the model call was exercised; the model
call was not. Codex's turns completed normally, so the pipeline is proven —
just not through Claude in this rig.

**L-2 — no client was driven by hand.** Desktop is 166 tests green, typechecks
and builds; mobile is 110 green with one pre-existing deprecation. Nobody has
tapped an approval on a phone.

**L-3 — one machine.** Linux, bash. The two most recent field bugs on this
project were both macOS-and-zsh specific.

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
