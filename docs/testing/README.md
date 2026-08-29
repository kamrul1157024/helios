# Testing Helios end to end

How to exercise a real Helios against real agents, and what each run is
allowed to conclude.

Test *reports* live beside this file, dated. This document is the method.

## Why a rig rather than the developer's own daemon

A live daemon owns the developer's sessions — hundreds of them, some running.
Restarting it to test a build is disruptive, and a test that writes to
`~/.claude` or `~/.codex` can lose real work. Every run below uses:

- a **separate `HOME`** under `/tmp`, so agent config and session history are
  the run's own
- a **separate daemon** on spare ports, so the developer's daemon is untouched
- **borrowed credentials only** — `auth.json` and `.credentials.json` copied
  in, nothing copied out

The cost is that the rig is not the developer's machine, and some failures
only happen on theirs. Note which is which when reporting.

## Standing up a rig

```bash
# 1. Build the binary under test
go build -o /tmp/hx/helios ./cmd/helios

# 2. An isolated HOME with borrowed credentials
export E2E=/tmp/hx/home
mkdir -p $E2E/.codex $E2E/.claude
cp ~/.codex/auth.json          $E2E/.codex/
cp ~/.claude/.credentials.json $E2E/.claude/

# 3. Claude skips onboarding only if it thinks it has done it. Without this it
#    sits on the theme picker for ever and no session ever starts.
python3 - <<'PY'
import json
src = json.load(open('/home/YOU/.claude.json'))
keep = {k: src[k] for k in ('hasCompletedOnboarding', 'lastOnboardingVersion') if k in src}
json.dump(keep, open('/tmp/hx/home/.claude.json', 'w'))
PY

# 4. Ports. The CLI reads these from config, and several commands are wrong
#    without it — hooks get installed against the default port.
cat > $E2E/.helios/config.yaml <<'YAML'
server:
    bind: localhost
    internal_port: 18654
    public_port: 18655
auth:
    enabled: true
tunnel:
    provider: none
YAML

# 5. Daemon, then hooks. This order: hooks are written from the config.
HOME=$E2E nohup /tmp/hx/helios daemon start \
  --internal-port 18654 --public-port 18655 > /tmp/hx/daemon.log 2>&1 &
sleep 7
HOME=$E2E /tmp/hx/helios hooks install
```

Confirm before testing anything else — a wrong port here silently invalidates
every later result:

```bash
python3 -c "
import json,re
d=json.load(open('/tmp/hx/home/.claude/settings.json'))
print(sorted(set(re.findall(r'localhost:(\d+)', json.dumps(d['hooks'])))))"
# → ['18654']
```

## Driving a session

Agents are full-screen TUIs. `helios attach` under a pty does not reliably
deliver keys, so drive the session's socket directly — the same path the
daemon uses, so what it sees is what the daemon sees.

`cmd/keyprobe` is not committed; recreate it when needed (see the dated
reports for the source). It dials a session socket, sends named keys, and
prints the rendered screen:

```bash
SOCK=$(python3 -c "
import json,glob
for f in glob.glob('/tmp/hx/home/.helios/run/*.json'):
    d=json.load(open(f))
    if d['session_id']=='$SID': print(d['socket'])")

/tmp/hx/keyprobe "$SOCK"                      # just look
/tmp/hx/keyprobe "$SOCK" down enter           # answer a dialog
/tmp/hx/keyprobe "$SOCK" "a prompt" enter     # type
```

## What a first run has to get past

Both agents block on dialogs before they will start a session. A run that
skips them measures nothing.

| Agent | Dialog | Default | Notes |
|---|---|---|---|
| Claude | workspace trust | **No, exit** | Only for folders it has not seen. A repo with `.claude/settings.local.json` pre-approvals gets a longer warning. |
| Codex | directory trust | Yes, continue | Applies to the git root, not the subdirectory. |
| Codex | hook trust | Review hooks | Only when hooks are new or changed. "Trust all and continue" is the one that works. |

Claude's default is the destructive option. Anything that answers a dialog by
pressing Return without looking will quit the agent.

## Reading the results

```bash
# Hooks actually delivered — the single best signal that the pipeline works
grep -oE "hook: received [a-z.]+" /tmp/hx/home/.helios/logs/daemon.log |
  sort | uniq -c

# Session state
curl -sS http://127.0.0.1:18654/internal/sessions | python3 -m json.tool

# Notifications, including the ones raised by the screen watcher
go run ./cmd/dbdump /tmp/hx/home/.helios/helios.db   # recreate; see reports
```

A session that reaches `idle` with a `transcript_path` and a `resume_id`
(Codex) has exercised the whole chain: launch, hooks, correlation, state.

## What this cannot tell you

State it in the report rather than letting a reader assume otherwise.

- **Agent turns may not complete.** Borrowed credentials can resolve to a
  backend the rig has no access to; Claude's went to Vertex and returned
  `PERMISSION_DENIED`. Hook plumbing, session lifecycle, dialogs and
  transcripts are all still testable. Model output is not.
- **The clients are not driven.** Desktop and mobile are covered by their own
  suites and by reading; nobody has tapped a card on a phone.
- **One machine, one shell.** Login-shell PATH, zsh versus bash, and macOS
  versus Linux have all produced real differences this project has had to fix.

## Tearing down

```bash
HOME=/tmp/hx/home /tmp/hx/helios daemon stop
rm -rf /tmp/hx
```

Check the developer's own daemon is still up afterwards:

```bash
curl -sS http://127.0.0.1:7654/internal/health
```
