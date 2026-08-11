package server

import (
	"sync"
	"time"
)

// Signals a caller can wait for. Each is reported by a hook the agent calls,
// so they arrive on a different connection than the request waiting on them.
const (
	// SignalAgentReady fires when an agent's SessionStart hook runs, which is
	// the first moment it is running rather than merely spawned.
	SignalAgentReady = "agent_ready"
	// SignalPromptSubmitted fires when an agent's UserPromptSubmit hook runs,
	// which is proof a prompt reached it and was accepted.
	SignalPromptSubmitted = "prompt_submitted"
)

// SessionSignals lets a request block until a session reports something.
//
// Nothing is remembered: a signal fired with no one waiting is gone. That is
// deliberate — a stale "ready" from an earlier boot would let the next caller
// through instantly, which is the bug this exists to prevent. Callers must
// therefore subscribe before doing the thing they expect a report from.
type SessionSignals struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
}

func NewSessionSignals() *SessionSignals {
	return &SessionSignals{waiters: make(map[string]map[chan struct{}]struct{})}
}

// Await subscribes to a signal for one session. The subscription is live from
// this call, so anything fired while the caller is busy is still caught. It
// must be released.
func (s *SessionSignals) Await(signal, sessionID string) *Subscription {
	key := signal + "\x00" + sessionID
	ch := make(chan struct{})

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waiters[key] == nil {
		s.waiters[key] = make(map[chan struct{}]struct{})
	}
	s.waiters[key][ch] = struct{}{}
	return &Subscription{signals: s, key: key, ch: ch}
}

// Fire wakes everything waiting on a signal for a session.
func (s *SessionSignals) Fire(signal, sessionID string) {
	key := signal + "\x00" + sessionID

	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.waiters[key] {
		close(ch)
		delete(s.waiters[key], ch)
	}
	delete(s.waiters, key)
}

// Subscription is one caller's interest in one signal.
type Subscription struct {
	signals *SessionSignals
	key     string
	ch      chan struct{}
}

// Wait reports whether the signal arrived within d. A signal that already
// arrived returns true immediately, however long ago it was.
func (sub *Subscription) Wait(d time.Duration) bool {
	// Checked before the timer is even armed: with both ready, a select would
	// pick between them at random.
	select {
	case <-sub.ch:
		return true
	default:
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-sub.ch:
		return true
	case <-timer.C:
		return false
	}
}

// Release drops the subscription. Safe to call after the signal fired, and
// safe to call twice.
func (sub *Subscription) Release() {
	sub.signals.mu.Lock()
	defer sub.signals.mu.Unlock()
	if set := sub.signals.waiters[sub.key]; set != nil {
		delete(set, sub.ch)
		if len(set) == 0 {
			delete(sub.signals.waiters, sub.key)
		}
	}
}
