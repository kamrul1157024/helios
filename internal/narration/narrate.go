package narration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
)

// Event represents a single narration-worthy event from the mobile client.
// Mobile sends raw data; the backend handles truncation and prompt formatting.
type Event struct {
	Type    string                 `json:"type"`              // "tool_use", "tool_result", "assistant", "user", "notification", "status"
	Tool    string                 `json:"tool,omitempty"`    // tool name (Read, Edit, Bash, etc.)
	Content string                 `json:"content,omitempty"` // raw text content (assistant response, user message, notification type, tool input)
	Success *bool                  `json:"success,omitempty"` // tool_result success/failure
	Status  string                 `json:"status,omitempty"`  // "idle", "error" for status events
	Payload map[string]interface{} `json:"payload,omitempty"` // raw notification payload
}

// Request is the payload sent by the mobile client.
type Request struct {
	Events         []Event `json:"events"`
	SessionContext string  `json:"session_context,omitempty"` // last user message (global voice only)
	SessionCWD     string  `json:"session_cwd,omitempty"`     // working directory (global voice only)
	SystemPrompt   string  `json:"system_prompt,omitempty"`   // custom narrator prompt override
}

// Response is returned to the mobile client.
type Response struct {
	Narration string `json:"narration"`
}

// DefaultSystemPrompt is the default narrator persona.
const DefaultSystemPrompt = `You are a voice narrator for a coding AI assistant. You speak as the AI in first person, narrating what you're doing for a user who is listening, not reading.

Rules:
- Generate ONE short spoken sentence (max 25 words)
- If given multiple events, summarize the batch naturally into 1-2 sentences
- Be casual and natural — like you're talking to a coworker
- For assistant responses, extract the key point — don't repeat the whole thing
- For tool calls, mention what you're doing and the file/command name
- For errors, be brief — the user will check the screen
- For session completion (status=idle), let them know you're done
- Never use markdown, code formatting, quotes, or asterisks
- Never start with "I" every time — vary your sentence openers`

// Generate calls the provider's small model to narrate the given events.
// Returns an empty narration on any error — narration is fire-and-forget.
func Generate(ctx context.Context, req Request, providerID string) *Response {
	caller := provider.GetSmallModelCaller(providerID)
	if caller == nil {
		return &Response{}
	}

	prompt := buildPrompt(req)
	if prompt == "" {
		return &Response{}
	}

	system := DefaultSystemPrompt
	if req.SystemPrompt != "" {
		system = req.SystemPrompt
	}

	result, err := caller(ctx, system, prompt)
	if err != nil {
		return &Response{}
	}

	return &Response{Narration: result}
}

const maxContentLen = 300

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func buildPrompt(req Request) string {
	if len(req.Events) == 0 {
		return ""
	}

	var sb strings.Builder

	if req.SessionCWD != "" || req.SessionContext != "" {
		sb.WriteString("Session:\n")
		if req.SessionCWD != "" {
			sb.WriteString(fmt.Sprintf("- Working directory: %s\n", req.SessionCWD))
		}
		if req.SessionContext != "" {
			sb.WriteString(fmt.Sprintf("- Task: %s\n", truncate(req.SessionContext, 200)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Events:\n")
	for i, e := range req.Events {
		sb.WriteString(fmt.Sprintf("%d. ", i+1))
		switch e.Type {
		case "assistant":
			sb.WriteString(fmt.Sprintf("[assistant] %s", truncate(e.Content, maxContentLen)))
		case "user":
			sb.WriteString(fmt.Sprintf("[user] %s", truncate(e.Content, maxContentLen)))
		case "tool_use":
			sb.WriteString(fmt.Sprintf("[tool_use] %s", e.Tool))
			if e.Content != "" {
				sb.WriteString(fmt.Sprintf(" — %s", truncate(e.Content, maxContentLen)))
			}
		case "tool_result":
			sb.WriteString(fmt.Sprintf("[tool_result] %s", e.Tool))
			if e.Success != nil {
				if *e.Success {
					sb.WriteString(" — success")
				} else {
					sb.WriteString(" — failed")
				}
			}
			if e.Content != "" {
				sb.WriteString(fmt.Sprintf(": %s", truncate(e.Content, maxContentLen)))
			}
		case "notification":
			sb.WriteString(fmt.Sprintf("[notification] %s", e.Content))
			if e.Payload != nil {
				if raw, err := json.Marshal(e.Payload); err == nil {
					sb.WriteString(fmt.Sprintf(" %s", truncate(string(raw), maxContentLen)))
				}
			}
		case "status":
			sb.WriteString(fmt.Sprintf("[status] %s", e.Status))
		default:
			sb.WriteString(fmt.Sprintf("[%s]", e.Type))
			if e.Content != "" {
				sb.WriteString(fmt.Sprintf(" %s", truncate(e.Content, maxContentLen)))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
