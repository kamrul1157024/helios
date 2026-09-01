---
name: helios
description: Drive Helios from the command line — schedules (cron, one-shot, monitors, chains), sessions, and the daemon. Use when asked to schedule work, watch for a condition, chain jobs, or inspect what Helios is running. Triggers on "schedule", "every morning", "when the build breaks", "after that job", "monitor", "helios".
---

# Helios from the command line

Helios runs AI coding agents in terminals it owns. `helios` is on the PATH of every
session it starts, so you can drive it from inside one.

This skill is about **schedules**: a saved prompt with something that decides when it
runs. The daemon fires it and the run is an ordinary session — same transcript, same
notifications — just marked with the schedule that started it.

## Before anything else

```sh
helios schedule list          # what already exists
helios schedule help          # the full flag list, which is the truth
```

Read the list first. Names are unique, and an edit is better than a second schedule
that does nearly the same thing.

## The four kinds

A schedule fires on exactly one of these. The flag you pass decides which.

| Kind | Flag | Fires |
|---|---|---|
| timer | `--cron "<expr>"` | on a cron expression |
| once | `--at <RFC3339>` | at one moment, then it is done |
| monitor | `--check "<cmd>"` | when a check matches; the cron says how often to look |
| after | `--after <name>` | when another job finishes |

## Creating one

```sh
helios schedule add "<prompt>" --name <name> [flags]
```

Five-field cron, local time: `minute hour day-of-month month day-of-week`.

```sh
# Every weekday at nine, in a checkout.
helios schedule add "triage the overnight PRs and summarise what needs a human" \
  --name morning-triage --cron "0 9 * * 1-5" --cwd ~/work/app

# Once, tonight. Then it is done and the row stays with its result.
helios schedule add "run the migration and report what changed" \
  --name tonight-migrate --at 2026-03-02T22:00:00Z --cwd ~/work/app
```

`--cwd` is optional. Leave it out for work that is not about a directory — reviewing
pull requests, reading an inbox — and the agent starts in the home directory with all
its usual tools.

## Monitors: a check decides

The cron says how often to *look*. The check decides whether there is anything to do.

**Two rules and no third:**

- **With `--match`, the pattern over the output decides** and the exit code is ignored.
  Use this when the command reports absence by failing: `gh pr list` prints `[]` and
  exits 0, `grep` exits 1 on a clean log.
- **Without it, a non-zero exit is the match.** The `test` convention: the command
  asserts things are fine, and failing is the news.

The check's output reaches the agent through `{{output}}` in the prompt. A prompt
without the placeholder simply does not get the output.

```sh
# Fires when the tests fail, and hands the failure to the agent.
helios schedule add "The tests are failing:\n\n{{output}}\n\nFind the cause and fix it." \
  --name build-watch --cron "*/15 * * * *" --cwd ~/work/app \
  --check "make test 2>&1"

# Fires when a PR is waiting. gh exits 0 either way, so the pattern decides.
helios schedule add "Pull requests are waiting on me:\n\n{{output}}\n\nReview each one." \
  --name pr-review --cron "0 */2 * * 1-5" \
  --check "gh pr list --search 'review-requested:@me draft:false' --json number,title" \
  --match '"number"'

# A script instead of a command, run directly by its shebang.
helios schedule add "The queue is backing up:\n\n{{output}}" \
  --name queue-watch --cron "*/5 * * * *" \
  --check-file ~/checks/queue_depth.py --check-arg --threshold --check-arg 5000
```

A matching check fires **every time it matches**, so the check must clear on its own.
`helios schedule list` shows how many times each one fired today, which is how a
runaway is spotted.

Test a monitor before trusting it:

```sh
helios schedule check build-watch    # runs the check once, reports, fires nothing
```

## Chains: one job after another

A job with `--after` has no clock. Its parent finishing is its only trigger, and
**a job is done when its session goes idle** — the agent finished its turn.

`--after-when success` (the default) stops the chain where it broke.
`--after-when any` runs whatever happened.

```sh
helios schedule add "run the migration"        --name nightly-migrate --at 2026-03-02T22:00:00Z --cwd ~/work/app
helios schedule add "test feature one"         --name test-one   --after nightly-migrate --cwd ~/work/app
helios schedule add "test feature two"         --name test-two   --after test-one --cwd ~/work/app
helios schedule add "write up what broke"      --name write-up   --after test-two --after-when any --cwd ~/work/app
```

Siblings — two jobs with the same parent — start together. A loop is refused when you
try to save it.

## Everything else

```sh
helios schedule edit <name> [same flags]   # only what you pass changes
helios schedule run <name>                 # fire now, out of turn
helios schedule enable <name>
helios schedule disable <name>             # pause, keeping its place in the clock
helios schedule rm <name>                  # deletes it; anything following it is paused
helios schedule logs <name> [--follow]     # its checks and fires, with output
```

## Reading a failure

Three places, and they answer different questions:

- `helios schedule list` — is it healthy. Shows `✗ ×3` for a streak, `! missed`,
  `⊘ blocked`, and when it next fires.
- `helios schedule logs <name>` — what its checks printed and what each fire did.
- `helios logs --daemon` — one line per decision the loop made.

A schedule that fails three times in a row pauses itself and says so.

## Rules the daemon enforces at save

It refuses these rather than letting you find them at 3am, and the message says which:

- A cron that can never match (`0 0 30 2 *` — February the thirtieth).
- `{{output}}` in a schedule that has no check to produce it.
- A check file that does not exist or is not executable.
- A link that would close a loop.
- A name that is already taken.

## Sessions, for context

```sh
helios sessions --list          # what is running
helios new "<prompt>" --cwd <dir>
helios attach <session-id>
```

Sessions a schedule started are kept out of the ordinary list — they are the runs of
that schedule, and the apps show them under it.
