package terminal

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Overlay is a modal helios paints over a session's terminal.
//
// The host renders it; whoever sets it only says what it contains. Keeping
// geometry off the wire means a resize is handled where the size is already
// known, with no round trip back to the setter.
type Overlay struct {
	// Title labels the box, e.g. the tool name or the question header.
	Title string `json:"title"`
	// Body is free text shown above the options.
	Body []string `json:"body,omitempty"`
	// Options are the choices. An empty list renders an informational box.
	Options []string `json:"options,omitempty"`
	// Details are per-option descriptions, index-aligned with Options. A short
	// slice or a blank entry means that option has none.
	Details []string `json:"details,omitempty"`
	// Checked ticks the options of a multi-select prompt, index-aligned with
	// Options. A nil slice renders a single-select list with no checkboxes.
	Checked []bool `json:"checked,omitempty"`
	// Input is the typed-answer row, drawn after the options. Nil leaves it off.
	Input *OverlayInput `json:"input,omitempty"`
	// Selected indexes Options, or equals len(Options) to mean the Input row.
	// Out-of-range values highlight nothing.
	Selected int `json:"selected"`
	// Footer is the key hint line.
	Footer string `json:"footer,omitempty"`
}

// OverlayInput is the row that collects an answer none of the options carry.
//
// Details, Checked and Input are all additive and omitted when empty: a host
// from an older build ignores them and paints the option labels alone, which is
// what it did before they existed.
type OverlayInput struct {
	// Label is the row that opens the field, e.g. "Other…".
	Label string `json:"label"`
	// Value is what has been typed so far.
	Value string `json:"value"`
	// Active reports that the field holds the keyboard.
	Active bool `json:"active"`
}

// ParseOverlay decodes a FrameOverlaySet payload.
func ParseOverlay(payload []byte) (Overlay, error) {
	var o Overlay
	if err := json.Unmarshal(payload, &o); err != nil {
		return Overlay{}, fmt.Errorf("decode overlay: %w", err)
	}
	return o, nil
}

// overlayMaxWidth caps the box so a wide terminal gets a readable column
// instead of one sentence stretched across it.
const overlayMaxWidth = 96

// overlayMinWidth is the narrowest box worth drawing. Below this the terminal
// is too small to overlay anything legible, and RenderOverlay returns nothing.
const overlayMinWidth = 24

// ANSI used by the overlay. Written literally rather than through lipgloss:
// these bytes go to viewers' terminals over the wire, so they must not depend
// on the host's own colour-profile detection.
const (
	sgrReset   = "\x1b[m"
	sgrBold    = "\x1b[1m"
	sgrDim     = "\x1b[2m"
	sgrReverse = "\x1b[7m"

	cursorSave    = "\x1b7"
	cursorRestore = "\x1b8"
	cursorHide    = "\x1b[?25l"
	cursorShow    = "\x1b[?25h"
)

// RenderOverlay paints o anchored to the bottom of a cols×rows viewport.
//
// The result is self-contained — it saves the cursor, draws, and restores — so
// it can be re-stamped after any PTY output without disturbing the application
// underneath. It returns nil when there is no room to draw.
func RenderOverlay(o Overlay, cols, rows int) []byte {
	if cols < overlayMinWidth+4 || rows < 3 {
		return nil
	}

	width := cols - 4
	if width > overlayMaxWidth {
		width = overlayMaxWidth
	}
	lines := overlayBox(o, width)

	// Anchor to the bottom, clipping from the top when the box is taller than
	// the viewport. The options and the key hint live at the bottom, so losing
	// the top of the body is the least destructive way to not fit.
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	startRow := rows - len(lines) + 1
	startCol := (cols-(width+4))/2 + 1
	if startCol < 1 {
		startCol = 1
	}

	var sb strings.Builder
	sb.WriteString(cursorSave)
	sb.WriteString(cursorHide)
	for i, line := range lines {
		fmt.Fprintf(&sb, "\x1b[%d;%dH", startRow+i, startCol)
		sb.WriteString(line)
	}
	sb.WriteString(sgrReset)
	sb.WriteString(cursorRestore)
	return []byte(sb.String())
}

// ClearOverlayBytes undoes the cursor state RenderOverlay set. The caller must
// follow it with a repaint: the cells the box covered still hold its glyphs.
func ClearOverlayBytes() []byte { return []byte(cursorShow) }

