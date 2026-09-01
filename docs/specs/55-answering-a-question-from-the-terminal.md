# Answering a Question From the Terminal

## The claim

The terminal should answer any question the desktop can answer. Today it answers the
easiest third of them: it shows the option labels and nothing else, it cannot take a typed
answer when none of the options fit, and it cannot select two options when the question
asks for two. Every one of those is a rendering gap, not a protocol gap — the daemon
already carries the data and already accepts the answers.

## Where we are

| | Option descriptions | Typed answer | Multi-select |
| --- | --- | --- | --- |
| Terminal overlay | dropped in `optionLabels()`, `claude/question.go:183` | none | none |
| Desktop | drawn, `desktop/src/renderer/components/notification-card.tsx:194` | none | none |
| Flutter | dropped, reads `opt['label']` only, `mobile/lib/providers/cards.dart:400` | none | reads `multiSelect` at `cards.dart:384`, then ignores it |

What already exists on the daemon side, unused:

- `questionOption.Description` is parsed (`claude/question.go:20`) and never read again.
- `questionAnswer.Text` is defined (`claude/question.go:47`) and rendered into the reason
  Claude reads (`claude/question.go:230`). No surface ever sets it.
- `Selections` is a flat list of `{question_index, option_index}` (`claude/question.go:46`),
  so two entries sharing a question index already *is* a multi-select answer. Nothing needs
  to change in the answer payload.

What is genuinely missing: `questionSpec` (`claude/question.go:24-28`) does not parse
`multiSelect` at all.

## What it looks like

Single-select, every description drawn:

```
┌─ Next step ────────────────────────────────────────────────────────┐
│ Unit repro done. What next on PR 141?                              │
│                                                                    │
│ ❯ Live repro in TUI                                                │
│      Start a real session with an alt-screen agent, trigger        │
│      AskUserQuestion, press arrows. Confirms end-to-end.           │
│   Code review the PR                                               │
│      Read the full diff and report issues: SS3 edge cases,         │
│      skip-to-final-byte logic, test coverage gaps.                 │
│   Other…                                                           │
│                                                                    │
│ ↑↓ select · enter confirm · esc cancel                             │
└────────────────────────────────────────────────────────────────────┘
```

Multi-select:

```
┌─ Which checks to run  (2/3) ───────────────────────────────────────┐
│ Pick every check that must pass before merge.                      │
│                                                                    │
│   [x] Unit tests                                                   │
│       go test ./... across the daemon. Two failures are            │
│       already present on main and stay expected.                   │
│ ❯ [x] Race detector                                                │
│       go test -race on internal/terminal. Slow, but the            │
│       overlay redraw path is where the races have been.            │
│   [ ] Flutter analyze                                              │
│       dart analyze on mobile/. Nothing here touches Dart.          │
│   [ ] Other…                                                       │
│                                                                    │
│ space toggle · ↑↓ move · enter confirm · esc cancel                │
└────────────────────────────────────────────────────────────────────┘
```

Editing a typed answer:

```
┌─ Next step ────────────────────────────────────────────────────────┐
│ Unit repro done. What next on PR 141?                              │
│                                                                    │
│   Live repro in TUI                                                │
│   Code review the PR                                               │
│ ❯ Other…                                                           │
│   ┌──────────────────────────────────────────────────────┐         │
│   │ rebase onto main first, then re-run the SS3 test█    │         │
│   └──────────────────────────────────────────────────────┘         │
│                                                                    │
│ enter send · esc back to the list                                  │
└────────────────────────────────────────────────────────────────────┘
```

Descriptions indent four columns, not two. At two they sit in the same column as an
unselected label and the list reads as one paragraph. They are dim and capped at two
wrapped lines: the box is anchored to the bottom and clips from the *top*
(`terminal/overlay.go:80`), so an uncapped description pushes the question itself off
screen. Four options with two lines each is 19 rows, which fits an 80×24 terminal.

Every option keeps its description at all times. Expanding only the highlighted row halves
the height but makes the box grow and shrink under the cursor, and because it is
bottom-anchored the top edge would walk up and down the screen while the user arrows.

## The change

### The overlay gains three optional fields

`terminal.Overlay` (`terminal/overlay.go:16-27`) is marshalled by the daemon
(`terminal/client.go:95-99`) and unmarshalled by `helios ptyhost`
(`terminal/host.go:820-825`). A ptyhost from an earlier build can still be running after an
upgrade. So every field is additive and `omitempty`, and `Options []string` keeps its exact
shape and meaning:

