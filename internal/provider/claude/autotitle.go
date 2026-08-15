package claude

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/transcript"
)

const maxAutoTitleAttempts = 5

var categories = []string{"DB", "AUTH", "API", "UI", "TEST", "DOCS", "INFRA", "REFACTOR", "FIX", "FEAT"}

// The glyph that goes in front of each category, from the Nerd Font range.
//
// Emoji were the model's own choice and no two sessions agreed: the same
// refactor came back a hammer one time and a broom the next, at double width,
// aligning badly down a column of monospaced titles. A glyph per category is
// one cell wide, the same for the same kind of work, and sits in a terminal
// font without pushing the text off its grid.
//
// Named in the prompt rather than prefixed afterwards, because the model is the
// one choosing the category — asking for the pair keeps the two agreeing.
var categoryGlyphs = map[string]string{
	"DB":       "", // database
	"AUTH":     "", // lock
	"API":      "", // exchange
	"UI":       "", // display
	"TEST":     "", // flask
	"DOCS":     "", // book
	"INFRA":    "", // server
	"REFACTOR": "", // cycle
	"FIX":      "", // bug
	"FEAT":     "", // star
}

// glyphList renders the pairs for the prompt, in the order of `categories` so
// the list reads the same way on every call.
func glyphList() string {
	var sb strings.Builder
	for _, category := range categories {
		sb.WriteString(fmt.Sprintf("\n  %s %s", categoryGlyphs[category], category))
	}
	return sb.String()
}

func autoTitleSystemPrompt(forceTitle bool) string {
	skipLine := ""
	if !forceTitle {
		skipLine = `- If the session is a greeting, test message, or non-substantive (e.g. "hi", "hello", "thanks", "test"), respond with exactly: SKIP` + "\n"
	}

	return fmt.Sprintf(`You are a session title generator for a coding assistant.

Given a session context (project, user message, assistant response), generate a concise title.

Rules:
%s- Pick the one category that fits, and use the glyph written beside it:%s
- Copy the glyph exactly. Do not replace it with an emoji or a description.
- Keep the title 5-8 words.
- Format: GLYPH [CATEGORY] Short title here
- Reply with the title and nothing before it. No reasoning, no analysis, no
  preamble, no quotes — the first thing you write is the title itself.`, skipLine, glyphList())
}

func autoTitleSystemPromptNoEmoji(forceTitle bool) string {
	categoryList := strings.Join(categories, ", ")
	skipLine := ""
	if !forceTitle {
		skipLine = `- If the session is a greeting, test message, or non-substantive (e.g. "hi", "hello", "thanks", "test"), respond with exactly: SKIP` + "\n"
	}

	return fmt.Sprintf(`You are a session title generator for a coding assistant.

Given a session context (project, user message, assistant response), generate a concise title.

Rules:
%s- Pick one category from: [%s]
- Keep the title 5-8 words.
- Format: [CATEGORY] Short title here
- Reply with the title and nothing before it. No reasoning, no analysis, no
  preamble, no quotes — the first thing you write is the title itself.`, skipLine, categoryList)
}

// Longest a title may be before it is treated as prose rather than a title.
const maxTitleChars = 120

// A title carries its category in brackets, which is what marks the answer out
// from any commentary around it.
var titleLine = regexp.MustCompile(`\[[A-Z]+\]`)

// cleanTitle finds the title in whatever the model sent back.
//
// Haiku narrates. Asked for a title it will often reason first — the project,
// the messages, why FIX and not FEAT — and put the answer on the last line.
// Saved verbatim that becomes a title several paragraphs long, which is what
// a session was named before this. The prompt asks for no preamble, but a
// prompt is a request; this is the part that holds.
//
// The last line wins, because that is where a model that thinks out loud puts
// its conclusion, and among the lines the one carrying a [CATEGORY] wins over
// one that does not.
func cleanTitle(raw string) string {
	var candidates []string
	for _, line := range strings.Split(raw, "\n") {
		if tidied := tidyTitleLine(line); tidied != "" {
			candidates = append(candidates, tidied)
		}
	}
	if len(candidates) == 0 {
		return ""
	}

	chosen := candidates[len(candidates)-1]
	for i := len(candidates) - 1; i >= 0; i-- {
		if titleLine.MatchString(candidates[i]) {
			chosen = candidates[i]
			break
		}
	}

	// Prose that got this far is not a title, and a sidebar cannot show it.
	if len(chosen) > maxTitleChars {
		return ""
	}
	return chosen
}

