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

	// Fetch all live pane IDs once — avoids one subprocess call per session.
	livePanes := tc.LivePanes()
	hasPane := func(id string) bool {
		if livePanes == nil {
			return false
		}
		_, ok := livePanes[id]
		return ok
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
		case "terminated":
			// Clean up any lingering tmux pane left over from before this logic existed.
			if sess.TmuxPane == nil || *sess.TmuxPane == "" {
				continue
			}
			if hasPane(*sess.TmuxPane) {
				tc.KillPane(*sess.TmuxPane)
			}
			db.ClearSessionTmuxPane(sess.SessionID)
			log.Printf("reaper: cleared orphaned pane %s for terminated session %s",
				*sess.TmuxPane, sess.SessionID)
			continue

		case "compacting":
			// Compaction can take 5-6 minutes — only check pane liveness
			if sess.TmuxPane == nil || *sess.TmuxPane == "" {
				continue
			}
			if hasPane(*sess.TmuxPane) {
				continue
			}

		case "active", "waiting_permission":
			// Check pane content — if Claude is showing the idle prompt with no
			// spinner, the generation finished but the stop hook didn't fire.
			if sess.TmuxPane != nil && *sess.TmuxPane != "" && hasPane(*sess.TmuxPane) {
				if content, err := tc.CapturePane(*sess.TmuxPane); err == nil {
					if claudeIsIdle(content) {
						db.UpdateSessionStatus(sess.SessionID, "idle", "PaneIdleDetected")
						sse.Broadcast(server.SSEEvent{
							Type: "session_status",
							Data: map[string]interface{}{
								"session_id": sess.SessionID,
								"status":     "idle",
							},
						})
						log.Printf("reaper: detected idle pane for session %s, updated to idle", sess.SessionID)
						continue
					}
				}
			}

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
				if hasPane(*sess.TmuxPane) {
					continue
				}
			}

		default:
			continue
		}

		if sess.TmuxPane != nil && *sess.TmuxPane != "" {
			tc.KillPane(*sess.TmuxPane)
		}
		db.UpdateSessionStatus(sess.SessionID, "terminated", "StaleReaper")
		sse.Broadcast(server.SSEEvent{
			Type: "session_status",
			Data: map[string]interface{}{
				"session_id": sess.SessionID,
				"status":     "terminated",
			},
		})

		log.Printf("reaper: terminated stale session %s (status was %s)",
			sess.SessionID, sess.Status)
	}
}

// claudeIsIdle returns true if the pane shows Claude's idle input prompt.
// When generating, Claude shows a verb line ending with "…" above the prompt.
// When idle, no such line exists — only the bare "❯" prompt remains.
func claudeIsIdle(content string) bool {
	if !strings.Contains(content, "❯") {
		return false
	}
	// When generating, Claude shows a verb line containing "…" (U+2026).
	if strings.Contains(content, "…") {
		return false
	}
	return true
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
