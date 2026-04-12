package reporter

import (
	"fmt"
	"strings"
)

// SessionContext provides session identity for the prompt so Haiku
// can reference which session it's narrating (critical for global voice
// where multiple sessions are narrated on the same stream).
type SessionContext struct {
	CWD             string
	Title           *string
	LastUserMessage *string
}

const maxContentLen = 300

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func buildPrompt(events []Event, ctx *SessionContext) string {
	if len(events) == 0 {
		return ""
	}

	var sb strings.Builder

	// Session identity — so Haiku knows what session this is about.
	// In global voice mode the user hears narrations from multiple sessions,
	// so the AI needs to reference the task/project naturally.
	if ctx != nil {
		sb.WriteString("Session:\n")
		if ctx.Title != nil && *ctx.Title != "" {
			sb.WriteString(fmt.Sprintf("- Title: %s\n", truncate(*ctx.Title, 200)))
		}
		if ctx.LastUserMessage != nil && *ctx.LastUserMessage != "" {
			sb.WriteString(fmt.Sprintf("- Task: %s\n", truncate(*ctx.LastUserMessage, 200)))
		}
		if ctx.CWD != "" {
			sb.WriteString(fmt.Sprintf("- Directory: %s\n", ctx.CWD))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Events:\n")
	for i, e := range events {
		sb.WriteString(fmt.Sprintf("%d. ", i+1))
		switch e.Type {
		case "tool_pre":
			sb.WriteString(fmt.Sprintf("[using tool] %s", e.ToolName))
			if e.ToolInput != "" {
				sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
			}
		case "tool_post":
			sb.WriteString(fmt.Sprintf("[tool done] %s", e.ToolName))
			if e.ToolInput != "" {
				sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
			}
		case "tool_post_failure":
			sb.WriteString(fmt.Sprintf("[tool failed] %s", e.ToolName))
			if e.ToolInput != "" {
				sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
			}
		case "prompt_submit":
			sb.WriteString(fmt.Sprintf("[user said] %s", truncate(e.Message, maxContentLen)))
		case "stop":
			sb.WriteString("[session completed]")
			if e.Detail != "" {
				sb.WriteString(fmt.Sprintf(" — last action: %s", truncate(e.Detail, maxContentLen)))
			}
		case "stop_failure":
			sb.WriteString("[session error]")
			if e.Detail != "" {
				sb.WriteString(fmt.Sprintf(" — %s", truncate(e.Detail, maxContentLen)))
			}
		case "permission":
			sb.WriteString(fmt.Sprintf("[permission needed] %s", e.ToolName))
			if e.ToolInput != "" {
				sb.WriteString(fmt.Sprintf(": %s", truncate(e.ToolInput, maxContentLen)))
			}
		case "question":
			sb.WriteString("[claude is asking]")
			if e.Message != "" {
				sb.WriteString(fmt.Sprintf(" %s", truncate(e.Message, maxContentLen)))
			}
		case "session_start":
			sb.WriteString(fmt.Sprintf("[session started] in %s", e.CWD))
		case "session_end":
			sb.WriteString("[session ended]")
		case "compact_pre":
			sb.WriteString("[compacting context]")
		case "compact_post":
			sb.WriteString("[context compacted]")
		case "subagent_start":
			sb.WriteString(fmt.Sprintf("[spawned subagent] %s", e.AgentType))
			if e.Detail != "" {
				sb.WriteString(fmt.Sprintf(" — %s", truncate(e.Detail, maxContentLen)))
			}
		case "subagent_stop":
			sb.WriteString("[subagent completed]")
		case "notification":
			sb.WriteString(fmt.Sprintf("[notification] %s", truncate(e.Message, maxContentLen)))
		default:
			sb.WriteString(fmt.Sprintf("[%s]", e.Type))
			if e.Message != "" {
				sb.WriteString(fmt.Sprintf(" %s", truncate(e.Message, maxContentLen)))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
