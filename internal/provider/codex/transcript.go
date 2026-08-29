package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/transcript"
)

// maxLineBytes bounds one rollout entry. A single tool result can hold an
// entire file, so the cap is generous; what matters is that it is a cap.
const maxLineBytes = 16 << 20

// rolloutEntry is one line of a Codex rollout file.
//
// Ordinal counts from zero across every record, not only the rendered ones, so
// it is monotonic but not dense. That is all transcript.Message.Seq needs, and
// it saves counting lines the way the Claude reader has to.
type rolloutEntry struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   int             `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type rolloutPayload struct {
	Type    string          `json:"type"`
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   string          `json:"input,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// wrapperOpen captures the tag of a message that begins with an XML-ish
// element. isWrapperElement closes the match.
//
// Two regexes and a suffix check rather than one pattern with a backreference:
// Go's RE2 has no backreferences, so `</\1>` does not compile. It does in
// Python, which is where this rule was first tried out — a portability trap
// worth naming.
var wrapperOpen = regexp.MustCompile(`(?s)\A<([a-z_]+)>(.*)\z`)

// injectedHeading matches the context Codex prepends as a user turn, which is
// a Markdown heading rather than an element.
//
// A literal prefix, deliberately. The structural rule below catches anything
// wholly wrapped in a tag; this one cannot be generalised without risking a
// real user message that happens to open with a heading, and hiding something
// the user typed is far worse than showing something they did not. Extend it
// as more injections are found, one measured shape at a time.
var injectedHeading = regexp.MustCompile(`(?m)\A# (AGENTS|CLAUDE)\.md instructions for `)

// isInjectedContext reports whether a user-role record is something Codex put
// there rather than something the user typed.
func isInjectedContext(text string) bool {
	return isWrapperElement(text) || injectedHeading.MatchString(text)
}

// isWrapperElement reports whether text is wholly one XML-ish element.
//
// Codex injects context as user-role records — <environment_context> with the
// cwd and shell, <recommended_plugins>, and others. Rendering them would show
// the user saying things they never typed.
//
// Matched structurally rather than by tag name on purpose. A first pass listed
// the tags a scripted session produced, and real sessions turned out to inject
// a different one; a fixed list would have shipped and missed the common case.
func isWrapperElement(text string) bool {
	m := wrapperOpen.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	return strings.HasSuffix(strings.TrimSpace(m[2]), "</"+m[1]+">")
}

// blockPreamble is the header Codex puts above every exec result.
const blockPreamble = "Script completed\n"

func (p *Provider) LocateTranscript(sessionID string) string {
	return FindRollout(sessionID)
}

// ParseLine reads one rollout record.
//
// The sequence number is the record's own ordinal, so seq is ignored: a
// rollout numbers its records itself, and counting rendered messages instead
// would give two readers of the same file different numbers for one message.
func (p *Provider) ParseLine(line []byte, _ int) []transcript.Message {
	var entry rolloutEntry
	if json.Unmarshal(line, &entry) != nil {
		return nil
	}
	// Only response_item carries conversation. event_msg, turn_context,
	// world_state and session_meta are bookkeeping.
	if entry.Type != "response_item" {
		return nil
	}
	var payload rolloutPayload
	if json.Unmarshal(entry.Payload, &payload) != nil {
		return nil
	}
	m := messageFrom(&entry, &payload)
	if m == nil {
		return nil
	}
	return []transcript.Message{*m}
}

func messageFrom(entry *rolloutEntry, payload *rolloutPayload) *transcript.Message {
	switch payload.Type {
	case "message":
		text := strings.TrimSpace(blocksText(payload.Content))
		if text == "" {
			return nil
		}
		switch payload.Role {
		case "user":
			// Not every user record is the user.
			if isInjectedContext(text) {
				return nil
			}
			return &transcript.Message{
				Seq: entry.Ordinal, Role: transcript.RoleUser,
				Content: text, Timestamp: entry.Timestamp,
			}
		case "assistant":
			return &transcript.Message{
				Seq: entry.Ordinal, Role: transcript.RoleAssistant,
				Content: text, Timestamp: entry.Timestamp,
			}
		default:
			// "developer" holds the system prompt, the skills block and the
			// multi-agent preamble — thousands of words of framework text.
			return nil
		}

	case "custom_tool_call", "function_call":
		return &transcript.Message{
			Seq: entry.Ordinal, Role: transcript.RoleToolUse,
			Tool: payload.Name, Summary: summarizeToolInput(payload.Input),
			Timestamp: entry.Timestamp,
		}

	case "custom_tool_call_output", "function_call_output":
		text := strings.TrimPrefix(blocksText(payload.Output), blockPreamble)
		return &transcript.Message{
			Seq: entry.Ordinal, Role: transcript.RoleToolResult,
			Summary: truncate(strings.TrimSpace(text), 2000), Timestamp: entry.Timestamp,
		}
	}
	// "reasoning" among others. There is no role for it, and rendering raw
	// reasoning ids would be noise.
	return nil
}

