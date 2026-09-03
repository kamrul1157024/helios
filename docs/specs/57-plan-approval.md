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

Every row but the first says no. This change makes the other two say yes.

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
│ # Plan: give plan approval its own rows                                    │
│                                                                            │
│ ## Context                                                                 │
│ The terminal offers Allow once and Deny. Plan approval needs the two       │
│ mode rows and a way to disagree in words.                                  │
│                                                                            │
│ ## Implementation                                                          │
│ 1. Branch on ExitPlanMode in showPermissionPrompt.                         │
│ 2. Carry the typed text into the deny message.                             │
│ …14 more lines · ~/.claude/plans/give-plan-approval-its-own-rows.md        │
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
│ …14 more lines · ~/.claude/plans/give-plan-approval-its-own-rows.md        │
│                                                                            │
│   Yes, and use auto mode                                                   │
│     Claude edits and runs commands without asking, for the rest of this    │
│     session                                                                │
│   Yes, manually approve edits                                              │
│     Claude asks before each edit, as it does now                           │
│ ❯ Tell Claude what to change                                               │
│   ┌──────────────────────────────────────────────────────────────────────┐ │
│   │ split the plan on newlines, do not reflow it█                        │ │
│   └──────────────────────────────────────────────────────────────────────┘ │
│                                                                            │
│ enter send · esc back to the list                                          │
└────────────────────────────────────────────────────────────────────────────┘
```

The plan is down to one line in the second state. That is not a mockup shortcut:
`RenderOverlay` anchors the box to the bottom of the viewport and clips from the top, so
the three rows the field adds push the plan off. The user reads the plan, then trades it
for the field. Capping the plan harder from the start would hold the height still, at the
cost of showing less plan to the person who has not opened the field at all — and by the
time they type, they have read it.

## What each part maps to

**Title.** `ExitPlanMode` becomes `Ready to code?`, the CLI's own words. A tool name is
right for `Bash`. For this tool it names the mechanism, not the decision.

**Body.** `tool_input.plan` split on newlines into one `Prompt.Body` entry per line. This
is load-bearing rather than cosmetic: `wrapLine` runs `strings.Fields`, so a whole plan
passed as one entry collapses into a single paragraph and the headings vanish.

**The cap line.** `…14 more lines · <planFilePath>` is not decoration. Without a cap the
plan pushes the rows themselves off the screen. `planFilePath` is already in the hook
payload, and it is where the whole plan lives.

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
