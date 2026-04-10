package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

// Decision carries the user's response back to the blocking hook handler.
type Decision struct {
	Status   string          `json:"status"`            // "approved", "denied", "answered", "dismissed", "timeout"
	Response json.RawMessage `json:"response,omitempty"` // opaque — stored in notification.response
}

type Manager struct {
	db      *store.Store
	mu      sync.Mutex
	pending map[string]chan Decision // notification ID -> channel awaiting decision
}

func NewManager(db *store.Store) *Manager {
	return &Manager{
		db:      db,
		pending: make(map[string]chan Decision),
	}
}

// WaitForDecision registers a channel for a notification and blocks until resolved.
func (m *Manager) WaitForDecision(notifID string) (Decision, error) {
	ch := make(chan Decision, 1)

	m.mu.Lock()
	m.pending[notifID] = ch
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pending, notifID)
		m.mu.Unlock()
	}()

	decision, ok := <-ch
	if !ok {
		return Decision{Status: "dismissed"}, nil
	}
	return decision, nil
}

// Resolve resolves a notification and unblocks any waiting hook handler.
func (m *Manager) Resolve(notifID string, decision Decision, source string) error {
	// Store response in the notification record
	if len(decision.Response) > 0 {
		m.db.UpdateNotificationResponse(notifID, string(decision.Response))
	}

	if err := m.db.ResolveNotification(notifID, decision.Status, source); err != nil {
		return err
	}

	m.mu.Lock()
	ch, ok := m.pending[notifID]
	m.mu.Unlock()

	if ok {
		ch <- decision
	}

	return nil
}

func (m *Manager) CreateNotification(n *store.Notification) error {
	return m.db.CreateNotification(n)
}

func (m *Manager) GetNotification(id string) (*store.Notification, error) {
	return m.db.GetNotification(id)
}

func (m *Manager) ListNotifications(source, status, nType string) ([]store.Notification, error) {
	return m.db.ListNotifications(source, status, nType)
}

func (m *Manager) HasPending(notifID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pending[notifID]
	return ok
}

func (m *Manager) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

func (m *Manager) CancelPending(notifID string) {
	m.cancelPendingWithStatus(notifID, "timeout", "system")
}

func (m *Manager) CancelPendingFromClaude(notifID string) {
	m.cancelPendingWithStatus(notifID, "resolved", "claude")
}

func (m *Manager) cancelPendingWithStatus(notifID, status, source string) {
	m.mu.Lock()
	ch, ok := m.pending[notifID]
	if ok {
		delete(m.pending, notifID)
	}
	m.mu.Unlock()

	if ok {
		close(ch)
	}

	m.db.ResolveNotification(notifID, status, source)
}

// Notification retention — keep only the latest N resolved notifications.
const maxNotifications = 200
const cleanupInterval = 5 * time.Minute

func (m *Manager) StartCleanup() {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			m.db.TruncateNotifications(maxNotifications)
		}
	}()
}

func GenerateNotificationID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "notif-" + hex.EncodeToString(b)
}
