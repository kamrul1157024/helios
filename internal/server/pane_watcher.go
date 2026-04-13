package server

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// PendingPane tracks a tmux pane waiting for Claude to start.
type PendingPane struct {
	PaneID    string
	CWD       string
	CreatedAt time.Time
	NotifSent bool // true if a claude.trust notification was already created for this pane
}

// PendingPaneMap is a thread-safe in-memory map of pending panes.
type PendingPaneMap struct {
	mu    sync.Mutex
	panes map[string]*PendingPane // paneID → PendingPane
}

func NewPendingPaneMap() *PendingPaneMap {
	return &PendingPaneMap{
		panes: make(map[string]*PendingPane),
	}
}

// Add registers a new pending pane.
func (m *PendingPaneMap) Add(paneID, cwd string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.panes[paneID] = &PendingPane{
		PaneID:    paneID,
		CWD:       cwd,
		CreatedAt: time.Now(),
	}
}

// Remove removes a pane from the pending map.
func (m *PendingPaneMap) Remove(paneID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.panes, paneID)
}

// RemoveByCWD removes the first pending pane matching the given CWD
// and returns its pane ID (empty string if not found).
func (m *PendingPaneMap) RemoveByCWD(cwd string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.panes {
		if p.CWD == cwd {
			delete(m.panes, id)
			return p.PaneID
		}
	}
	return ""
}

// List returns a snapshot of all pending panes.
func (m *PendingPaneMap) List() []PendingPane {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]PendingPane, 0, len(m.panes))
	for _, p := range m.panes {
		result = append(result, *p)
	}
	return result
}

// MarkNotifSent marks a pane as having had a trust notification sent.
func (m *PendingPaneMap) MarkNotifSent(paneID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.panes[paneID]; ok {
		p.NotifSent = true
	}
}

// trustPromptPatterns are strings that appear in Claude's workspace trust dialog.
var trustPromptPatterns = []string{
	"yes, i trust this folder",
	"quick safety check",
	"one you trust",
	"trust the files in this",
}

// containsTrustPrompt checks if tmux pane output contains a trust prompt.
func containsTrustPrompt(output string) bool {
	lower := strings.ToLower(output)
	for _, pattern := range trustPromptPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// StartPaneWatcher starts a goroutine that polls pending panes for trust prompts.
func StartPaneWatcher(shared *Shared) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			panes := shared.PendingPanes.List()
			if len(panes) == 0 {
				continue
			}

			for _, p := range panes {
				// Skip if notification already sent
				if p.NotifSent {
					continue
				}

				// Expire panes older than 2 minutes (Claude should have started by then)
				if time.Since(p.CreatedAt) > 2*time.Minute {
					shared.PendingPanes.Remove(p.PaneID)
					log.Printf("pane-watcher: expired stale pending pane %s", p.PaneID)
					continue
				}

				// Check if pane still exists
				if !shared.Tmux.HasPane(p.PaneID) {
					shared.PendingPanes.Remove(p.PaneID)
					log.Printf("pane-watcher: pane %s no longer exists, removing", p.PaneID)
					continue
				}

				// Capture pane content
				output, err := shared.Tmux.CapturePane(p.PaneID)
				if err != nil {
					continue
				}

				// Check for trust prompt
				if containsTrustPrompt(output) {
					log.Printf("pane-watcher: trust prompt detected in pane %s", p.PaneID)
					createTrustNotification(shared, &p, output)
					shared.PendingPanes.MarkNotifSent(p.PaneID)
				}
			}
		}
	}()
}

// createTrustNotification creates a claude.trust notification for a pane showing the trust dialog.
func createTrustNotification(shared *Shared, p *PendingPane, output string) {
	notifID := notifications.GenerateNotificationID()
	title := "Workspace trust required"
	detail := "Claude needs permission to access this workspace"

	payloadStr := `{"pane_id":"` + p.PaneID + `","cwd":"` + p.CWD + `"}`

	notif := &store.Notification{
		ID:      notifID,
		Source:  "claude",
		CWD:     p.CWD,
		Type:    "claude.trust",
		Status:  "pending",
		Title:   &title,
		Detail:  &detail,
		Payload: &payloadStr,
	}

	if err := shared.Mgr.CreateNotification(notif); err != nil {
		log.Printf("pane-watcher: failed to create trust notification: %v", err)
		return
	}

	shared.SSE.Broadcast(SSEEvent{Type: "notification", Data: notif})
}
