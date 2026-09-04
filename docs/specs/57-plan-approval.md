# Approving a Plan From the Terminal

## The claim

A plan is not a yes-or-no question, and helios asked it as one.

When Claude finishes planning it calls `ExitPlanMode`. On the wire that is a permission
like any other, so the `PermissionRequest` hook intercepts it and helios paints its own
box over the CLI's. The box helios painted said `ExitPlanMode`, showed the raw tool input
cut at 100 characters, and offered `Allow once` and `Deny`.

The CLI's own dialog, in the same moment, offers three rows:

```
Claude has written up a plan and is ready to execute. Would you like to proceed?
❯ 1. Yes, and use auto mode
  2. Yes, manually approve edits
  3. Tell Claude what to change
      shift+tab to approve with this feedback
  ctrl+g to edit in Vim · ~/.claude/plans/give-plan-approval-its-own-rows.md
```

Three things were lost. The mode the session continues in — the difference between
approving one plan and approving every edit that follows from it. The ability to disagree
in words rather than with a bare no. And the plan itself, which nobody could read.

## Where we were

| | Mode choice | Disagree with a reason | Plan readable |
| --- | --- | --- | --- |
| CLI's own dialog | yes, two rows | yes | yes |
| Terminal overlay | no | no | no — raw JSON, 100 chars |
| Mobile / desktop | no | no | no |

Every row but the first says no. This change makes the other two say yes on the first two
columns. The third is answered differently on each surface: the phone and the desktop app
have nowhere else to show the plan, so they show it. The terminal already has the CLI's
own rendering of it directly above the box — see **The box does not reprint the plan**.

## What it looks like

Drawn at the overlay's real geometry: 74-column content, the 4-column detail indent, and
the nested field from `inputRows`. Three things ASCII cannot show — the selected row is a
reverse-video bar across the full width, the descriptions are dim, and the footer is dim.

Before:

```
┌─ ExitPlanMode ─────────────────────────────────────────────────────────────┐
│ {"plan":"# Plan: give plan approval its own rows\n\n## Context\nThe        │
│ terminal only offers Allow onc...                                          │
│                                                                            │
│ ❯ Allow once                                                               │
│   Deny                                                                     │
│                                                                            │
│ ↑↓ select · enter confirm · esc cancel                                     │
└────────────────────────────────────────────────────────────────────────────┘
```

After:

```
┌─ Ready to code? ───────────────────────────────────────────────────────────┐
│ ~/.claude/plans/give-plan-approval-its-own-rows.md                         │
│                                                                            │
│ ❯ Yes, and use auto mode                                                   │
│     Claude edits and runs commands without asking, for the rest of this    │
│     session                                                                │
│   Yes, manually approve edits                                              │
│     Claude asks before each edit, as it does now                           │
│   Tell Claude what to change                                               │
│                                                                            │
│ ↑↓ select · enter confirm · esc cancel                                     │
└────────────────────────────────────────────────────────────────────────────┘
```

After Enter on the third row:

```
┌─ Ready to code? ───────────────────────────────────────────────────────────┐
│ ~/.claude/plans/give-plan-approval-its-own-rows.md                         │
│                                                                            │
│   Yes, and use auto mode                                                   │
│     Claude edits and runs commands without asking, for the rest of this    │
│     session                                                                │
│   Yes, manually approve edits                                              │
│     Claude asks before each edit, as it does now                           │
│ ❯ Tell Claude what to change                                               │
│   ┌──────────────────────────────────────────────────────────────────────┐ │
│   │ answer the CLI's dialog too, a hook cannot decide this█              │ │
│   └──────────────────────────────────────────────────────────────────────┘ │
│                                                                            │
│ enter send · esc back to the list                                          │
└────────────────────────────────────────────────────────────────────────────┘
```

The box holds its height when the field opens: nothing above the rows grows with the plan,
so there is nothing for the three rows the field adds to push off. That matters because
`RenderOverlay` anchors the box to the bottom of the viewport and clips from the top.

## The box does not reprint the plan

The first build of this put the plan in the box — `tool_input.plan` split into one
`Prompt.Body` line each, capped at 14 with a `…57 more lines` tail. On screen it was
unreadable, and the reason is not the cap:

- The CLI has **already rendered the plan** into the transcript directly above, with
  headings, colour and tables. The box repainted the same plan a second time, worse.
- The overlay has no markdown renderer. It is box-drawn text assembled in
  `internal/terminal/overlay.go` and written straight to every viewer's terminal, so
  `#`, backticks and pipe-tables arrive as themselves.
- There is nowhere to scroll to the rest, so a plan of any size was cut mid-sentence.
- The height it took came out of the rows, on exactly the short screens where the rows
  are hardest to fit.

So the box says where the plan is written down and leaves the reading to the transcript
above it. The alternative considered and rejected was a full-screen overlay with a
markdown renderer: a new protocol field, host-side scrolling, a renderer dependency, and
at the end of it a copy that covers the CLI's own better one.

This applies to the terminal alone. The phone and the desktop app have no transcript
beside the card, so they still carry the plan; see **The phone and the desktop app get the
same rows**.

## What each part maps to

**Title.** `ExitPlanMode` becomes `Ready to code?`, the CLI's own words. A tool name is
right for `Bash`. For this tool it names the mechanism, not the decision.

**Body.** `planFilePath` from the hook payload, one line, and nothing else. It is the one
thing the transcript above does not say. A payload with no `plan` at all is the exception:
the CLI printed nothing to read either, so the box falls back to `summarizeToolInput`.
A payload with a plan but no path leaves the body empty rather than inventing one.

