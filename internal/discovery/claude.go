package discovery

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

// claudeJSONLEntry is the minimal structure we need from a .jsonl line.
type claudeJSONLEntry struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

type claudeMessageMeta struct {
	Model string `json:"model"`
}

// DiscoverClaudeSessions scans ~/.claude/projects/ for .jsonl transcript files,
// extracts session metadata, cross-references with running tmux panes,
// and upserts sessions that don't already exist in the DB.
func DiscoverClaudeSessions(db *store.Store, tmuxClient *tmux.Client) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return
	}

	// Build a map of running Claude panes: CWD → paneID
	runningPanes := make(map[string]tmux.PaneProcess)
	if tmuxClient.Available() {
		panes, err := tmuxClient.ListClaudePanes()
		if err == nil {
			for _, p := range panes {
				if p.CWD != "" {
					runningPanes[p.CWD] = p
				}
			}
		}
	}

	// Scan all project directories
	projectDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return
	}

	discovered := 0
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		projectPath := filepath.Join(projectsDir, pd.Name())
		entries, err := os.ReadDir(projectPath)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}

			// Skip subagent transcripts (agent-*.jsonl)
			if strings.HasPrefix(entry.Name(), "agent-") {
				continue
			}

			jsonlPath := filepath.Join(projectPath, entry.Name())
			sessionID := strings.TrimSuffix(entry.Name(), ".jsonl")

			// Skip if session already exists in DB
			existing, err := db.GetSession(sessionID)
			if err == nil && existing != nil {
				continue
			}

			// Parse session metadata from the JSONL file
			sess := parseSessionFromJSONL(jsonlPath, sessionID)
			if sess == nil {
				continue
			}

			// Check if this session is currently running in tmux
			if pane, ok := runningPanes[sess.CWD]; ok {
				sess.TmuxPane = &pane.PaneID
				sess.TmuxPID = &pane.ClaudePID
				sess.Status = "idle" // conservative — hooks will correct this
			}

			if err := db.InsertDiscoveredSession(sess); err != nil {
				log.Printf("discovery: failed to upsert session %s: %v", sessionID, err)
				continue
			}
			discovered++
		}
	}

	if discovered > 0 {
		log.Printf("discovery: backfilled %d session(s) from Claude transcripts", discovered)
	}
}

// parseSessionFromJSONL reads the first few lines of a .jsonl file to extract
// session metadata: sessionId, cwd, timestamp, model.
func parseSessionFromJSONL(path, sessionID string) *store.Session {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256KB line buffer

	var cwd, firstTimestamp, lastTimestamp, model, lastUserMessage string
	linesRead := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry claudeJSONLEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		// Track timestamps
		if entry.Timestamp != "" {
			if firstTimestamp == "" {
				firstTimestamp = entry.Timestamp
			}
			lastTimestamp = entry.Timestamp
		}

		// Get CWD from first user entry
		if entry.CWD != "" && cwd == "" {
			cwd = entry.CWD
		}

		// Track last user message
		if entry.Type == "user" && len(entry.Message) > 0 {
			if msg := extractUserMessage(entry.Message); msg != "" {
				lastUserMessage = msg
			}
		}

		// Get model from first assistant entry
		if model == "" && entry.Type == "assistant" && len(entry.Message) > 0 {
			var meta claudeMessageMeta
			if json.Unmarshal(entry.Message, &meta) == nil && meta.Model != "" {
				model = meta.Model
			}
		}

		// Stop after we have what we need or after reading 50 lines
		linesRead++
		if cwd != "" && model != "" && lastUserMessage != "" {
			break
		}
		if linesRead > 50 {
			break
		}
	}

	if cwd == "" && firstTimestamp == "" {
		return nil // not enough info
	}

	// Get last timestamp and last user message from file tail
	if lt := lastTimestampFromFile(path); lt != "" {
		lastTimestamp = lt
	}
	if lum := lastUserMessageFromFile(path); lum != "" {
		lastUserMessage = lum
	}

	sess := &store.Session{
		SessionID: sessionID,
		Source:    "claude",
		CWD:       cwd,
		Project:   filepath.Base(cwd),
		Status:    "terminated", // default — assume terminated unless tmux says otherwise
		LastEvent: strPtr("Discovered"),
	}

	transcriptPath := path
	sess.TranscriptPath = &transcriptPath

	if model != "" {
		sess.Model = &model
	}

	if lastTimestamp != "" {
		sess.LastEventAt = &lastTimestamp
	}

	if lastUserMessage != "" {
		sess.LastUserMessage = &lastUserMessage
	}

	return sess
}

// lastTimestampFromFile reads the last few kilobytes of a file to find
// the last timestamp, giving us the most recent activity time.
func lastTimestampFromFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return ""
	}

	// Read last 8KB
	readSize := int64(8192)
	offset := stat.Size() - readSize
	if offset < 0 {
		offset = 0
		readSize = stat.Size()
	}

	buf := make([]byte, readSize)
	f.ReadAt(buf, offset)

	// Find last timestamp in the tail
	var lastTS string
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Timestamp != "" {
			lastTS = entry.Timestamp
		}
	}

	return lastTS
}

// extractUserMessage extracts the text content from a Claude user message JSON.
func extractUserMessage(raw json.RawMessage) string {
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return ""
	}

	// Content can be a simple string
	var text string
	if json.Unmarshal(msg.Content, &text) == nil {
		return text
	}

	// Or an array of content blocks — extract text blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(msg.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}

	return ""
}

// lastUserMessageFromFile reads the tail of a JSONL file to find the last user message.
func lastUserMessageFromFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return ""
	}

	// Read last 16KB to find user messages
	readSize := int64(16384)
	offset := stat.Size() - readSize
	if offset < 0 {
		offset = 0
		readSize = stat.Size()
	}

	buf := make([]byte, readSize)
	f.ReadAt(buf, offset)

	var lastMsg string
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry claudeJSONLEntry
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "user" {
			if msg := extractUserMessage(entry.Message); msg != "" {
				lastMsg = msg
			}
		}
	}

	return lastMsg
}

func strPtr(s string) *string {
	return &s
}
