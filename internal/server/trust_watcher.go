package server

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// pendingTTL is how long a session stays watched for the trust dialog. Claude
// shows it within seconds of launch or not at all.
const pendingTTL = 2 * time.Minute

// PendingSession is a session whose terminal has started but whose agent has
// not reported in yet. Only these are watched for the workspace-trust dialog.
type PendingSession struct {
	SessionID string
	CWD       string
	CreatedAt time.Time
	NotifSent bool
}

// PendingSessionMap is a thread-safe set of sessions awaiting first contact.
type PendingSessionMap struct {
	mu       sync.Mutex
	sessions map[string]*PendingSession
}

func NewPendingSessionMap() *PendingSessionMap {
	return &PendingSessionMap{sessions: make(map[string]*PendingSession)}
}

// Add registers a session that has just been launched.
func (m *PendingSessionMap) Add(sessionID, cwd string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sessionID] = &PendingSession{
		SessionID: sessionID,
		CWD:       cwd,
		CreatedAt: time.Now(),
	}
}

// Remove stops watching a session.
func (m *PendingSessionMap) Remove(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

// List returns a snapshot of the watched sessions.
func (m *PendingSessionMap) List() []PendingSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PendingSession, 0, len(m.sessions))
	for _, p := range m.sessions {
		out = append(out, *p)
	}
	return out
}

// Get returns a watched session.
func (m *PendingSessionMap) Get(sessionID string) (PendingSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.sessions[sessionID]
	if !ok {
		return PendingSession{}, false
	}
	return *p, true
}

// MarkNotifSent records that a trust notification already went out, so the
// same dialog does not produce one per screen update.
func (m *PendingSessionMap) MarkNotifSent(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.sessions[sessionID]; ok {
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

// containsTrustPrompt reports whether rendered screen text shows the dialog.
//
// The input must be emulator output, never the raw PTY stream: Claude Code
// positions text with cursor-column jumps, so these phrases never appear
// contiguously in the bytes it writes.
func containsTrustPrompt(screen string) bool {
	lower := strings.ToLower(screen)
	for _, pattern := range trustPromptPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// screenNotifier is implemented by backends that can push screen changes. The
// host backend can; a polling backend would not, which is why this is checked
// at runtime rather than required by Backend.
type screenNotifier interface {
	OnUpdate(func(sessionID string))
}

// StartTrustWatcher watches freshly launched sessions for the workspace-trust
// dialog and raises a notification when one appears.
//
// It is driven by screen updates rather than a timer: the daemon already holds
// a live mirror of every session, so there is nothing to poll. A slow ticker
// remains only to expire sessions that never showed the dialog.
func StartTrustWatcher(shared *Shared) {
	if n, ok := shared.Backend.(screenNotifier); ok {
		n.OnUpdate(func(sessionID string) {
			checkTrustPrompt(shared, sessionID)
		})
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			for _, p := range shared.Pending.List() {
				if time.Since(p.CreatedAt) > pendingTTL {
					shared.Pending.Remove(p.SessionID)
					log.Printf("trust-watcher: stopped watching %s", p.SessionID)
					continue
				}
				if !shared.Backend.Alive(p.SessionID) {
					shared.Pending.Remove(p.SessionID)
				}
			}
		}
	}()
}

// checkTrustPrompt inspects one session's screen. It is called on the mirror's
// read goroutine, so it must stay cheap: the early returns below mean a
// session that is not pending costs one map lookup.
func checkTrustPrompt(shared *Shared, sessionID string) {
	p, ok := shared.Pending.Get(sessionID)
	if !ok || p.NotifSent {
		return
	}
	screen, err := shared.Backend.Capture(sessionID)
	if err != nil || !containsTrustPrompt(screen) {
		return
	}
	log.Printf("trust-watcher: trust prompt detected in session %s", sessionID)
	shared.Pending.MarkNotifSent(sessionID)
	createTrustNotification(shared, &p)
}

// createTrustNotification raises a claude.trust notification for a session
// showing the trust dialog.
func createTrustNotification(shared *Shared, p *PendingSession) {
	title := "Workspace trust required"
	detail := "Claude needs permission to access this workspace"
	payloadStr := `{"session_id":"` + p.SessionID + `","cwd":"` + p.CWD + `"}`

	notif := &store.Notification{
		ID:      notifications.GenerateNotificationID(),
		Source:  "claude",
		CWD:     p.CWD,
		Type:    "claude.trust",
		Status:  "pending",
		Title:   &title,
		Detail:  &detail,
		Payload: &payloadStr,
	}

	if err := shared.Mgr.CreateNotification(notif); err != nil {
		log.Printf("trust-watcher: failed to create trust notification: %v", err)
		return
	}
	shared.SSE.Broadcast(SSEEvent{Type: "notification", Data: notif})
}
