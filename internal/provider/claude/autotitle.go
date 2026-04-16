package claude

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/transcript"
)

const maxAutoTitleAttempts = 5

var categories = []string{"DB", "AUTH", "API", "UI", "TEST", "DOCS", "INFRA", "REFACTOR", "FIX", "FEAT"}

func autoTitleSystemPrompt(forceTitle bool) string {
	categoryList := strings.Join(categories, ", ")
	skipLine := ""
	if !forceTitle {
		skipLine = `- If the session is a greeting, test message, or non-substantive (e.g. "hi", "hello", "thanks", "test"), respond with exactly: SKIP` + "\n"
	}

	emojiLine := "- Start with one relevant emoji, then the category tag, then the title."
	return fmt.Sprintf(`You are a session title generator for a coding assistant.

Given a session context (project, user message, assistant response), generate a concise title.

Rules:
%s- Pick one category from: [%s]
- %s
- Keep the title 5-8 words.
- Format: EMOJI [CATEGORY] Short title here
- No explanation, no quotes, nothing else.`, skipLine, categoryList, emojiLine)
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
- No explanation, no quotes, nothing else.`, skipLine, categoryList)
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

	emojiEnabled := true
	val, _ := db.GetSetting("autotitle.emoji")
	if val == "false" {
		emojiEnabled = false
	}

	var systemPrompt string
	if emojiEnabled {
		systemPrompt = autoTitleSystemPrompt(forceTitle)
	} else {
		systemPrompt = autoTitleSystemPromptNoEmoji(forceTitle)
	}

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

	title = strings.TrimSpace(title)

	if !forceTitle && strings.EqualFold(title, "SKIP") {
		log.Printf("autotitle: skipped for %s (attempt %d)", sessionID, attempts)
		return
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
