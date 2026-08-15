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
