package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// maxLineBytes bounds one transcript entry. A single tool result can hold an
// entire file, so the cap is generous; what matters is that it is a cap.
const maxLineBytes = 16 << 20

// MessageRole identifies the type of transcript entry.
type MessageRole string

const (
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolUse    MessageRole = "tool_use"
	RoleToolResult MessageRole = "tool_result"
)

// Message is a generic, provider-agnostic transcript message.
type Message struct {
	Role      MessageRole            `json:"role"`
	Content   string                 `json:"content,omitempty"`
	Tool      string                 `json:"tool,omitempty"`
	Summary   string                 `json:"summary,omitempty"`
	Success   *bool                  `json:"success,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp string                 `json:"timestamp"`
}

// TranscriptResult holds paginated transcript messages.
type TranscriptResult struct {
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
	Returned int       `json:"returned"`
	Offset   int       `json:"offset"`
	HasMore  bool      `json:"has_more"`
}

// claudeEntry is the raw structure of a Claude .jsonl line.
type claudeEntry struct {
	Type      string          `json:"type"`
	Message   json.RawMessage `json:"message"`
	Timestamp string          `json:"timestamp"`
}

// claudeMessage is the inner message structure.
type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// claudeContentBlock is a single block in the content array.
type claudeContentBlock struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	Name      string                 `json:"name,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	Content   interface{}            `json:"content,omitempty"`
	IsError   bool                   `json:"is_error,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
}

// ParseClaudeTranscript reads a Claude .jsonl transcript file and returns
// generic messages with pagination. offset=0 means start from the end (newest).
func ParseClaudeTranscript(path string, limit, offset int) (*TranscriptResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	return parseClaude(f, maxLineBytes, limit, offset)
}

// parseClaude parses the .jsonl stream, skipping any entry longer than max.
func parseClaude(src io.Reader, max, limit, offset int) (*TranscriptResult, error) {
	var allMessages []Message
	r := bufio.NewReaderSize(src, 64*1024)

	for {
		line, oversized, err := readLine(r, max)
		// An entry longer than the cap is a tool result carrying a whole file,
		// and its tail is gone: parsing the head would only yield broken JSON.
		if len(line) > 0 && !oversized {
			var entry claudeEntry
			if json.Unmarshal(line, &entry) == nil {
				allMessages = append(allMessages, parseClaudeEntry(&entry)...)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read transcript: %w", err)
		}
	}

	total := len(allMessages)

	// Paginate from the end: offset=0 gets the last `limit` messages
	start := total - offset - limit
	end := total - offset
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}

	page := allMessages[start:end]

	return &TranscriptResult{
		Messages: page,
		Total:    total,
		Returned: len(page),
		Offset:   offset,
		HasMore:  start > 0,
	}, nil
}

// readLine returns the next line without its terminator, reporting whether it
// was longer than max bytes — in which case the excess is read and dropped.
// bufio.Scanner cannot do this: one over-long line ends the scan, and a single
// huge tool result would cost the caller the whole transcript.
func readLine(r *bufio.Reader, max int) (line []byte, oversized bool, err error) {
	total := 0
	for {
		chunk, readErr := r.ReadSlice('\n')
		more := errors.Is(readErr, bufio.ErrBufferFull)
		if !more {
			chunk = trimEOL(chunk)
		}
		total += len(chunk)
		if room := max - len(line); room > 0 {
			if room > len(chunk) {
				room = len(chunk)
			}
			line = append(line, chunk[:room]...)
		}
		if more {
			continue
		}
		return line, total > max, readErr
	}
}

// trimEOL drops a trailing newline and the carriage return before it.
func trimEOL(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte("\n"))
	return bytes.TrimSuffix(line, []byte("\r"))
}

// parseClaudeEntry converts a Claude .jsonl entry into generic Messages.
func parseClaudeEntry(entry *claudeEntry) []Message {
	// Skip non-message types
	switch entry.Type {
	case "user", "assistant":
		// process below
	default:
		return nil
	}

	if len(entry.Message) == 0 {
		return nil
	}

	var msg claudeMessage
	if err := json.Unmarshal(entry.Message, &msg); err != nil {
		return nil
	}

	// Content can be a string or an array of blocks
	var contentStr string
	if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
		// Simple string content (typically user messages)
		return []Message{{
			Role:      RoleUser,
			Content:   contentStr,
			Timestamp: entry.Timestamp,
		}}
	}

	// Array of content blocks
	var blocks []claudeContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil
	}

	var messages []Message
	for _, block := range blocks {
		switch block.Type {
		case "text":
			role := RoleAssistant
			if msg.Role == "user" {
				role = RoleUser
			}
			messages = append(messages, Message{
				Role:      role,
				Content:   block.Text,
				Timestamp: entry.Timestamp,
			})

		case "tool_use":
			messages = append(messages, Message{
				Role:      RoleToolUse,
				Tool:      block.Name,
				Summary:   summarizeToolInput(block.Name, block.Input),
				Metadata:  block.Input,
				Timestamp: entry.Timestamp,
			})

		case "tool_result":
			success := !block.IsError
			messages = append(messages, Message{
				Role:      RoleToolResult,
				Tool:      block.Name,
				Success:   &success,
				Timestamp: entry.Timestamp,
			})
		}
	}

	return messages
}

// summarizeToolInput creates a human-readable summary of a tool call.
func summarizeToolInput(toolName string, input map[string]interface{}) string {
	switch toolName {
	case "Read":
		if p, ok := input["file_path"].(string); ok {
			return shortPath(p)
		}
	case "Write":
		if p, ok := input["file_path"].(string); ok {
			return shortPath(p)
		}
	case "Edit":
		if p, ok := input["file_path"].(string); ok {
			return shortPath(p)
		}
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			if len(cmd) > 80 {
				return cmd[:80] + "..."
			}
			return cmd
		}
		if desc, ok := input["description"].(string); ok {
			return desc
		}
	case "Glob":
		if p, ok := input["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := input["pattern"].(string); ok {
			return p
		}
	case "Agent":
		if d, ok := input["description"].(string); ok {
			return d
		}
	}

	// Fallback: show first key=value
	for k, v := range input {
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:60] + "..."
		}
		return fmt.Sprintf("%s: %s", k, s)
	}

	return ""
}

// shortPath returns the last 2 path components.
func shortPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}
