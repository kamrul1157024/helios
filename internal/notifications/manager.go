package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/kamrul1157024/helios/internal/store"
)

type Manager struct {
	db      *store.Store
	mu      sync.Mutex
	pending map[string]chan string // notification ID -> channel awaiting decision
}

func NewManager(db *store.Store) *Manager {
	return &Manager{
		db:      db,
		pending: make(map[string]chan string),
	}
}

// WaitForDecision registers a channel for a notification and blocks until resolved.
// Returns "approved" or "denied".
func (m *Manager) WaitForDecision(notifID string) (string, error) {
	ch := make(chan string, 1)

	m.mu.Lock()
	m.pending[notifID] = ch
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pending, notifID)
		m.mu.Unlock()
	}()

	decision := <-ch
	return decision, nil
}

// Resolve resolves a notification and unblocks any waiting hook handler.
func (m *Manager) Resolve(notifID, status, source string) error {
	if err := m.db.ResolveNotification(notifID, status, source); err != nil {
		return err
	}

	m.mu.Lock()
	ch, ok := m.pending[notifID]
	m.mu.Unlock()

	if ok {
		ch <- status
	}

	return nil
}

func (m *Manager) CreateNotification(n *store.Notification) error {
	return m.db.CreateNotification(n)
}

func (m *Manager) GetNotification(id string) (*store.Notification, error) {
	return m.db.GetNotification(id)
}

func (m *Manager) ListNotifications(status, nType string) ([]store.Notification, error) {
	return m.db.ListNotifications(status, nType)
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
	m.cancelPendingWithStatus(notifID, "dismissed", "claude")
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

func GenerateNotificationID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "notif-" + hex.EncodeToString(b)
}