// The category the model settled on, and whatever it put in front of it.
var categoryTag = regexp.MustCompile(`^(.*?)\[?([A-Z]{2,10})\]?\s+(.*)$`)

// normalizeTitle puts the right glyph on the front, whatever the model sent.
//
// Asked to copy a Nerd Font glyph the model does not: it answers "🗂️ API ..."
// with an emoji of its own choosing and the brackets dropped. Those codepoints
// sit in the Private Use Area, and a model has no reliable way to reproduce
// one — telling it twice in the prompt does not change that.
//
// So the model is left to do the part it is good at, choosing the category,
// and the glyph is written here from the category it chose. That also settles
// the disagreement the old emoji had with itself: one glyph per category, the
// same every time.
//
// Anything without a recognisable category is passed through untouched, which
// is what keeps a custom prompt's own format intact.
func normalizeTitle(title string, glyphs bool) string {
	match := categoryTag.FindStringSubmatch(title)
	if match == nil {
		return title
	}
	category, rest := match[2], strings.TrimSpace(match[3])
	glyph, known := categoryGlyphs[category]
	if !known || rest == "" {
		return title
	}
	if !glyphs {
		return fmt.Sprintf("[%s] %s", category, rest)
	}
	return fmt.Sprintf("%s [%s] %s", glyph, category, rest)
}

// tidyTitleLine strips the dressing a model puts around an answer.
func tidyTitleLine(line string) string {
	line = strings.TrimSpace(line)
	// Markdown emphasis, which arrives on the analysis lines and sometimes on
	// the answer itself.
	line = strings.ReplaceAll(line, "**", "")
	line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
	for _, prefix := range []string{"Title:", "title:", "Answer:"} {
		line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	line = strings.Trim(line, "`\"' ")
	// A bullet is part of the reasoning it introduces, never the title.
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return ""
	}
	return strings.TrimSpace(line)
}

// titleSystemPrompt picks the instructions the model is given.
//
// A custom prompt replaces the built-in one outright rather than being appended
// to it. Someone who writes their own wants their own format — a house style, a
// ticket number, another language — and leaving the built-in rules underneath
// would have the two contradict each other over the format line.
//
// The cost of that is theirs to carry: SKIP and the glyph list are ours, so a
// custom prompt that does not mention SKIP will title every "hi" it sees.
func titleSystemPrompt(custom string, glyphs, forceTitle bool) string {
	if trimmed := strings.TrimSpace(custom); trimmed != "" {
		return trimmed
	}
	if glyphs {
		return autoTitleSystemPrompt(forceTitle)
	}
	return autoTitleSystemPromptNoEmoji(forceTitle)
}

// TriggerAutoTitle checks eligibility and fires async title generation if appropriate.
func TriggerAutoTitle(ctx *provider.HookContext, sessionID, cwd, transcriptPath string, notify func(string, interface{})) {
	enabled, _ := ctx.DB.GetSetting("autotitle.enabled")
	if enabled != "true" {
		return
	}

	sess, err := ctx.DB.GetSession(sessionID)
	if err != nil || sess == nil || sess.Title != nil {
		return
	}

	go generateTitle(ctx.DB, sessionID, cwd, transcriptPath, notify)
}

