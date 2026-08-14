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
	// Selected indexes Options. Out-of-range values highlight nothing.
	Selected int `json:"selected"`
	// Footer is the key hint line.
	Footer string `json:"footer,omitempty"`
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

	if len(o.Options) > 0 {
		if len(o.Body) > 0 {
			lines = append(lines, boxRow("", "", width))
		}
		for i, opt := range o.Options {
			text := ansi.Truncate(opt, width-2, "…")
			raw := "  " + text
			styled := "  " + text
			if i == o.Selected {
				raw = "❯ " + text
				styled = sgrReverse + "❯ " + text + strings.Repeat(" ", max(width-2-ansi.StringWidth(text), 0)) + sgrReset
				// Already padded to the full width so the highlight is a bar,
				// not a ragged block ending at the label.
				lines = append(lines, "│ "+styled+" │")
				continue
			}
			lines = append(lines, boxRow(raw, styled, width))
		}
	}

	if o.Footer != "" {
		lines = append(lines, boxRow("", "", width))
		foot := ansi.Truncate(o.Footer, width, "…")
		lines = append(lines, boxRow(foot, sgrDim+foot+sgrReset, width))
	}

	return append(lines, "└"+strings.Repeat("─", inner)+"┘")
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
