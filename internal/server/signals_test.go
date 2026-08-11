package server

import (
	"testing"
	"time"
)

func TestSignals_WaitReturnsOnFire(t *testing.T) {
	s := NewSessionSignals()
	sub := s.Await(SignalAgentReady, "sess-1")
	defer sub.Release()

	go s.Fire(SignalAgentReady, "sess-1")

	if !sub.Wait(2 * time.Second) {
		t.Error("waiter was not woken")
	}
}

// A signal that already arrived stays arrived for its subscriber: the caller
// may be busy spawning or typing when the hook comes in.
func TestSignals_FireBeforeWaitIsStillSeen(t *testing.T) {
	s := NewSessionSignals()
	sub := s.Await(SignalPromptSubmitted, "sess-1")
	defer sub.Release()

	s.Fire(SignalPromptSubmitted, "sess-1")

	if !sub.Wait(0) {
		t.Error("a signal fired before the wait was missed")
	}
}

// Nothing is remembered for a session nobody is listening to. A "ready" left
// over from an earlier boot would let the next caller type into a terminal
// that is not up yet, which is exactly the bug this guards.
func TestSignals_FireWithNoSubscriberIsNotRemembered(t *testing.T) {
	s := NewSessionSignals()

	s.Fire(SignalAgentReady, "sess-1")

	sub := s.Await(SignalAgentReady, "sess-1")
	defer sub.Release()
	if sub.Wait(20 * time.Millisecond) {
		t.Error("a stale signal was replayed")
	}
}

func TestSignals_OnlyTheNamedSessionWakes(t *testing.T) {
	s := NewSessionSignals()
	mine := s.Await(SignalAgentReady, "sess-1")
	defer mine.Release()

	s.Fire(SignalAgentReady, "sess-2")

	if mine.Wait(20 * time.Millisecond) {
		t.Error("another session's signal woke this waiter")
	}
}

func TestSignals_OnlyTheNamedSignalWakes(t *testing.T) {
	s := NewSessionSignals()
	ack := s.Await(SignalPromptSubmitted, "sess-1")
	defer ack.Release()

	s.Fire(SignalAgentReady, "sess-1")

	if ack.Wait(20 * time.Millisecond) {
		t.Error("the wrong signal woke this waiter")
	}
}

// Two clients can be sending to one session at once.
func TestSignals_EveryWaiterWakes(t *testing.T) {
	s := NewSessionSignals()
	a := s.Await(SignalPromptSubmitted, "sess-1")
	defer a.Release()
	b := s.Await(SignalPromptSubmitted, "sess-1")
	defer b.Release()

	s.Fire(SignalPromptSubmitted, "sess-1")

	if !a.Wait(2*time.Second) || !b.Wait(2*time.Second) {
		t.Error("both waiters should have woken")
	}
}

func TestSignals_ReleaseIsIdempotentAndSurvivesFire(t *testing.T) {
	s := NewSessionSignals()
	sub := s.Await(SignalAgentReady, "sess-1")

	s.Fire(SignalAgentReady, "sess-1")
	sub.Release()
	sub.Release()

	// The subscription was the only one, so its bookkeeping should be gone.
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiters) != 0 {
		t.Errorf("waiters = %v, want it emptied", s.waiters)
	}
}

// Timing out must not leave the subscription behind, or a long-lived daemon
// accumulates one per abandoned send.
func TestSignals_ReleaseAfterTimeoutLeavesNothing(t *testing.T) {
	s := NewSessionSignals()
	sub := s.Await(SignalAgentReady, "sess-1")

	sub.Wait(time.Millisecond)
	sub.Release()

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiters) != 0 {
		t.Errorf("waiters = %v, want it emptied", s.waiters)
	}
}