```go
Details []string      `json:"details,omitempty"` // index-aligned with Options
Checked []bool        `json:"checked,omitempty"` // non-nil renders checkboxes
Input   *OverlayInput `json:"input,omitempty"`   // the typed-answer row
```

```go
type OverlayInput struct {
    Label  string `json:"label"`  // the row that opens the field, e.g. "Other…"
    Value  string `json:"value"`  // what has been typed so far
    Active bool   `json:"active"` // the field has the keyboard
}
```

An old ptyhost ignores all three and paints today's list from `Options`.

### Capability, because two of the three do not degrade

Descriptions degrade silently and correctly. The other two do not. An old host that ignores
`Checked` draws a multi-select question as a single-select list, and Enter answers with one
option — silently wrong. An old host that ignores `Input` leaves the user typing into a
buffer they cannot see.

So the daemon asks before it offers. `hitl.Overlays` (`hitl/hitl.go:51-54`) gains a third
method, defined at the consumer as the other two are:

```go
OverlayProtocol(sessionID string) int
```

It reads through to the mirror's cached sidecar protocol (`terminal/mirror.go:244-253`),
the same value `SendText` already gates `FramePaste` on (`terminal/mirror.go:232-242`).
`HostProtocol` goes to 2 (`terminal/paths.go:39`).

The three do not fail the same way, so they are not gated the same way.

| On a host reporting protocol 1 | |
| --- | --- |
| Descriptions | Drawn as labels alone. Nothing is lost that the user needed. |
| The answer field | Dropped. The choices are still answerable, and what the user wanted to write is still answerable on the phone. |
| Checkboxes | The whole question goes to the phone. Drawn as a single-select list it would collect an answer the user did not give. |
| A question with no choices | The whole question goes to the phone. There is no reduced version of a prompt that was only ever a field. |

Suppressing every question on an old host would be the easy rule and the wrong one: since
questions always offer the field, it would leave a terminal that used to show the options
showing nothing at all. Sending the whole set to the phone is what a free-text question
already did (`claude/question.go:72`), and it stays reserved for the cases with no
smaller thing to draw.

### The prompt gains the state, and the state machine gains a mode

`hitl.Prompt` (`hitl/hitl.go:32-39`):

```go
Details   []string // index-aligned with Choices, optional
Multi     bool     // space toggles, enter submits the set
AllowText bool     // append the Other… row
TextLabel string   // defaults to "Other…"
```

`live` (`hitl/hitl.go:66-79`) gains `checked []bool`, `text string` and `editing bool`
under the same mutex that guards `selected`.

`hitl.Answer` (`hitl/hitl.go:42-45`) gains two fields beside `Index`:

```go
Indexes []int  // set when Prompt.Multi
Text    string // set when the user typed instead of choosing
```

`Index` keeps its meaning for the single-choice case, so the three prompt builders that do
not opt in (`claude/hooks.go:232`, `claude/hooks.go:522`, `codex/hooks.go:428`) are
untouched. `Cancelled()` becomes "nothing was chosen and nothing was typed" rather than
`Index < 0` alone, because a typed answer legitimately carries no index.

### Two key modes

`decodeKeys` (`hitl/keys.go:33`) today treats every printable byte as a command: `j` and
`k` move, `1`-`9` jump the highlight, ESC cancels. A text buffer needs those same bytes as
literal characters, so the decoder takes the mode:

| | List mode | Edit mode |
| --- | --- | --- |
| `↑ ↓ j k` | move | move (leaves the field first) |
| `1`-`9` | jump the highlight; toggle when `Multi` | literal digits |
| space | toggle when `Multi` | literal space |
| printable | ignored | appended |
| backspace / DEL | ignored | delete one rune |
| Ctrl-U / Ctrl-W | ignored | clear the line / delete a word |
| Enter | confirm | send the typed answer |
| ESC | cancel the prompt | leave the field, stay in the prompt |
| Ctrl-C | cancel | cancel |
| bracketed paste | skipped whole | inserted literally |

ESC is deliberately overloaded rather than spending a second key on the exit. The footer
carries it: it reads `enter send · esc back to the list` while the field is active, so the
user is told the first ESC does not throw the question away. Bracketed paste stops being
skipped as an unknown CSI (`hitl/keys.go:84-93`) and becomes an insert while editing.

