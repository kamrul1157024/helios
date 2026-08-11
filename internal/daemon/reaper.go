package daemon

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
)

// reapStaleSessions drops terminals that have died and backfills
// last_user_message from transcripts.
//
// Losing a terminal does not end a session. Under the warm pool cold is a
// normal state: the conversation still exists and `claude --resume` brings it
// back, so the session keeps its status and the clients just see it go cold.
// Only claude itself, through the SessionEnd hook, terminates a session.
func reapStaleSessions(db *store.Store, be backend.Backend, sse *server.SSEBroadcaster) {
	for _, sessionID := range be.Sweep() {
		sse.Broadcast(server.SSEEvent{
			Type: "session_updated",
			Data: map[string]interface{}{
				"session_id": sessionID,
			},
		})
		log.Printf("reaper: session %s went cold (terminal died)", sessionID)
	}

	// Backfill last_user_message from transcripts.
	sessions, err := db.ListSessions()
	if err != nil {
		return
	}
	for _, sess := range sessions {
		if sess.TranscriptPath == nil || *sess.TranscriptPath == "" {
			continue
		}
		if msg := lastUserMessageFromTranscript(*sess.TranscriptPath); msg != "" {
			if sess.LastUserMessage == nil || *sess.LastUserMessage != msg {
				db.UpdateSessionLastUserMessage(sess.SessionID, msg)
			}
		}
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
