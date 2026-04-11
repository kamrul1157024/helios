package daemon

import (
	"log"
	"time"

	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

const staleThreshold = 2 * time.Minute

// reapStaleSessions marks sessions as stale when they appear dead.
// For active/waiting_permission: stale if no hook activity for 2 min AND tmux pane gone.
// For compacting: stale only if tmux pane gone (no time check — compaction can take 5-6 min).
func reapStaleSessions(db *store.Store, tc *tmux.Client, sse *server.SSEBroadcaster) {
	sessions, err := db.ListSessions()
	if err != nil {
		return
	}

	for _, sess := range sessions {
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
