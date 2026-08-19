package sysinfo

import (
	"testing"
	"time"
)

// The numbers are shown to a user deciding what to close, so a plausibility
// check is the most a test can do: the real values differ every run.
func TestReadReportsPlausibleNumbers(t *testing.T) {
	s := Read()

	if s.MemoryTotal <= 0 {
		t.Fatalf("memory total = %d, want the machine's physical memory", s.MemoryTotal)
	}
	if s.MemoryUsed <= 0 || s.MemoryUsed > s.MemoryTotal {
		t.Errorf("memory used = %d, want between 0 and %d", s.MemoryUsed, s.MemoryTotal)
	}
	if s.Load < 0 || s.Load > 100 {
		t.Errorf("load = %f, want a sane per-core fraction", s.Load)
	}
}

func TestReadIsCached(t *testing.T) {
	first := Read()

	mu.Lock()
	cached.MemoryUsed = 1
	mu.Unlock()

	if got := Read().MemoryUsed; got != 1 {
		t.Errorf("second read = %d, want the cached 1 — measuring costs a fork per field", got)
	}

	mu.Lock()
	read = time.Now().Add(-2 * ttl)
	mu.Unlock()

	if got := Read().MemoryUsed; got == 1 {
		t.Errorf("reading never went stale; want a fresh measurement near %d", first.MemoryUsed)
	}
}
