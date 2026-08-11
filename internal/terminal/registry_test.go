package terminal

import (
	"net"
	"os"
	"testing"
	"time"
)

// registryEnv is a Registry over a scratch HELIOS_DIR with stub hosts: a real
// unix listener per session, so Probe sees them as live, but no processes. It
// exercises the pool's bookkeeping without building or spawning anything.
type registryEnv struct {
	t   *testing.T
	dir string
	reg *Registry
}

func newRegistryEnv(t *testing.T) *registryEnv {
	t.Helper()
	// Not t.TempDir: its path is long enough to blow the 104-byte sun_path
	// limit once the run dir and socket name are appended.
	dir, err := os.MkdirTemp("/tmp", "helios-reg")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	reg := NewRegistry(dir, func(sessionID, cwd string, argv []string) error {
		t.Errorf("unexpected spawn of %s", sessionID)
		return nil
	})
	reg.MaxWarmRSS = 0 // isolate the count ceiling from the memory one
	return &registryEnv{t: t, dir: dir, reg: reg}
}

// add registers a stub host and backdates its activity by age, so tests can
// control the LRU order directly.
func (e *registryEnv) add(sessionID string, age time.Duration) {
	e.t.Helper()
	if err := os.MkdirAll(RunDir(e.dir), 0o700); err != nil {
		e.t.Fatalf("mkdir run: %v", err)
	}
	sock := SocketPath(e.dir, sessionID)
	l, err := net.Listen("unix", sock)
	if err != nil {
		e.t.Fatalf("listen %s: %v", sessionID, err)
	}
	e.t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	if _, err := e.reg.Adopt(sessionID, e.dir); err != nil {
		e.t.Fatalf("adopt %s: %v", sessionID, err)
	}
	e.reg.mu.Lock()
	e.reg.entries[sessionID].lastActive = time.Now().Add(-age)
	e.reg.mu.Unlock()
}

func (e *registryEnv) warm() map[string]bool {
	e.t.Helper()
	out := make(map[string]bool)
	for _, id := range e.reg.Warm() {
		out[id] = true
	}
	return out
}

// TestSweepKeepsPoolAtMaxWarm is the regression test for the reaper evicting a
// healthy session on every pass. evictForRoom took no headroom argument, so
// the sweep asked for a slot nobody wanted and the pool sat one below its
// ceiling forever: MaxWarm 3 meant 2 warm sessions.
func TestSweepKeepsPoolAtMaxWarm(t *testing.T) {
	e := newRegistryEnv(t)
	e.reg.MaxWarm = 3
	e.add("a", 3*time.Hour)
	e.add("b", 2*time.Hour)
	e.add("c", time.Hour)

	e.reg.Sweep()

	if got := len(e.reg.Warm()); got != 3 {
		t.Errorf("warm = %d, want 3: Sweep evicted a healthy session to make room nobody asked for", got)
	}
}

// TestEvictForRoomMakesSpaceForOne covers the other side: the caller that is
// about to spawn does need a free slot.
func TestEvictForRoomMakesSpaceForOne(t *testing.T) {
	e := newRegistryEnv(t)
	e.reg.MaxWarm = 3
	e.add("a", 3*time.Hour)
	e.add("b", 2*time.Hour)
	e.add("c", time.Hour)

	e.reg.evictForRoom(1)

	warm := e.warm()
	if len(warm) != 2 {
		t.Fatalf("warm = %v, want 2 so a third host can start", warm)
	}
	if warm["a"] {
		t.Error("least recently active session survived; the LRU picked wrong")
	}
}

// TestSweepDoesNotEvictOnAge pins the policy decision: sessions are closed by
// the user, never by the clock. Age alone must not cost a session its host,
// because an eviction loses the scrollback ring and `claude --resume` does not
// bring it back.
func TestSweepDoesNotEvictOnAge(t *testing.T) {
	e := newRegistryEnv(t)
	e.reg.MaxWarm = 10
	e.add("ancient", 30*24*time.Hour)
	e.add("recent", time.Minute)

	e.reg.Sweep()

	warm := e.warm()
	if !warm["ancient"] {
		t.Error("a month-old idle session was evicted; there is no idle TTL any more")
	}
	if !warm["recent"] {
		t.Error("recent session was evicted")
	}
}

// TestEvictForRoomSkipsWatchedSessions checks that a session somebody has open
// is never the victim, however stale its LRU position looks.
func TestEvictForRoomSkipsWatchedSessions(t *testing.T) {
	e := newRegistryEnv(t)
	e.reg.MaxWarm = 2
	e.add("watched", 5*time.Hour)
	e.add("idle", time.Hour)
	e.reg.InUse = func(sessionID string) bool { return sessionID == "watched" }

	e.reg.evictForRoom(1)

	warm := e.warm()
	if !warm["watched"] {
		t.Error("evicted the session with a live viewer")
	}
	if warm["idle"] {
		t.Error("expected the unwatched session to be evicted instead")
	}
}

// TestEvictForRoomStopsWhenEverythingIsWatched prefers going over the ceiling
// to killing a terminal someone is looking at.
func TestEvictForRoomStopsWhenEverythingIsWatched(t *testing.T) {
	e := newRegistryEnv(t)
	e.reg.MaxWarm = 1
	e.add("a", 5*time.Hour)
	e.add("b", time.Hour)
	e.reg.InUse = func(string) bool { return true }

	done := make(chan struct{})
	go func() {
		e.reg.evictForRoom(0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("evictForRoom did not return: it is looping over victims it refuses to evict")
	}

	if got := len(e.reg.Warm()); got != 2 {
		t.Errorf("warm = %d, want 2: both sessions are watched and neither may be evicted", got)
	}
}

// TestSweepForgetsDeadSockets keeps the half of the reaper that still earns
// its keep: noticing that a ptyhost died.
func TestSweepForgetsDeadSockets(t *testing.T) {
	e := newRegistryEnv(t)
	e.reg.MaxWarm = 10
	e.add("alive", time.Minute)
	e.add("dead", time.Minute)

	// Drop the socket out from under the registry, as a crashed host would.
	if err := os.Remove(SocketPath(e.dir, "dead")); err != nil {
		t.Fatalf("remove socket: %v", err)
	}

	e.reg.Sweep()

	warm := e.warm()
	if warm["dead"] {
		t.Error("session with a dead socket is still warm")
	}
	if !warm["alive"] {
		t.Error("live session was forgotten")
	}
}