// overlayBox lays the modal out as full box-drawn lines, content width wide.
func overlayBox(o Overlay, width int) []string {
	inner := width + 2

	title := o.Title
	if title == "" {
		title = "helios"
	}
	// "─ " + title + " " has to leave at least one dash before the corner.
	title = ansi.Truncate(title, inner-4, "…")
	head := "─ " + sgrBold + title + sgrReset + " "
	headWidth := 3 + ansi.StringWidth(title)

	lines := []string{"┌" + head + strings.Repeat("─", max(inner-headWidth, 0)) + "┐"}

	for _, b := range o.Body {
		for _, wrapped := range wrapLine(b, width) {
			lines = append(lines, boxRow(wrapped, wrapped, width))
		}
	}

	if len(o.Options) > 0 || o.Input != nil {
		if len(o.Body) > 0 {
			lines = append(lines, boxRow("", "", width))
		}
		for i := range o.Options {
			lines = append(lines, optionRows(o, i, width)...)
		}
		if o.Input != nil {
			lines = append(lines, inputRows(o, width)...)
		}
	}

	if o.Footer != "" {
		lines = append(lines, boxRow("", "", width))
		foot := ansi.Truncate(o.Footer, width, "…")
		lines = append(lines, boxRow(foot, sgrDim+foot+sgrReset, width))
	}

	return append(lines, "└"+strings.Repeat("─", inner)+"┘")
}

// optionRows draws one choice and the description under it.
func optionRows(o Overlay, i, width int) []string {
	mark := ""
	if i < len(o.Checked) {
		mark = "[ ] "
		if o.Checked[i] {
			mark = "[x] "
		}
	}
	text := ansi.Truncate(o.Options[i], width-2-ansi.StringWidth(mark), "…")

	rows := []string{selectableRow(mark+text, i == o.Selected, width)}
	if i < len(o.Details) {
		// Indented past the checkbox as well, so the description keeps sitting
		// under the label rather than under the box.
		rows = append(rows, detailRows(o.Details[i], detailIndent+ansi.StringWidth(mark), width)...)
	}
	return rows
}

// selectableRow draws a row that the highlight can land on.
func selectableRow(text string, selected bool, width int) string {
	if !selected {
		raw := "  " + text
		return boxRow(raw, raw, width)
	}
	// Padded to the full width so the highlight is a bar, not a ragged block
	// ending at the label.
	body := "❯ " + text
	pad := strings.Repeat(" ", max(width-ansi.StringWidth(body), 0))
	return "│ " + sgrReverse + body + pad + sgrReset + " │"
}

// detailMaxLines caps a description. The box is anchored to the bottom and
// clips from the top, so an uncapped description pushes the question itself off
// the screen.
const detailMaxLines = 2

// detailIndent sets a description in from its label. Two columns would leave it
// in the same column as an unselected label, and the list would read as one
// paragraph.
const detailIndent = 4

// detailRows wraps one option's description, dim and indented under the label.
func detailRows(detail string, at, width int) []string {
	if strings.TrimSpace(detail) == "" {
		return nil
	}
	avail := width - at
	if avail < 1 {
		return nil
	}
	wrapped := wrapLine(detail, avail)
	if len(wrapped) > detailMaxLines {
		wrapped = wrapped[:detailMaxLines]
		last := detailMaxLines - 1
		wrapped[last] = ansi.Truncate(wrapped[last], avail-1, "") + "…"
	}

	indent := strings.Repeat(" ", at)
	rows := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		rows = append(rows, boxRow(indent+line, indent+sgrDim+line+sgrReset, width))
	}
	return rows
}

// inputRows draws the typed-answer row, and the field itself once it is active.
func inputRows(o Overlay, width int) []string {
	in := o.Input
	label := ansi.Truncate(in.Label, width-2, "…")
	rows := []string{selectableRow(label, o.Selected == len(o.Options), width)}
	if !in.Active {
		return rows
	}

	frame := width - detailIndent
	text := frame - 2
	if text < 1 {
		return rows
	}

	// The caret rides at the end of the value, and a value longer than the
	// field scrolls: the tail is where the typing is happening.
	field := in.Value + "█"
	if w := ansi.StringWidth(field); w > text {
		field = ansi.TruncateLeft(field, w-text+1, "…")
	}

	pad := strings.Repeat(" ", 2)
	edge := strings.Repeat("─", frame)
	top := pad + "┌" + edge + "┐"
	mid := pad + "│ " + field + strings.Repeat(" ", max(text-ansi.StringWidth(field), 0)) + " │"
	bottom := pad + "└" + edge + "┘"
	return append(rows,
		boxRow(top, top, width),
		boxRow(mid, mid, width),
		boxRow(bottom, bottom, width),
	)
}

// boxRow pads styled to width using raw's display width, so SGR bytes do not
// count toward the column count.
func boxRow(raw, styled string, width int) string {
	pad := width - ansi.StringWidth(raw)
	if pad < 0 {
		pad = 0
	}
	return "│ " + styled + strings.Repeat(" ", pad) + " │"
}

// wrapLine breaks s on spaces to fit width, so a long question is readable
// rather than truncated. An empty string yields one empty line.
func wrapLine(s string, width int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	var cur string
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case ansi.StringWidth(cur)+1+ansi.StringWidth(word) <= width:
			cur += " " + word
		default:
			out = append(out, cur)
			cur = word
		}
		// A single word longer than the box has to be cut somewhere.
		if ansi.StringWidth(cur) > width {
			out = append(out, ansi.Truncate(cur, width, "…"))
			cur = ""
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
