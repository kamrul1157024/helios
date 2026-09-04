# Approving a Plan From a Phone

## The claim

A plan is not a yes-or-no question, and helios asked it as one.

When Claude finishes planning it calls `ExitPlanMode`. On the wire that is a permission
like any other, so the `PermissionRequest` hook intercepts it, and the phone and the
desktop app got a card saying `ExitPlanMode` with the raw tool input cut at 100 characters
and two buttons: Approve and Deny.

The CLI's own dialog, in the same moment, offers three rows:

```
Claude has written up a plan and is ready to execute. Would you like to proceed?
❯ 1. Yes, and use auto mode
  2. Yes, manually approve edits
  3. Tell Claude what to change
      shift+tab to approve with this feedback
  ctrl+g to edit in Vim · ~/.claude/plans/give-plan-approval-its-own-rows.md
```

Three things were lost on the remote surfaces. The mode the session continues in — the
difference between approving one plan and approving every edit that follows from it. The
ability to disagree in words rather than with a bare no. And the plan itself, which nobody
could read.

## Where we were

| | Mode choice | Disagree with a reason | Plan readable |
| --- | --- | --- | --- |
| CLI's own dialog | yes, two rows | yes | yes |
| Mobile / desktop | no | no | no — raw JSON, 100 chars |

The CLI's row already says yes to all three. This change makes the phone and the desktop
match it, and leaves the terminal alone.

## The terminal keeps the CLI's dialog, and nothing else

Helios paints its own approval box over a session's terminal for every other tool. For a
plan it paints nothing.

The first build of this did paint one — `Ready to code?`, the two mode rows, an answer
field — and it made the terminal worse in two ways at once:

- **The screen became unreadable.** The CLI's dialog is already there. Helios's box is
  composited over it by the host, so the two interleave character by character:
  `Clau│ealreadyiwritten,aonlyntoncommiteady to execute. Would you like to proceed?`
- **Helios could no longer read the screen it had to press.** The same capture feeds
  `answerPlanDialog`, which is how a phone's answer reaches the CLI. With two boxes in it,
  no row matched, and approving from the phone silently did nothing.

There was nothing to gain for the cost. The CLI's dialog offers the same three rows, the
same feedback path, and renders the plan above itself in full. A second copy of a better
original is not worth an unreadable screen.

So `showPermissionPrompt` returns a no-op for `ExitPlanMode`. The person at the keyboard
answers the CLI directly, exactly as they did before helios existed.

## Pressing the row a phone picked

The CLI ignores an `allow` for `ExitPlanMode`. It shows its own dialog anyway and the plan
does not start. That is measured, not assumed — see Evidence. So helios cannot answer a
plan the way it answers `Bash`.

What it does instead: it replies `ask` to get out of the CLI's way, then presses the row
the phone chose. The keystroke has to follow the reply rather than accompany it, because
the CLI does not draw that dialog until the hook has answered. `answerPlanDialog` therefore
runs in a goroutine and polls for up to 15 seconds.

**It presses the row's number, not its highlight.** `provider.ConfirmChoice` — the
screen-scraping picker that answers the workspace trust dialog — walks the highlight onto a
row with arrow keys, and finds the highlight by looking for a cursor mark. Under a plan
dialog sits the composer, carrying a `❯` of its own, and `locateChoice` takes the *lowest*
match. It read the composer as the selection, pressed Up eight times, and gave up. The CLI
numbers these rows and tells the user to press the number; helios presses the same key.

**Nothing is pressed until the CLI's own question is on screen.** `would you like to
proceed` has to appear in the capture first. A numbered list is an ordinary shape for a
terminal to hold, and a digit sent at the wrong moment is a digit typed into the composer.

The row is then found by wording. There is more than one candidate per row, because the
copy belongs to the CLI and has already moved: 2.1.259 offers `Yes, and use auto mode`,
2.1.126 offers `Yes, auto-accept edits`. Helios tries `auto-accept` then `auto mode` for
the first, and `manually approve` for the second. Matching only the newer wording made
approval a no-op on 2.1.126, and the log said no more than that no row was found — so a
miss now prints the screen it looked at.

Failing to find a row is survivable and deliberately quiet: the CLI's dialog is on screen,
fully usable, and the person at the terminal answers it by hand. A renamed row degrades to
that, not to a hung session.

| The card's answer | Hook response | Then |
| --- | --- | --- |
| Yes, and use auto mode | `ask` | press the CLI's `1`; record `auto` |
| Yes, manually approve edits | `ask` | press the CLI's `2`; record `manual` |
| Feedback typed, Send back | `deny`, `message` = a line naming the user, then their words | — |
| Deny | `deny`, message says the plan was rejected and asks Claude to plan again | — |

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
`ExitPlanMode` both now show the two mode rows and a feedback field, and post `plan_choice`
or `feedback` to `/api/notifications/{id}/action`. Approve stays disabled until a row is
picked; typing words relabels Deny to `Send back`.

Both drop the quick rules and the edit field for a plan: the CLI sends no
`permission_suggestions` for this tool, and there is no command to edit.

**The phone names the plan instead of printing it.** A card on a phone is a few hundred
pixels that also have to hold the rows, and the plan went into them as raw markdown in a
scroll box — a small window dragged over a long document, with the buttons that answer it
below. The card now carries the plan's first heading, the file's name, and a **View plan**
button that opens the file in `FileViewerScreen`, which renders the markdown on a whole
screen and is already how any other file is read from the phone. The daemon serves it:
`resolveSafePath` expands `~` and does not confine a read to the session's cwd, so
`~/.claude/plans/….md` opens like a file in the project does. The viewer is rooted at the
plan's own folder rather than at the project, because that is the folder the file is in.

A plan with no `planFilePath` keeps the whole text on the card. There is nothing to open,
so the card is the only copy. The desktop app keeps the plan on the card either way — it
has the width for it, and the card sits beside no viewer to send the reader to.

Either surface builds the same `permissionAnswer`, so the daemon does not care which one
answered: `handlePermissionAction` turns it into a decision, and the decision presses the
CLI's dialog.

## Known limit

A plan answered at the keyboard is invisible to helios. The CLI's dialog is the CLI's, so
the notification stays pending until it is dismissed or the session moves on — helios
learns the mode only when a later hook reports it.
