package narration

import (
	"context"
	"fmt"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
)

// Event represents a single narration-worthy event from the mobile client.
type Event struct {
	Type    string `json:"type"`              // "tool_use", "tool_result", "assistant", "notification", "status"
	Tool    string `json:"tool,omitempty"`     // tool name (Read, Edit, Bash, etc.)
	Target  string `json:"target,omitempty"`   // file path, command, etc.
	Summary string `json:"summary,omitempty"` // human-readable summary
	Content string `json:"content,omitempty"` // assistant response text (truncated to 500 chars by mobile)
	Success *bool  `json:"success,omitempty"` // tool_result success/failure
	Status  string `json:"status,omitempty"`  // "idle", "error" for status events
}

// Request is the payload sent by the mobile client.
type Request struct {
	Events         []Event `json:"events"`
	SessionContext string  `json:"session_context,omitempty"` // user's prompt / session title
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

func buildPrompt(req Request) string {
	if len(req.Events) == 0 {
		return ""
	}

	var sb strings.Builder

	if req.SessionContext != "" {
		sb.WriteString(fmt.Sprintf("Session context: %s\n\n", req.SessionContext))
	}

	sb.WriteString("Events:\n")
	for i, e := range req.Events {
		sb.WriteString(fmt.Sprintf("%d. ", i+1))
		switch e.Type {
		case "assistant":
			sb.WriteString(fmt.Sprintf("[assistant] %s", e.Content))
		case "tool_use":
			sb.WriteString(fmt.Sprintf("[tool_use] %s", e.Tool))
			if e.Target != "" {
				sb.WriteString(fmt.Sprintf(" %s", e.Target))
			}
			if e.Summary != "" {
				sb.WriteString(fmt.Sprintf(" — %s", e.Summary))
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
		case "notification":
			sb.WriteString(fmt.Sprintf("[notification] %s", e.Tool))
			if e.Summary != "" {
				sb.WriteString(fmt.Sprintf(" — %s", e.Summary))
			}
		case "status":
			sb.WriteString(fmt.Sprintf("[status] %s", e.Status))
		default:
			sb.WriteString(fmt.Sprintf("[%s]", e.Type))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
