package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

// Decision carries the user's response back to the blocking hook handler.
type Decision struct {
	Status   string          `json:"status"`             // "approved", "denied", "answered", "dismissed", "timeout"
	Response json.RawMessage `json:"response,omitempty"` // opaque — stored in notification.response
}

type Manager struct {
	db        *store.Store
	mu        sync.Mutex
	pending   map[string]chan Decision                 // notification ID -> channel awaiting decision
	broadcast func(eventType string, data interface{}) // SSE fan-out; nil until wired
}

func NewManager(db *store.Store) *Manager {
	return &Manager{
		db:      db,
		pending: make(map[string]chan Decision),
	}
}

// SetBroadcaster installs the fan-out used to announce resolutions.
//
// A notification's status lives here, in the database, and clients are only
// ever a view of it. That only holds if every resolution announces itself, so
// the announcement lives with the write rather than at each call site — which
// is what let the hook timeout path mark a notification resolved and tell
// nobody, leaving an approval on every phone and tray that nothing would ever
// take down.
func (m *Manager) SetBroadcaster(fn func(eventType string, data interface{})) {
	m.mu.Lock()
	m.broadcast = fn
	m.mu.Unlock()
}

// Register reserves the pending slot for a notification. Call it before the
// notification is published to clients: a decision can come back before the
// handler reaches WaitForDecision, and without the slot Resolve has nowhere to
// deliver it and the handler blocks until it times out.
func (m *Manager) Register(notifID string) {
	m.mu.Lock()
	if _, ok := m.pending[notifID]; !ok {
		m.pending[notifID] = make(chan Decision, 1)
	}
	m.mu.Unlock()
}

// WaitForDecision blocks until the notification is resolved. It adopts the slot
// left by Register, so a decision that arrived early is already buffered.
func (m *Manager) WaitForDecision(notifID string) (Decision, error) {
	m.mu.Lock()
	ch, ok := m.pending[notifID]
	if !ok {
		ch = make(chan Decision, 1)
		m.pending[notifID] = ch
	}
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

// Resolve resolves a notification, unblocks any waiting hook handler, and
// tells every client it is gone.
//
// Returns store.ErrAlreadyResolved when another surface got there first; the
// row is untouched and nothing is announced twice.
func (m *Manager) Resolve(notifID string, decision Decision, source string) error {
	// Store response in the notification record
	if len(decision.Response) > 0 {
		if err := m.db.UpdateNotificationResponse(notifID, string(decision.Response)); err != nil {
			return fmt.Errorf("store response for %s: %w", notifID, err)
		}
	}

	if err := m.db.ResolveNotification(notifID, decision.Status, source); err != nil {
		return err
	}

	m.unblock(notifID, decision)
	m.announce(notifID, decision.Status, source)
	return nil
}

// ResolveSession resolves every pending notification for a session, as when
// the agent answers in its own terminal and the Stop hook fires.
func (m *Manager) ResolveSession(sessionID, status, source string) ([]string, error) {
	ids, err := m.db.ResolveSessionNotifications(sessionID, status, source)
	if err != nil {
		return nil, fmt.Errorf("resolve notifications for %s: %w", sessionID, err)
	}
	m.announceAll(ids, status, source)
	return ids, nil
}

// ResolveSessionByType is ResolveSession narrowed to one notification type, so
// clearing a session's answered question does not also clear a permission
// request the same session is still waiting on.
func (m *Manager) ResolveSessionByType(sessionID, nType, status, source string) ([]string, error) {
	ids, err := m.db.ResolveSessionNotificationsByType(sessionID, nType, status, source)
	if err != nil {
		return nil, fmt.Errorf("resolve %s notifications for %s: %w", nType, sessionID, err)
	}
	m.announceAll(ids, status, source)
	return ids, nil
}

func (m *Manager) announceAll(ids []string, status, source string) {
	for _, id := range ids {
		m.unblock(id, Decision{Status: status})
		m.announce(id, status, source)
	}
}

// unblock hands a decision to a waiting hook handler, if one is still there.
// Non-blocking: a repeat resolve for the same notification must not wedge the
// caller behind the already-buffered decision.
func (m *Manager) unblock(notifID string, decision Decision) {
	m.mu.Lock()
	ch, ok := m.pending[notifID]
	m.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- decision:
	default:
	}
}

func (m *Manager) announce(notifID, status, source string) {
	m.mu.Lock()
	fn := m.broadcast
	m.mu.Unlock()
	if fn == nil {
		return
	}
	fn("notification_resolved", map[string]string{"id": notifID, "action": status, "source": source})
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
	// Hand the waiter a dismissal rather than closing the channel: the slot may
	// have been registered before the handler blocks on it, and the decision has
	// to survive that gap. The waiter deletes the slot on its way out.
	m.unblock(notifID, Decision{Status: "dismissed"})

	if err := m.db.ResolveNotification(notifID, status, source); err != nil {
		// Already resolved is the ordinary race: another surface got there
		// first and announced it then. Anything else means the row did not
		// change, so there is nothing to announce either.
		if !errors.Is(err, store.ErrAlreadyResolved) {
			log.Printf("notifications: cancel %s: %v", notifID, err)
		}
		return
	}
	m.announce(notifID, status, source)
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