// blocksText flattens a content array, or a bare string.
func blocksText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// execCommand pulls the shell command out of a Code Mode wrapper.
//
// With Code Mode active a shell call is recorded as JavaScript:
//
//	const r = await tools.exec_command({cmd:"echo hi", workdir:"/tmp"})
//
// A hosted session never needs this — its PreToolUse hook reports a normalised
// tool_name and tool_input. Discovery does: it reads transcripts of sessions
// helios never hosted, where no hook ever fired and this is the only record of
// what ran.
//
// A regex, and deliberately not a parser. It handles the literal form the CLI
// emits and gives up on anything computed, which is the right trade for a
// history panel: showing the JavaScript is better than showing nothing, and
// far better than pretending to have parsed it.
var execCommand = regexp.MustCompile(`(?s)\bcmd\s*:\s*"((?:[^"\\]|\\.)*)"`)

func summarizeToolInput(input string) string {
	if input == "" {
		return ""
	}
	if m := execCommand.FindStringSubmatch(input); m != nil {
		if unquoted, err := strconv.Unquote(`"` + m[1] + `"`); err == nil {
			return truncate(unquoted, 200)
		}
		return truncate(m[1], 200)
	}
	return truncate(strings.TrimSpace(input), 200)
}

// ==================== Discovery ====================

// sessionsDir is where Codex keeps rollout files:
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<ts>-<id>.jsonl
func sessionsDir() string { return filepath.Join(codexHome(), "sessions") }

// FindRollout locates a session's rollout file by the id in its name.
func FindRollout(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	var found string
	filepath.WalkDir(sessionsDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil //nolint:nilerr // a missing tree means no transcript, not a failure
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, sessionID+".jsonl") {
			found = path
		}
		return nil
	})
	return found
}

// Discover registers Codex sessions the user started outside Helios.
//
// Discovered sessions are cold by definition: a live one would already be in
// the database through its hooks.
func (p *Provider) Discover(db *store.Store) {
	root := sessionsDir()
	if _, err := os.Stat(root); err != nil {
		return
	}
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable day directory is not fatal
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		id := rolloutSessionID(name)
		if id == "" {
			return nil
		}
		// Two lookups, because a session helios launched is keyed by the id
		// helios minted and carries the codex id in resume_id. Checking only
		// the primary key would miss it and insert a second, terminated row
		// for a conversation already being tracked — on every daemon start.
		if existing, err := db.GetSession(id); err == nil && existing != nil {
			return nil
		}
		if existing, err := db.SessionByResumeID(id); err == nil && existing != nil {
			return nil
		}
		meta := readSessionMeta(path)
		if meta.cwd == "" || meta.source == execSource {
			return nil
		}
		sess := &store.Session{
			SessionID:      id,
			Source:         "codex",
			CWD:            meta.cwd,
			TranscriptPath: &path,
			Status:         "terminated",
			LastEvent:      strPtr("Discovered"),
		}
		if meta.model != "" {
			sess.Model = &meta.model
		}
		// INSERT OR IGNORE, not an upsert: a scan must never write over a
		// session that is alive. The GetSession check above is a fast path,
		// not a lock.
		if err := db.InsertDiscoveredSession(sess); err != nil {
			return nil //nolint:nilerr // one bad row must not stop the scan
		}
		// Separately, because the discovery insert does not carry it — and
		// without it `codex resume` has no id and the session cannot be woken
		// at all, which is most of the point of discovering it.
		if err := db.UpdateSessionResumeID(id, id); err != nil {
			return nil //nolint:nilerr
		}
		return nil
	})
}

// rolloutSessionID extracts the id from rollout-<timestamp>-<uuid>.jsonl.
func rolloutSessionID(name string) string {
	base := strings.TrimSuffix(strings.TrimPrefix(name, "rollout-"), ".jsonl")
	// The id is the last five dash-separated groups of a UUID.
	parts := strings.Split(base, "-")
	if len(parts) < 5 {
		return ""
	}
	return strings.Join(parts[len(parts)-5:], "-")
}

type sessionMeta struct {
	cwd    string
	model  string
	source string
}

// execSource is what a `codex exec` run records as its source; an interactive
// one records "cli".
const execSource = "exec"

// IsOneShot reports whether a rollout came from `codex exec` rather than the
// interactive CLI.
//
// A one-shot run is not a session anybody manages: it has no terminal to
// attach to and ends on its own. Helios makes these itself — naming a session
// costs one `codex exec` — and each one fired the session hooks and appeared
// in the list as a session whose first message was the title prompt.
//
// Codex draws the same line: `codex resume` hides them unless asked for with
// --include-non-interactive.
func IsOneShot(rolloutPath string) bool {
	return readSessionMeta(rolloutPath).source == execSource
}

// readSessionMeta reads the first line, which is always session_meta.
func readSessionMeta(path string) sessionMeta {
	f, err := os.Open(path)
	if err != nil {
		return sessionMeta{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	if !scanner.Scan() {
		return sessionMeta{}
	}
	var entry rolloutEntry
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil || entry.Type != "session_meta" {
		return sessionMeta{}
	}
	var meta struct {
		CWD    string `json:"cwd"`
		Model  string `json:"model"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(entry.Payload, &meta); err != nil {
		return sessionMeta{}
	}
	return sessionMeta{cwd: meta.CWD, model: meta.Model, source: meta.Source}
}
