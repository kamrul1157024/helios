package terminal

import (
	"fmt"
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

	reg := NewRegistry(dir, func(sessionID, cwd string, argv []string, env map[string]string) error {
		t.Errorf("unexpected spawn of %s", sessionID)
		return nil
	})
	return &registryEnv{t: t, dir: dir, reg: reg}
}

// add registers a stub host and backdates its activity by age, so tests can
// control the activity order directly.
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

// TestSweepDoesNotEvictOnAge pins the policy decision: sessions are closed by
// the user, never by the clock. Age alone must not cost a session its host,
// because an eviction loses the scrollback ring and `claude --resume` does not
// bring it back.
func TestSweepDoesNotEvictOnAge(t *testing.T) {
	e := newRegistryEnv(t)
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

// TestSweepForgetsDeadSockets keeps the half of the reaper that still earns
// its keep: noticing that a ptyhost died.
func TestSweepForgetsDeadSockets(t *testing.T) {
	e := newRegistryEnv(t)
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

// TestSweepHasNoCeiling pins the other half of the policy: the pool is
// unbounded. Memory is reported to the user, who closes what they are done
// with; nothing here reclaims it for them.
func TestSweepHasNoCeiling(t *testing.T) {
	e := newRegistryEnv(t)
	for i := 0; i < 25; i++ {
		e.add(fmt.Sprintf("sess-%d", i), time.Duration(i)*time.Hour)
	}

	e.reg.Sweep()

	if got := len(e.reg.Warm()); got != 25 {
		t.Errorf("warm = %d, want all 25 kept", got)
	}
}
