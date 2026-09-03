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
❯ 1. Yes, auto-accept edits
  2. Yes, manually approve edits
  3. Tell Claude what to change
      shift+tab to approve with this feedback
  ctrl+g to edit in Vim · ~/.claude/plans/give-plan-approval-its-own-rows.md
```

Three things were lost. The mode the session continues in — the difference between
approving one plan and approving every edit that follows from it. The ability to disagree
in words rather than with a bare no. And the plan itself, which nobody could read.

## Where we are

| | Mode choice | Disagree with a reason | Plan readable |
| --- | --- | --- | --- |
| CLI's own dialog | yes, two rows | yes | yes |
| Terminal overlay | no | no | no — raw JSON, 100 chars |
| Mobile / desktop | no | no | no |

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
│ ❯ Yes, auto-accept edits                                                   │
│     Claude edits files without asking for the rest of this session         │
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
│   Yes, auto-accept edits                                                   │
│     Claude edits files without asking for the rest of this session         │
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

**The rows.** Two `setMode` rows. The text under each is `Prompt.Details`, which the
overlay already draws and caps at two lines.

**The third row is not an option.** It is `Prompt.AllowText` with
`TextLabel: "Tell Claude what to change"`. Enter opens the field, Enter sends, and the
text becomes the hook's deny message.

**Escape** still denies, and gets a written reason in place of `"Denied via helios"`.

## Wire effects

| Row | Hook response |
| --- | --- |
| Yes, auto-accept edits | `allow` + `updatedPermissions: [{"type":"setMode","destination":"session","mode":"acceptEdits"}]` |
| Yes, manually approve edits | `allow` + `setMode` `default` |
| Tell Claude what to change | `deny`, `message` = a line naming the user, then their words |
| Esc | `deny`, message says the plan was rejected and asks Claude to plan again |

A denied tool reaches the model as `Error: <message>`, so the feedback needs someone
attached to it. Bare text there reads as a malfunction rather than as a person talking.

## The mode has to be written down twice

`setMode` reaches the running CLI process and nothing else. Helios repeats
`--permission-mode` from the session record on every resume
(`internal/provider/claude/register.go:56`), so a session that left plan mode would wake
back up inside it. Approving a plan therefore also writes the mode to the session record.

The two vocabularies differ by one name: `setMode` calls the ask-each-time mode `default`,
and `--permission-mode` calls it `manual`. `heliosMode` is that translation and refuses
any mode helios cannot launch with.

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

## Not in this change

The mobile and desktop cards keep their Approve / Deny pair
(`mobile/lib/providers/cards.dart:267`). The daemon side is ready for them: the answer a
surface posts to `/api/notifications/{id}/action` now carries `permission_updates` and
`feedback`, so a phone can pick a mode or send a plan back in words as soon as the card
draws the rows. That is the follow-up.

## Known limit

An older ptyhost reports overlay protocol < 2. `hitl.Ask` then drops `AllowText`, so the
feedback row disappears and only the two mode rows remain. That is the established
behaviour for the answer field, not a new failure.