**The rows.** Two mode rows. The text under each is `Prompt.Details`, which the overlay
already draws and caps at two lines.

**The third row is not an option.** It is `Prompt.AllowText` with
`TextLabel: "Tell Claude what to change"`. Enter opens the field, Enter sends, and the
text becomes the hook's deny message.

**Escape** still denies, and gets a written reason in place of `"Denied via helios"`.

## A plan is the one permission a hook cannot decide

The CLI ignores an `allow` for `ExitPlanMode`. It shows its own dialog anyway and the plan
does not start. That is measured, not assumed — see Evidence. So helios cannot answer a
plan the way it answers `Bash`.

What it does instead: it collects the answer on its own overlay, replies `ask` to get out
of the CLI's way, and then presses the matching row on the CLI's dialog. The keystroke has
to follow the reply rather than accompany it, because the CLI does not draw that dialog
until the hook has answered. `answerPlanDialog` therefore runs in a goroutine and polls for
up to 15 seconds, reusing `provider.ConfirmChoice` — the same screen-scraping row picker
that answers the workspace trust dialog.

The match is a substring of the CLI's row, kept to the words that carry the meaning:
`auto mode` and `manually approve`. Failing to find it is survivable and deliberately
quiet: the CLI's dialog is on screen, fully usable, and the person at the terminal answers
it by hand. A renamed row degrades to that, not to a hung session.

| Row | Hook response | Then |
| --- | --- | --- |
| Yes, and use auto mode | `ask` | Enter on the CLI's `auto mode` row; record `auto` |
| Yes, manually approve edits | `ask` | Enter on the CLI's `manually approve` row; record `manual` |
| Tell Claude what to change | `deny`, `message` = a line naming the user, then their words | — |
| Esc | `deny`, message says the plan was rejected and asks Claude to plan again | — |

A denied tool reaches the model as `Error: <message>`, so the feedback needs someone
attached to it. Bare text there reads as a malfunction rather than as a person talking.

## The mode has to be written down twice

The CLI applies the chosen mode to the running process and nothing else. Helios repeats
`--permission-mode` from the session record on every resume
(`internal/provider/claude/register.go:56`), so a session that left plan mode would wake
back up inside it. Approving a plan therefore also writes `auto` or `manual` to the session
record.

## Evidence

Measured against Claude Code 2.1.259, in a real plan-mode session with a hook that logged
the payload.

The `PermissionRequest` hook does fire for `ExitPlanMode`:

```json
{"permission_mode":"plan","hook_event_name":"PermissionRequest",
 "tool_name":"ExitPlanMode",
 "tool_input":{"plan":"# Plan: Print Hello\n\n## Context\n...",
               "planFilePath":"~/.claude/plans/....md"}}
```

`permission_suggestions` is **absent** for this tool, so the rows cannot be driven from
it. Helios has to name them.

The deny message reached the model:

```
⎿  Error: HELIOS_PROBE_FEEDBACK do not print hello, print goodbye instead
⎿  Denied by PermissionRequest hook
```

Claude then said *"I see the feedback has changed the requirement"*, rewrote the plan, and
called `ExitPlanMode` again. The disagree path already worked end to end. Helios simply
never collected the words.

**An `allow` for a plan is ignored.** The first cut of this change replied `allow` with a
`setMode` permission update and stopped there. Run live, the plan never started and the
CLI's own dialog came up regardless. Four runs in a single-hook environment — helios's own
hooks are installed globally in `~/.claude/settings.json`, so the probe needed an isolated
`HOME` to avoid two `PermissionRequest` hooks answering at once:

| Probe | Result |
| --- | --- |
| `allow` for `ExitPlanMode` | ignored; dialog shown, plan not started |
| `allow` + `setMode` for `ExitPlanMode` | ignored; same |
| `allow` for `Bash` (control) | honoured; the command ran with no dialog |
| `deny` with a message | honoured; Claude re-planned from the words |

The control run is what makes this a statement about the tool rather than about the
response shape: the same JSON, on `Bash`, works.

**The dialog is drawn after the hook replies, not before.** A hook that blocked for 90
seconds saw no dialog appear. So the keystroke cannot be sent alongside the reply.

**The keystroke works, and the mode takes.** Sending `1` to the CLI's dialog started the
plan in six seconds, and the `Write` that followed produced no `PermissionRequest` hook
entry — the chosen mode was in force.

## The phone and the desktop app get the same rows

`mobile/lib/providers/cards.dart` and
`desktop/src/renderer/components/notification-card.tsx` draw the permission card. For
`ExitPlanMode` both now show the plan as prose, the two mode rows, and a feedback field, and
post `plan_choice` or `feedback` to `/api/notifications/{id}/action`. Approve stays disabled
until a row is picked; typing words relabels Deny to `Send back`.

Both drop the quick rules and the edit field for a plan: the CLI sends no
`permission_suggestions` for this tool, and there is no command to edit.

The daemon does not care which surface answered. `handlePermissionAction` and the terminal
build the same `permissionAnswer`, so a plan approved from a phone is pressed onto the CLI's
dialog exactly as one approved at the keyboard is.

## Known limit

An older ptyhost reports overlay protocol < 2. `hitl.Ask` then drops `AllowText`, so the
feedback row disappears and only the two mode rows remain. That is the established
behaviour for the answer field, not a new failure.
