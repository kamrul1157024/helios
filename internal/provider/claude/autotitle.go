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

// How long the model gets to answer.
//
// The call runs 3-6s against Vertex on a good day and occasionally spikes well
// past that. At 20s those spikes were killed mid-flight and logged as failures,
// which is a slow provider being mistaken for a broken session.
const titleCallTimeout = 45 * time.Second

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

func autoTitleSystemPrompt(forceTitle bool) string {
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

// A title carries a category we know. Matching any bracketed word instead
// picked up the model echoing the placeholder back — asked for a title with
// too little to go on, it replies "…following the format [CATEGORY] Short
// title", and that sentence looked exactly like an answer.
var titleLine = regexp.MustCompile(`\[?\b(` + strings.Join(categories, "|") + `)\b\]?`)

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

// The first category we recognise, and whatever the model put in front of it.
// Built from the list so a new category cannot be added without this seeing it.
var categoryTag = regexp.MustCompile(`\[?\b(` + strings.Join(categories, "|") + `)\b\]?[\s:]+`)

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
// The second return says whether a category was found. Our own prompt always
// asks for one, so its absence means the reply is not a title — most often the
// model asking for more context than the session has given it — and the caller
// drops it rather than naming a session after a sentence of prose. A custom
// prompt never reaches here, so its own format is left intact.
func normalizeTitle(title string, glyphs bool) (string, bool) {
	// Indices, not the submatches: what follows the tag is the title, and that
	// is the part the expression does not capture.
	at := categoryTag.FindStringSubmatchIndex(title)
	if at == nil {
		return title, false
	}
	category := title[at[2]:at[3]]
	rest := strings.TrimSpace(title[at[1]:])
	glyph, known := categoryGlyphs[category]
	if !known || rest == "" {
		return title, false
	}
	if !glyphs {
		return fmt.Sprintf("[%s] %s", category, rest), true
	}
	return fmt.Sprintf("%s [%s] %s", glyph, category, rest), true
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
func titleSystemPrompt(custom string, forceTitle bool) string {
	if trimmed := strings.TrimSpace(custom); trimmed != "" {
		return trimmed
	}
	return autoTitleSystemPrompt(forceTitle)
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

	go generateTitle(ctx.DB, sessionID, cwd, transcriptPath, notify, false)
}

// RegenerateTitle names a session on request, whatever it is called now.
//
// The automatic path leaves a titled session alone and lets the model decline
// a session it judges not worth naming. Both are right for something that
// fires on every turn. A click is the opposite: a request for a name, made by
// someone looking at the name they already have — so it ignores the existing
// title, the attempt count, and SKIP.
//
// It ignores autotitle.enabled as well. That setting decides whether helios
// names sessions on its own, which is a different question from being asked
// to name one.
//
// Synchronous, unlike the hook path, so the endpoint can say what happened: a
// button that reports nothing is how a title that never changed came to look
// like a broken feature.
func RegenerateTitle(db *store.Store, sessionID, cwd, transcriptPath string, notify func(string, interface{})) string {
	sess, err := db.GetSession(sessionID)
	if err != nil || sess == nil {
		return ""
	}
	return generateTitle(db, sessionID, cwd, transcriptPath, notify, true)
}

// generateTitle asks the model for a name and saves what comes back, returning
// the title it set or "" if it set none. `asked` is a human waiting on it,
// which is what suspends the guards that exist for the automatic path.
func generateTitle(db *store.Store, sessionID, cwd, transcriptPath string, notify func(string, interface{}), asked bool) string {
	// Read the count rather than spend one. An attempt is the session's budget
	// for ever being named, and it is only fair to charge for one once the
	// model has answered — a call that timed out says nothing about whether the
	// session is nameable, and five slow minutes used to disable titling for
	// good.
	spent, err := db.AutoTitleAttempts(sessionID)
	if err != nil {
		log.Printf("autotitle: failed to read attempts for %s: %v", sessionID, err)
		return ""
	}
	attempt := spent + 1

	// The cap stops helios spending a call per turn on a session the model
	// keeps declining. It has nothing to say about a request.
	if !asked && attempt > maxAutoTitleAttempts {
		return ""
	}

	forceTitle := asked || attempt >= maxAutoTitleAttempts

	sess, err := db.GetSession(sessionID)
	if err != nil || sess == nil {
		return ""
	}

	userMsg := ""
	if sess.LastUserMessage != nil {
		userMsg = *sess.LastUserMessage
	}
	if userMsg == "" || strings.HasPrefix(strings.TrimSpace(userMsg), "/") {
		return ""
	}
	recentPairs := extractLastExchangePairs(transcriptPath, 5)
	project := filepath.Base(cwd)

	prompt := buildTitlePrompt(project, userMsg, recentPairs)

	custom, _ := db.GetSetting("autotitle.prompt")
	emoji, _ := db.GetSetting("autotitle.emoji")
	systemPrompt := titleSystemPrompt(custom, forceTitle)

	caller := provider.GetSmallModelCaller("claude")
	if caller == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), titleCallTimeout)
	defer cancel()

	title, err := caller(ctx, systemPrompt, prompt)
	if err != nil || title == "" {
		// Deliberately before the attempt is charged: the model never answered.
		log.Printf("autotitle: haiku call failed for %s (attempt %d, not charged): %v", sessionID, attempt, err)
		return ""
	}

	// It answered. Whatever it said — a title, or SKIP — that is the session
	// spending one of its attempts.
	if _, err := db.IncrementAutoTitleAttempts(sessionID); err != nil {
		log.Printf("autotitle: failed to count the attempt for %s: %v", sessionID, err)
	}

	// Before the SKIP check, not after: a model that reasons out loud buries
	// its answer, and comparing the whole reply to "SKIP" never matches. The
	// reasoning then becomes the title.
	title = cleanTitle(title)
	if title == "" {
		log.Printf("autotitle: nothing usable in the reply for %s (attempt %d)", sessionID, attempt)
		return ""
	}

	if !forceTitle && strings.EqualFold(title, "SKIP") {
		log.Printf("autotitle: skipped for %s (attempt %d)", sessionID, attempt)
		return ""
	}

	// Only for our own prompt: a custom one owns its format, and rewriting it
	// into ours would ignore the whole point of setting it.
	if strings.TrimSpace(custom) == "" {
		normalized, ok := normalizeTitle(title, emoji != "false")
		if !ok {
			// Our prompt always asks for a category. Without one this is not a
			// title — usually the model asking for more context than the
			// session has given it yet.
			log.Printf("autotitle: no category in the reply for %s (attempt %d): %q", sessionID, attempt, truncateWords(title, 12))
			return ""
		}
		title = normalized
	}

	if err := db.UpdateSessionTitle(sessionID, title); err != nil {
		log.Printf("autotitle: failed to save title for %s: %v", sessionID, err)
		return ""
	}

	log.Printf("autotitle: set title for %s (attempt %d): %q", sessionID, attempt, title)

	if notify != nil {
		notify("session_updated", map[string]interface{}{
			"session_id": sessionID,
			"title":      title,
		})
	}
	return title
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
