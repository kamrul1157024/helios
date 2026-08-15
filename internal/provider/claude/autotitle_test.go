package claude

import (
	"strings"
	"testing"
)

func TestTitlePrompt_NamesAGlyphForEveryCategory(t *testing.T) {
	prompt := autoTitleSystemPrompt(false)

	for _, category := range categories {
		glyph, ok := categoryGlyphs[category]
		if !ok {
			t.Errorf("category %s has no glyph", category)
			continue
		}
		// The pair, not just the two separately: the model is told to use the
		// glyph beside the category, so they have to arrive together.
		if !strings.Contains(prompt, glyph+" "+category) {
			t.Errorf("prompt does not pair %q with %s", glyph, category)
		}
	}
}

// Two categories sharing a glyph would make the prefix meaningless as a signal.
func TestTitlePrompt_GlyphsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for category, glyph := range categoryGlyphs {
		if other, clash := seen[glyph]; clash {
			t.Errorf("%s and %s share the glyph %q", other, category, glyph)
		}
		seen[glyph] = category
	}
}

// The emoji instruction is what the glyphs replaced; leaving it in would invite
// the model to reach for one anyway.
func TestTitlePrompt_DoesNotAskForEmoji(t *testing.T) {
	prompt := autoTitleSystemPrompt(false)
	if strings.Contains(prompt, "EMOJI") {
		t.Error("prompt still asks for an EMOJI")
	}
	if !strings.Contains(prompt, "GLYPH [CATEGORY]") {
		t.Error("prompt does not state the glyph format")
	}
}

func TestTitlePrompt_SkipIsOfferedUntilTheLastAttempt(t *testing.T) {
	if !strings.Contains(autoTitleSystemPrompt(false), "SKIP") {
		t.Error("a normal attempt should be allowed to skip a greeting")
	}
	if strings.Contains(autoTitleSystemPrompt(true), "SKIP") {
		t.Error("the forced attempt must not be offered a way out")
	}
}

func TestTitlePrompt_CustomReplacesTheBuiltIn(t *testing.T) {
	custom := "Name the session in Bengali. Nothing else."
	got := titleSystemPrompt(custom, true, false)

	if got != custom {
		t.Errorf("custom prompt not used verbatim: %q", got)
	}
	// Appending would leave two format rules contradicting each other.
	if strings.Contains(got, "GLYPH") {
		t.Error("the built-in rules leaked into a custom prompt")
	}
}

func TestTitlePrompt_BlankCustomFallsBack(t *testing.T) {
	for _, blank := range []string{"", "   ", "\n\t "} {
		got := titleSystemPrompt(blank, true, false)
		if !strings.Contains(got, "GLYPH [CATEGORY]") {
			t.Errorf("blank custom prompt %q did not fall back to the built-in", blank)
		}
	}
}

func TestTitlePrompt_GlyphsOffDropsThePrefix(t *testing.T) {
	got := titleSystemPrompt("", false, false)

	if strings.Contains(got, "GLYPH") {
		t.Error("glyphs were turned off but the prompt still asks for one")
	}
	if !strings.Contains(got, "Format: [CATEGORY]") {
		t.Errorf("expected the bare category format, got: %q", got)
	}
}

// The reply that named a session after several paragraphs of the model's own
// reasoning, copied out of the daemon log. Everything above the last line is
// Haiku thinking out loud.
var narratedReply = `I need to generate a session title based on the context provided.

Let me analyze the session:
- **Project**: helios
- **User messages**: "merge all", "Check logs if regenerate title button works or not?"
- **Topic**: Debugging a title regeneration feature

This is a **FIX** category session - they're troubleshooting why the regenerate
title button isn't working properly.

` + "\n" + categoryGlyphs["FIX"] + ` [FIX] Title regenerate button nav refresh`

