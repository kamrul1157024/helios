package terminal

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestOverlayFrameRoundTrip(t *testing.T) {
	want := Overlay{
		Title:    "Bash",
		Body:     []string{"rm -rf build/", "Run this command?"},
		Options:  []string{"Yes", "Yes, and don't ask again", "No"},
		Details:  []string{"Once.", "And write the rule.", ""},
		Checked:  []bool{false, false, true},
		Input:    &OverlayInput{Label: "Other…", Value: "half typed", Active: true},
		Selected: 2,
		Footer:   "↑↓ select · enter confirm",
	}

	var buf bytes.Buffer
	if err := WriteJSONFrame(&buf, FrameOverlaySet, want); err != nil {
		t.Fatalf("WriteJSONFrame: %v", err)
	}
	f, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Type != FrameOverlaySet {
		t.Fatalf("type = %s, want overlay-set", f.Type)
	}
	got, err := ParseOverlay(f.Payload)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	if got.Title != want.Title || got.Selected != want.Selected || got.Footer != want.Footer {
		t.Errorf("overlay = %+v, want %+v", got, want)
	}
	if strings.Join(got.Options, "|") != strings.Join(want.Options, "|") {
		t.Errorf("options = %v, want %v", got.Options, want.Options)
	}
	if strings.Join(got.Body, "|") != strings.Join(want.Body, "|") {
		t.Errorf("body = %v, want %v", got.Body, want.Body)
	}
	if strings.Join(got.Details, "|") != strings.Join(want.Details, "|") {
		t.Errorf("details = %v, want %v", got.Details, want.Details)
	}
	if len(got.Checked) != len(want.Checked) || !got.Checked[2] {
		t.Errorf("checked = %v, want %v", got.Checked, want.Checked)
	}
	if got.Input == nil || *got.Input != *want.Input {
		t.Errorf("input = %+v, want %+v", got.Input, want.Input)
	}
}

func TestParseOverlayRejectsGarbage(t *testing.T) {
	if _, err := ParseOverlay([]byte("not json")); err == nil {
		t.Error("expected an error decoding a malformed overlay")
	}
}

