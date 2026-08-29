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
	// Seq is the message's position in the transcript, counted from the start
	// of the file. Transcripts are append-only, so a seq refers to the same
	// message for as long as the epoch it was served under holds.
	Seq       int                    `json:"seq"`
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
	// Epoch identifies the parse the seq numbers belong to. It changes when a
	// transcript stops being an extension of what was read before — a fork, a
	// new file at the same path, a truncation.
	Epoch string `json:"epoch,omitempty"`
	// EpochChanged answers a delta request that named a stale epoch. The
	// messages are then a fresh newest page rather than a delta, and the
	// caller has to replace what it holds instead of appending.
	EpochChanged bool `json:"epoch_changed,omitempty"`
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
	allMessages, _, err := parseSegment(src, max, 0)
	if err != nil {
		return nil, err
	}
	return page(allMessages, limit, offset), nil
}

// parseSegment parses complete lines out of src, numbering messages from
// firstSeq, and reports how many bytes it consumed.
//
// A transcript is read while it is being written, so the last line can be half
// there. Only bytes up to and including the final newline are counted as
// consumed: an unterminated tail is left for the next read rather than parsed
// into a truncated message that would then be cached forever.
func parseSegment(src io.Reader, max, firstSeq int) (msgs []Message, consumed int64, err error) {
	r := bufio.NewReaderSize(src, 64*1024)
	seq := firstSeq

	for {
		line, oversized, raw, terminated, readErr := readSegmentLine(r, max)
		if terminated {
			consumed += raw
		}
		// An entry longer than the cap is a tool result carrying a whole file,
		// and its tail is gone: parsing the head would only yield broken JSON.
		if terminated && len(line) > 0 && !oversized {
			var entry claudeEntry
			if json.Unmarshal(line, &entry) == nil {
				for _, m := range parseClaudeEntry(&entry) {
					m.Seq = seq
					seq++
					msgs = append(msgs, m)
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return msgs, consumed, nil
			}
			return nil, 0, fmt.Errorf("read transcript: %w", readErr)
		}
	}
}

// page slices a window off the end of msgs: offset=0 gets the last `limit`.
// Paginate is the shared paging contract, exported so every provider's parser
// answers a given limit and offset identically.
//
// It was briefly reimplemented in the codex parser, where limit <= 0 came to
// mean "everything" rather than "nothing" — the same API call would have
// returned a whole transcript from one provider and an empty page from the
// other.
func Paginate(msgs []Message, limit, offset int) *TranscriptResult {
	return page(msgs, limit, offset)
}

func page(msgs []Message, limit, offset int) *TranscriptResult {
	total := len(msgs)

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

	return &TranscriptResult{
		Messages: msgs[start:end],
		Total:    total,
		Returned: end - start,
		Offset:   offset,
		HasMore:  start > 0,
	}
}

// readSegmentLine returns the next line without its terminator, reporting
// whether it was longer than max bytes — in which case the excess is read and
// dropped — how many raw bytes it occupied, and whether it ended in a newline.
//
// bufio.Scanner cannot do this: one over-long line ends the scan, and a single
// huge tool result would cost the caller the whole transcript.
func readSegmentLine(r *bufio.Reader, max int) (line []byte, oversized bool, raw int64, terminated bool, err error) {
	total := 0
	for {
		chunk, readErr := r.ReadSlice('\n')
		more := errors.Is(readErr, bufio.ErrBufferFull)
		raw += int64(len(chunk))
		if !more {
			terminated = len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
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
		return line, total > max, raw, terminated, readErr
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
