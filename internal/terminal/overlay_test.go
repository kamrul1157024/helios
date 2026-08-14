package terminal

import (
	"bytes"
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