The buffer lives in the daemon, not the host. `HandleInput` already re-sends the whole
overlay on every highlight move (`hitl/hitl.go:177-181`), so a redraw per keystroke is the
existing path. It follows that every viewer of the session watches the answer being typed,
which is correct for a surface where the first answer wins.

### The question provider fills it in

In `claude/question.go`:

- `questionSpec` gains `MultiSelect bool \`json:"multiSelect"\``.
- `optionDescriptions(q)` beside `optionLabels()` (`:183`), returning nil when every
  description is blank so `details` stays out of the JSON.
- `ask()` (`:101`) sets `Details`, `Multi: q.MultiSelect` and `AllowText: true`. The CLI
  always permits a free-text answer, so the `Other…` row is not conditional.
- `answered()` (`:125`) writes one `selection` per chosen index and puts a typed answer in
  `questionAnswer.Text`. A set shares one text field on the wire, so an answer past the
  first is prefixed with the question it belongs to.
- The bail-out for optionless questions (`:72`) goes away. It is no longer a special case:
  a question with no choices is one whose only row is the field, and the capability gate
  already sends it to the phone when the host cannot draw that. `Ask` opens the field for
  it immediately, since there is nothing else on the row to pick.

### The other two surfaces

Desktop (`desktop/src/renderer/components/notification-card.tsx`) already draws
descriptions. It gains checkboxes when `multiSelect` is set, and an "Other" field that
posts `text`.

Flutter (`mobile/lib/providers/cards.dart`) gains all three. `_selections` becomes a set
per question rather than `Map<int, int>` (`:313`); the submit body at `:322-323` still
emits a flat `{question_index, option_index}` list, one entry per checked option.

## What we are not doing

- **Elicitation forms.** `claude/hooks.go:522` still sends the user to the app. One line of
  text is not a JSON schema, and pretending otherwise collects the wrong answer.
- **Permission suggestions in the overlay.** `PermissionSuggestions` stays desktop-only.
- **All four questions at once.** They still paint one at a time, titled `Header (i/N)`
  (`claude/question.go:112`).
- **Wrapping long labels.** `ansi.Truncate(opt, width-2, "…")` (`terminal/overlay.go:131`)
  stays. Descriptions wrap; labels are meant to be short.
- **Multi-line typed answers.** One line. Enter sends, so there is no key left to mean
  newline without stealing another.
- **Readline.** Backspace, Ctrl-U and Ctrl-W. No cursor movement inside the line, no
  history, no completion.

## Tests

`internal/terminal/overlay_test.go`, in the style of
`TestRenderOverlayDrawsTitleBodyAndOptions`:

- A description is drawn under its own option, indented four, dim, after the label it
  belongs to.
- A description longer than two wrapped lines is cut to two and ends in `…`.
- `Checked` renders `[x]` and `[ ]` and leaves the highlight bar intact on a checked row.
- `Input` renders the label, the framed value, and swaps the footer when `Active`.
- An `Overlay` carrying none of the three marshals to JSON with no `details`, `checked` or
  `input` key. This is the promise made to an older ptyhost, so it is asserted on the bytes,
  not on the render.
- Rows stay uniform width with all three present, extending
  `TestOverlayBoxRowsAreUniformWidth`.
- `TestOverlayFrameRoundTrip` carries all three through the frame.

`internal/hitl/keys_test.go`:

- Every row of the mode table above, both modes.
- ESC in edit mode does not produce a cancel; ESC in list mode still does.
- A bracketed paste inserts its payload in edit mode and is skipped whole in list mode.
- Coalesced input mixing a paste and an Enter is decoded in order.

`internal/hitl/hitl_test.go`:

- Space toggles under `Multi` and does nothing without it.
- Enter under `Multi` answers with every checked index; with none checked it is a cancel.
- Typing and Enter answers with `Text` set and no index.
- A prompt whose host reports protocol 1 and which needs text or checkboxes never calls
  `SetOverlay`.
- A prompt with none of the new fields paints an overlay with all three fields nil, so the
  permission and elicitation prompts are byte-identical to today.

`internal/provider/claude/question_test.go`:

- Descriptions reach `Prompt.Details`; a question with none produces nil.
- `multiSelect: true` reaches `Prompt.Multi`.
- Two checked options on one question produce two `selection` entries sharing a question
  index, and `questionReason` names both.
- A typed answer reaches `questionAnswer.Text` and appears in the reason.