func TestCleanTitle_TakesTheTitleOutOfTheReasoning(t *testing.T) {
	got := cleanTitle(narratedReply)

	if want := categoryGlyphs["FIX"] + " [FIX] Title regenerate button nav refresh"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCleanTitle_LeavesAPlainTitleAlone(t *testing.T) {
	// Built from the real glyph, not one typed into this file: a PUA character
	// pasted by hand is easy to mangle into something that only looks right.
	title := categoryGlyphs["FEAT"] + " [FEAT] Add file upload to the composer"

	if got := cleanTitle(title); got != title {
		t.Errorf("got %q, want %q", got, title)
	}
}

// TrimSpace must not eat the prefix: the glyphs sit in the Private Use Area,
// and a whitespace-like character there would be silently trimmed away.
func TestCleanTitle_KeepsEveryGlyph(t *testing.T) {
	for category, glyph := range categoryGlyphs {
		title := glyph + " [" + category + "] Some short title"
		if got := cleanTitle(title); got != title {
			t.Errorf("%s: glyph did not survive — got %q, want %q", category, got, title)
		}
	}
}

// The check that decides whether to skip runs on the cleaned value, so a model
// that reasons its way to SKIP still skips instead of being saved whole.
func TestCleanTitle_FindsSkipAfterPreamble(t *testing.T) {
	got := cleanTitle("The user only said hello, so there is nothing to name.\n\nSKIP")
	if !strings.EqualFold(got, "SKIP") {
		t.Errorf("got %q, want SKIP", got)
	}
}

func TestCleanTitle_StripsTheDressing(t *testing.T) {
	cases := map[string]string{
		"**[FIX] Stop the double upload**":    "[FIX] Stop the double upload",
		`"[FIX] Stop the double upload"`:      "[FIX] Stop the double upload",
		"Title: [FIX] Stop the double upload": "[FIX] Stop the double upload",
		"`[FIX] Stop the double upload`":      "[FIX] Stop the double upload",
	}
	for raw, want := range cases {
		if got := cleanTitle(raw); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A bullet belongs to the analysis around it. Picking one would title the
// session "Project: helios".
func TestCleanTitle_IgnoresBulletedAnalysis(t *testing.T) {
	got := cleanTitle("Analysis:\n- **Project**: helios\n- Topic: uploads\n\n[FEAT] Upload files from the phone")
	if want := "[FEAT] Upload files from the phone"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Reasoning with no title at the end of it is not a title, and saving the last
// sentence of it would be worse than saving nothing.
func TestCleanTitle_RejectsProse(t *testing.T) {
	prose := "This session is a long discussion about many things and the model " +
		"never actually produced a short title for it, it simply kept explaining."
	if got := cleanTitle(prose); got != "" {
		t.Errorf("expected nothing usable, got %q", got)
	}
}

func TestCleanTitle_EmptyReplyIsNothing(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\n"} {
		if got := cleanTitle(raw); got != "" {
			t.Errorf("cleanTitle(%q) = %q, want empty", raw, got)
		}
	}
}

// The prompt cannot enforce this on its own, but it should still ask.
func TestTitlePrompt_ForbidsPreamble(t *testing.T) {
	for _, prompt := range []string{autoTitleSystemPrompt(false), autoTitleSystemPromptNoEmoji(false)} {
		if !strings.Contains(prompt, "No reasoning") {
			t.Errorf("prompt does not forbid reasoning: %q", prompt)
		}
	}
}

// What the live model actually returns: an emoji of its own choosing where the
// glyph was asked for, and the brackets dropped. PUA codepoints are not
// something it can reproduce, however firmly the prompt asks.
func TestNormalizeTitle_ReplacesTheModelsEmojiWithTheGlyph(t *testing.T) {
	got := normalizeTitle("🗂️ API Multipart file upload with stored paths", true)

	want := categoryGlyphs["API"] + " [API] Multipart file upload with stored paths"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeTitle_AddsTheGlyphWhenTheModelSentNone(t *testing.T) {
	got := normalizeTitle("[FIX] Stop the double upload", true)

	if want := categoryGlyphs["FIX"] + " [FIX] Stop the double upload"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeTitle_KeepsACorrectTitleAsItIs(t *testing.T) {
	title := categoryGlyphs["DB"] + " [DB] Add the uploads table"

	if got := normalizeTitle(title, true); got != title {
		t.Errorf("got %q, want %q", got, title)
	}
}

// With icons off, whatever the model prefixed comes off too.
func TestNormalizeTitle_StripsThePrefixWhenIconsAreOff(t *testing.T) {
	if got := normalizeTitle("🗂️ API Multipart upload", false); got != "[API] Multipart upload" {
		t.Errorf("got %q", got)
	}
	if got := normalizeTitle(categoryGlyphs["DB"]+" [DB] Add a table", false); got != "[DB] Add a table" {
		t.Errorf("got %q", got)
	}
}

// A custom prompt's format is the user's, and an unknown tag is not ours to
// rewrite.
func TestNormalizeTitle_LeavesUnknownShapesAlone(t *testing.T) {
	for _, title := range []string{
		"PROJ-123 fix the upload retry",
		"just some words with no category",
		"[UNKNOWNCAT] something else",
	} {
		if got := normalizeTitle(title, true); got != title {
			t.Errorf("normalizeTitle(%q) = %q, want it untouched", title, got)
		}
	}
}
