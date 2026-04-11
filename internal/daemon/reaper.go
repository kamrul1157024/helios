package daemon

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

const staleThreshold = 2 * time.Minute

// reapStaleSessions marks sessions as stale when they appear dead,
// and backfills last_user_message for sessions missing it.
func reapStaleSessions(db *store.Store, tc *tmux.Client, sse *server.SSEBroadcaster) {
	sessions, err := db.ListSessions()
	if err != nil {
		return
	}

	for _, sess := range sessions {
		// Backfill last_user_message from transcript
		if sess.TranscriptPath != nil && *sess.TranscriptPath != "" {
			if msg := lastUserMessageFromTranscript(*sess.TranscriptPath); msg != "" {
				if sess.LastUserMessage == nil || *sess.LastUserMessage != msg {
					db.UpdateSessionLastUserMessage(sess.SessionID, msg)
				}
			}
		}

		switch sess.Status {
		case "compacting":
			// Compaction can take 5-6 minutes — only check pane liveness
			if sess.TmuxPane == nil || *sess.TmuxPane == "" {
				continue
			}
			if tc.HasPane(*sess.TmuxPane) {
				continue
			}

		case "active", "waiting_permission":
			// Time-based + pane check
			if sess.LastEventAt == nil {
				continue
			}
			lastEvent, err := time.Parse(time.RFC3339, *sess.LastEventAt)
			if err != nil {
				continue
			}
			if time.Since(lastEvent) < staleThreshold {
				continue
			}
			if sess.TmuxPane != nil && *sess.TmuxPane != "" {
				if tc.HasPane(*sess.TmuxPane) {
					continue
				}
			}

		default:
			continue
		}

		db.UpdateSessionStatus(sess.SessionID, "stale", "StaleReaper")
		sse.Broadcast(server.SSEEvent{
			Type: "session_status",
			Data: map[string]interface{}{
				"session_id": sess.SessionID,
				"status":     "stale",
			},
		})

		log.Printf("reaper: marked session %s as stale (status was %s)",
			sess.SessionID, sess.Status)
	}
}

// lastUserMessageFromTranscript reads a transcript JSONL file backward
// in chunks to find the last user message text. Tool-result entries can
// push the last real user prompt far from the end of the file, so we
// read in 64KB chunks working backward until we find one.
func lastUserMessageFromTranscript(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return ""
	}

	const chunkSize int64 = 65536
	fileSize := stat.Size()
	if fileSize == 0 {
		return ""
	}

	// Read backward in chunks until we find a user text message.
	for end := fileSize; end > 0; {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		readLen := end - start

		buf := make([]byte, readLen)
		f.ReadAt(buf, start)

		var lastMsg string
		scanner := bufio.NewScanner(strings.NewReader(string(buf)))
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var entry struct {
				Type    string          `json:"type"`
				Message json.RawMessage `json:"message"`
			}
			if json.Unmarshal([]byte(line), &entry) != nil || entry.Type != "user" {
				continue
			}
			var msg struct {
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(entry.Message, &msg) != nil {
				continue
			}
			var text string
			if json.Unmarshal(msg.Content, &text) == nil && text != "" {
				lastMsg = text
				continue
			}
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(msg.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						lastMsg = b.Text
						break
					}
				}
			}
		}

		if lastMsg != "" {
			return lastMsg
		}

		end = start
	}

	return ""
}