func TestRenderOverlayDrawsTitleBodyAndOptions(t *testing.T) {
	out := string(RenderOverlay(Overlay{
		Title:   "Permission",
		Body:    []string{"Claude wants to run a command."},
		Options: []string{"Allow", "Deny"},
		Footer:  "enter to confirm",
	}, 80, 24))

	for _, want := range []string{"Permission", "Claude wants to run", "Allow", "Deny", "enter to confirm", "┌", "└"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	// Self-contained: it must restore whatever the application left behind.
	if !strings.HasPrefix(out, cursorSave) || !strings.HasSuffix(out, cursorRestore) {
		t.Error("render is not bracketed by a cursor save/restore")
	}
	if !strings.Contains(out, cursorHide) {
		t.Error("render does not hide the cursor")
	}
}

func TestRenderOverlayHighlightsTheSelectedOption(t *testing.T) {
	out := string(RenderOverlay(Overlay{
		Title:    "Pick",
		Options:  []string{"first", "second"},
		Selected: 1,
	}, 80, 24))

	before, after, found := strings.Cut(out, sgrReverse)
	if !found {
		t.Fatalf("no highlight in render:\n%s", out)
	}
	if strings.Contains(before, "second") {
		t.Error("the selected option was drawn before the highlight")
	}
	if !strings.HasPrefix(strings.TrimPrefix(after, "❯ "), "second") {
		t.Errorf("highlight does not lead with the selected option: %q", after)
	}
}

// TestRenderOverlayAnchorsToTheBottom pins the placement: the box has to sit
// where a CLI puts its own prompt, not over the middle of the transcript.
func TestRenderOverlayAnchorsToTheBottom(t *testing.T) {
	const rows = 24
	out := string(RenderOverlay(Overlay{Title: "Pick", Options: []string{"a", "b"}}, 80, rows))

	positions := regexp.MustCompile(`\x1b\[(\d+);(\d+)H`).FindAllStringSubmatch(out, -1)
	if len(positions) == 0 {
		t.Fatalf("render positions nothing:\n%q", out)
	}
	last, err := strconv.Atoi(positions[len(positions)-1][1])
	if err != nil {
		t.Fatalf("parse row: %v", err)
	}
	if last != rows {
		t.Errorf("last line drawn on row %d, want %d", last, rows)
	}
}

// TestRenderOverlayClipsFromTheTop covers a box taller than the viewport: the
// options and the key hint are what the user needs, so the body goes first.
func TestRenderOverlayClipsFromTheTop(t *testing.T) {
	body := make([]string, 40)
	for i := range body {
		body[i] = "a line of explanation"
	}
	out := string(RenderOverlay(Overlay{
		Title:   "Long",
		Body:    body,
		Options: []string{"Allow", "Deny"},
	}, 80, 10))

	positions := regexp.MustCompile(`\x1b\[(\d+);\d+H`).FindAllStringSubmatch(out, -1)
	if len(positions) != 10 {
		t.Errorf("drew %d lines into a 10-row viewport", len(positions))
	}
	if !strings.Contains(out, "Deny") || !strings.Contains(out, "└") {
		t.Error("clipping dropped the bottom of the box instead of the top")
	}
}

func TestRenderOverlayNeedsRoom(t *testing.T) {
	if got := RenderOverlay(Overlay{Title: "Pick", Options: []string{"a"}}, 20, 24); got != nil {
		t.Errorf("rendered into a 20-column terminal: %q", got)
	}
	if got := RenderOverlay(Overlay{Title: "Pick", Options: []string{"a"}}, 80, 2); got != nil {
		t.Errorf("rendered into a 2-row terminal: %q", got)
	}
}

// TestOverlayBoxRowsAreUniformWidth is the padding contract: styling bytes must
// not count toward a column, or the right-hand border goes ragged.
func TestOverlayBoxRowsAreUniformWidth(t *testing.T) {
	const width = 40
	lines := overlayBox(Overlay{
		Title:    "A rather long title that keeps going",
		Body:     []string{"", "short", strings.Repeat("wrap me ", 12)},
		Options:  []string{"yes", strings.Repeat("verbose-", 10), "no"},
		Details:  []string{"a short reason", strings.Repeat("a long reason ", 8), ""},
		Checked:  []bool{true, false, true},
		Input:    &OverlayInput{Label: "Other…", Value: strings.Repeat("typed ", 12), Active: true},
		Selected: 1,
		Footer:   "↑↓ select · enter confirm · esc cancel",
	}, width)

	for i, line := range lines {
		if got := ansi.StringWidth(line); got != width+4 {
			t.Errorf("line %d width = %d, want %d: %q", i, got, width+4, line)
		}
	}
}

func TestWrapLineBreaksOnSpaces(t *testing.T) {
	got := wrapLine("one two three four five", 9)
	want := []string{"one two", "three", "four five"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapLine = %v, want %v", got, want)
	}
}

func TestWrapLineCutsAnOverlongWord(t *testing.T) {
	for _, line := range wrapLine(strings.Repeat("x", 40), 10) {
		if ansi.StringWidth(line) > 10 {
			t.Errorf("wrapped line too wide: %q", line)
		}
	}
}

func TestRenderOverlayDrawsDescriptionsUnderTheirOptions(t *testing.T) {
	out := string(RenderOverlay(Overlay{
		Title:   "Next step",
		Options: []string{"Live repro", "Code review"},
		Details: []string{"Drive the real TUI.", "Read the whole diff."},
	}, 80, 24))

	first := strings.Index(out, "Live repro")
	reason := strings.Index(out, "Drive the real TUI.")
	second := strings.Index(out, "Code review")
	if first < 0 || reason < 0 || second < 0 {
		t.Fatalf("render is missing a label or a description:\n%s", out)
	}
	if !(first < reason && reason < second) {
		t.Errorf("the description is not between its own label and the next:\n%s", out)
	}
	if !strings.Contains(out, sgrDim+"Drive the real TUI.") {
		t.Errorf("the description is not dim:\n%s", out)
	}
}

// A description is capped rather than wrapped forever: the box is anchored to
// the bottom and clips from the top, so a long one would push the question off
// the screen.
func TestRenderOverlayCapsALongDescription(t *testing.T) {
	lines := overlayBox(Overlay{
		Options: []string{"only"},
		Details: []string{strings.Repeat("reason ", 60)},
	}, 40)

	body := 0
	for _, line := range lines {
		if strings.Contains(line, "reason") {
			body++
		}
	}
	if body != detailMaxLines {
		t.Errorf("description drew %d lines, want %d", body, detailMaxLines)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "…") {
		t.Error("a cut description does not say it was cut")
	}
}

func TestRenderOverlayDrawsCheckboxes(t *testing.T) {
	out := string(RenderOverlay(Overlay{
		Options: []string{"Unit tests", "Race detector"},
		Checked: []bool{true, false},
	}, 80, 24))

	if !strings.Contains(out, "[x] Unit tests") {
		t.Errorf("a ticked option is not drawn ticked:\n%s", out)
	}
	if !strings.Contains(out, "[ ] Race detector") {
		t.Errorf("an unticked option is not drawn empty:\n%s", out)
	}
}

func TestRenderOverlayDrawsTheAnswerField(t *testing.T) {
	closed := string(RenderOverlay(Overlay{
		Options: []string{"Live repro"},
		Input:   &OverlayInput{Label: "Other…"},
	}, 80, 24))
	if !strings.Contains(closed, "Other…") {
		t.Errorf("the row that opens the field is missing:\n%s", closed)
	}
	if strings.Contains(closed, "█") {
		t.Errorf("a closed field drew a caret:\n%s", closed)
	}

	open := string(RenderOverlay(Overlay{
		Options:  []string{"Live repro"},
		Input:    &OverlayInput{Label: "Other…", Value: "rebase first", Active: true},
		Selected: 1,
	}, 80, 24))
	if !strings.Contains(open, "rebase first█") {
		t.Errorf("the open field does not show the value and caret:\n%s", open)
	}
}

// A value longer than the field scrolls: the tail is where the typing is.
func TestRenderOverlayScrollsALongValue(t *testing.T) {
	lines := overlayBox(Overlay{
		Options: []string{"only"},
		Input:   &OverlayInput{Label: "Other…", Value: strings.Repeat("x", 200) + "tail", Active: true},
	}, 40)

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "tail█") {
		t.Errorf("the end of the value is not visible:\n%s", joined)
	}
	if !strings.Contains(joined, "…") {
		t.Error("a scrolled value does not say it was scrolled")
	}
}

// The promise to a host from an older build: an overlay that uses none of the
// new fields marshals to the bytes it always did, so that host keeps painting.
func TestAPlainOverlayMarshalsWithoutTheNewKeys(t *testing.T) {
	b, err := json.Marshal(Overlay{
		Title:   "Bash",
		Options: []string{"Allow", "Deny"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, key := range []string{"details", "checked", "input"} {
		if bytes.Contains(b, []byte(key)) {
			t.Errorf("plain overlay carries %q: %s", key, b)
		}
	}
}
