package daemon

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The daemon has to leave the session of whatever started it. In its own
// session no terminal can signal it: the setup TUI's Ctrl-C reached the daemon
// and killed it, and because the supervisor runs in the daemon's own process
// there was nothing left to bring it back.
func TestSpawnDetachedLeavesTheCallersSession(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep available")
	}

	pid, err := SpawnDetached(sleep, []string{"5"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)

	// The child calls setsid as it starts, so its own session id is not
	// readable the instant StartProcess returns.
	var sid int
	for i := 0; i < 50; i++ {
		if sid, err = syscall.Getsid(pid); err == nil && sid != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("getsid(%d): %v", pid, err)
	}

	mine, err := syscall.Getsid(os.Getpid())
	if err != nil {
		t.Fatalf("getsid(self): %v", err)
	}
	if sid == mine {
		t.Errorf("child session %d is the caller's: a terminal signal would take the daemon with it", sid)
	}
	if sid != pid {
		t.Errorf("child session = %d, want its own (%d)", sid, pid)
	}
}
