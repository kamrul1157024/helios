package desktop

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// TicketTTL is how long a ticket stays good. A client asks for one right
// before it opens the tunnel, so anything longer only widens the window in
// which a leaked URL is worth something.
const TicketTTL = 30 * time.Second

// Tickets stand in for the bearer token on the tunnel. A browser WebSocket and
// a WebView cannot set an Authorization header, and a JWT in a URL ends up in
// logs, so the client trades its token for a ticket that works once (D2).
type Tickets struct {
	mu        sync.Mutex
	issued    map[string]ticket
	now       func() time.Time
	lastSweep time.Time
}

type ticket struct {
	expires time.Time
	device  string
}

// NewTickets returns an empty ticket book.
func NewTickets() *Tickets {
	return &Tickets{issued: make(map[string]ticket), now: time.Now}
}

// Issue mints a ticket for a device and returns it with its expiry.
func (t *Tickets) Issue(device string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("mint ticket: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(raw)

	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.sweep(now)
	expires := now.Add(TicketTTL)
	t.issued[value] = ticket{expires: expires, device: device}
	return value, expires, nil
}

// Redeem spends a ticket. It reports the device the ticket was issued to and
// whether the ticket was good; a ticket is gone after its first redemption,
// good or late.
func (t *Tickets) Redeem(value string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tk, ok := t.issued[value]
	if !ok {
		return "", false
	}
	delete(t.issued, value)
	if !t.now().Before(tk.expires) {
		return "", false
	}
	return tk.device, true
}

// sweep drops expired tickets so a client that asks and never connects does
// not grow the map. It runs on issue, at most once per TTL.
func (t *Tickets) sweep(now time.Time) {
	if now.Sub(t.lastSweep) < TicketTTL {
		return
	}
	t.lastSweep = now
	for k, tk := range t.issued {
		if !now.Before(tk.expires) {
			delete(t.issued, k)
		}
	}
}