func generateTitle(db *store.Store, sessionID, cwd, transcriptPath string, notify func(string, interface{})) {
	attempts, err := db.IncrementAutoTitleAttempts(sessionID)
	if err != nil {
		log.Printf("autotitle: failed to increment attempts for %s: %v", sessionID, err)
		return
	}

	if attempts > maxAutoTitleAttempts {
		return
	}

	forceTitle := attempts >= maxAutoTitleAttempts

	sess, err := db.GetSession(sessionID)
	if err != nil || sess == nil {
		return
	}

	userMsg := ""
	if sess.LastUserMessage != nil {
		userMsg = *sess.LastUserMessage
	}
	if userMsg == "" || strings.HasPrefix(strings.TrimSpace(userMsg), "/") {
		return
	}
	recentPairs := extractLastExchangePairs(transcriptPath, 5)
	project := filepath.Base(cwd)

	prompt := buildTitlePrompt(project, userMsg, recentPairs)

	custom, _ := db.GetSetting("autotitle.prompt")
	emoji, _ := db.GetSetting("autotitle.emoji")
	systemPrompt := titleSystemPrompt(custom, emoji != "false", forceTitle)

	caller := provider.GetSmallModelCaller("claude")
	if caller == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	title, err := caller(ctx, systemPrompt, prompt)
	if err != nil || title == "" {
		log.Printf("autotitle: haiku call failed for %s (attempt %d): %v", sessionID, attempts, err)
		return
	}

	// Before the SKIP check, not after: a model that reasons out loud buries
	// its answer, and comparing the whole reply to "SKIP" never matches. The
	// reasoning then becomes the title.
	title = cleanTitle(title)
	if title == "" {
		log.Printf("autotitle: nothing usable in the reply for %s (attempt %d)", sessionID, attempts)
		return
	}

	if !forceTitle && strings.EqualFold(title, "SKIP") {
		log.Printf("autotitle: skipped for %s (attempt %d)", sessionID, attempts)
		return
	}

	// Only for our own prompt: a custom one owns its format, and rewriting it
	// into ours would ignore the whole point of setting it.
	if strings.TrimSpace(custom) == "" {
		title = normalizeTitle(title, emoji != "false")
	}

	if err := db.UpdateSessionTitle(sessionID, title); err != nil {
		log.Printf("autotitle: failed to save title for %s: %v", sessionID, err)
		return
	}

	log.Printf("autotitle: set title for %s (attempt %d): %q", sessionID, attempts, title)

	if notify != nil {
		notify("session_updated", map[string]interface{}{
			"session_id": sessionID,
			"title":      title,
		})
	}
}

func buildTitlePrompt(project, userMsg string, pairs []exchangePair) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project: %s\n", project))
	sb.WriteString(fmt.Sprintf("Last user message: %s\n", truncateWords(userMsg, 50)))
	if len(pairs) > 0 {
		sb.WriteString("Recent conversation:\n")
		for _, p := range pairs {
			if p.user != "" {
				sb.WriteString(fmt.Sprintf("  User: %s\n", truncateWords(p.user, 50)))
			}
			if p.assistant != "" {
				sb.WriteString(fmt.Sprintf("  Assistant: %s\n", truncateWords(p.assistant, 50)))
			}
		}
	}
	return sb.String()
}

type exchangePair struct {
	user      string
	assistant string
}

// extractLastExchangePairs returns the last n user+assistant pairs from the
// transcript. ParseClaudeTranscript with offset=0 returns the most recent
// messages, so we scan in reverse collecting complete pairs.
func extractLastExchangePairs(transcriptPath string, n int) []exchangePair {
	if transcriptPath == "" {
		return nil
	}

	result, err := transcript.ParseClaudeTranscript(transcriptPath, 200, 0)
	if err != nil {
		return nil
	}

	// Scan in reverse, collecting pairs.
	var pairs []exchangePair
	var current exchangePair
	for i := len(result.Messages) - 1; i >= 0 && len(pairs) < n; i-- {
		msg := result.Messages[i]
		switch msg.Role {
		case transcript.RoleAssistant:
			if msg.Content != "" && current.assistant == "" {
				current.assistant = msg.Content
			}
		case transcript.RoleUser:
			if msg.Content != "" && current.user == "" {
				current.user = msg.Content
				// Complete pair — prepend so output is chronological.
				pairs = append([]exchangePair{current}, pairs...)
				current = exchangePair{}
			}
		}
	}

	return pairs
}

func truncateWords(s string, maxWords int) string {
	words := strings.Fields(s)
	if len(words) <= maxWords {
		return s
	}
	return strings.Join(words[:maxWords], " ") + "..."
}
